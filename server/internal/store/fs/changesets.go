package fs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"gopkg.in/inconshreveable/log15.v2"

	"golang.org/x/net/context"
	"golang.org/x/tools/godoc/vfs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"sourcegraph.com/sourcegraph/go-vcs/vcs"
	"sourcegraph.com/sqs/pbtypes"
	"src.sourcegraph.com/sourcegraph/go-sourcegraph/sourcegraph"
	"src.sourcegraph.com/sourcegraph/notif"
	"src.sourcegraph.com/sourcegraph/platform/storage"
	"src.sourcegraph.com/sourcegraph/store"
	"src.sourcegraph.com/sourcegraph/svc"
	sfs "src.sourcegraph.com/vfs"
)

const (
	reviewRef             = "refs/src/review"
	changesetMetadataFile = "changeset.json"
	changesetReviewsFile  = "reviews.json"
	changesetEventsFile   = "events.json"

	changesetIndexOpenDir   = "index/open"
	changesetIndexClosedDir = "index/closed"

	defaultCommitMessageTmpl = "{{.Title}}\n\nMerge changeset #{{.ID}}\n\n{{.Description}}"
)

type Changesets struct {
	fsLock sync.RWMutex // guards FS
}

func (s *Changesets) Create(ctx context.Context, repoPath string, cs *sourcegraph.Changeset) error {
	s.migrate(ctx, repoPath)
	s.fsLock.Lock()
	defer s.fsLock.Unlock()

	fs := s.storage(ctx, repoPath)
	dir := absolutePathForRepo(ctx, repoPath)
	repo, err := vcs.Open("git", dir)

	cs.ID, err = resolveNextChangesetID(fs)
	if err != nil {
		return err
	}

	// Update the index with the changeset's current state.
	s.updateIndex(ctx, fs, cs.ID, true)

	ts := pbtypes.NewTimestamp(time.Now())
	cs.CreatedAt = &ts
	id := strconv.FormatInt(cs.ID, 10)
	head, err := repo.ResolveBranch(cs.DeltaSpec.Head.Rev)
	if err != nil {
		return err
	}
	cs.DeltaSpec.Head.CommitID = string(head)
	if err := updateRef(dir, "refs/changesets/"+id+"/head", string(head)); err != nil {
		return err
	}
	return writeChangeset(ctx, fs, cs)
}

// writeChangeset writes the given changeset into the repository specified by
// repoPath. When committing the change, msg is used as the commit message to
// which the changeset ID is appended.
//
// Callers must guard by holding the s.fsLock lock.
func writeChangeset(ctx context.Context, fs sfs.FileSystem, cs *sourcegraph.Changeset) error {
	b, err := json.MarshalIndent(cs, "", "\t")
	if err != nil {
		return err
	}
	p := filepath.Join(strconv.FormatInt(cs.ID, 10), changesetMetadataFile)
	f, err := fs.Open(p)
	if err != nil && os.IsNotExist(err) {
		f, err = fs.Create(p)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}

func resolveNextChangesetID(fs sfs.FileSystem) (int64, error) {
	fis, err := fs.ReadDir(".")
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}
	max := int64(0)
	for _, fi := range fis {
		if !fi.IsDir() {
			continue
		}
		n, err := strconv.Atoi(fi.Name())
		if err != nil {
			continue
		}
		if n := int64(n); n > max {
			max = n
		}
	}
	return max + 1, nil
}

func (s *Changesets) Get(ctx context.Context, repoPath string, ID int64) (*sourcegraph.Changeset, error) {
	s.migrate(ctx, repoPath)
	s.fsLock.RLock()
	defer s.fsLock.RUnlock()

	return s.get(ctx, repoPath, ID)
}

// callers must guard
func (s *Changesets) get(ctx context.Context, repoPath string, ID int64) (*sourcegraph.Changeset, error) {
	fs := s.storage(ctx, repoPath)
	f, err := fs.Open(filepath.Join(strconv.FormatInt(ID, 10), changesetMetadataFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cs := &sourcegraph.Changeset{}
	return cs, json.NewDecoder(f).Decode(cs)
}

// getReviewRefTip returns the commit ID that is at the tip of the reference where
// the changeset data is stored.
//
// Callers must guard by holding the s.fsLock lock.
func (s *Changesets) getReviewRefTip(repo vcs.Repository) (vcs.CommitID, error) {
	refResolver, ok := repo.(refResolver)
	if !ok {
		return "", &os.PathError{Op: "getReviewRefTip", Path: reviewRef, Err: os.ErrNotExist}
	}
	id, err := refResolver.ResolveRef(reviewRef)
	if err == vcs.ErrRefNotFound {
		return "", &os.PathError{Op: "getReviewRefTip", Path: reviewRef, Err: os.ErrNotExist}
	}
	return id, err
}

func (s *Changesets) CreateReview(ctx context.Context, repoPath string, changesetID int64, newReview *sourcegraph.ChangesetReview) (*sourcegraph.ChangesetReview, error) {
	s.migrate(ctx, repoPath)
	s.fsLock.Lock()
	defer s.fsLock.Unlock()
	fs := s.storage(ctx, repoPath)

	// Read current reviews into structure
	all := sourcegraph.ChangesetReviewList{Reviews: []*sourcegraph.ChangesetReview{}}
	err := s.readFile(ctx, fs, changesetID, changesetReviewsFile, &all.Reviews)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Append new review to list
	newReview.ID = int64(len(all.Reviews) + 1)
	all.Reviews = append(all.Reviews, newReview)

	b, err := json.MarshalIndent(all.Reviews, "", "\t")
	if err != nil {
		return nil, err
	}
	p := filepath.Join(strconv.FormatInt(changesetID, 10), changesetReviewsFile)
	f, err := fs.Open(p)
	if err != nil && os.IsNotExist(err) {
		f, err = fs.Create(p)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_, err = f.Write(b)
	return newReview, err
}

func (s *Changesets) ListReviews(ctx context.Context, repo string, changesetID int64) (*sourcegraph.ChangesetReviewList, error) {
	s.migrate(ctx, repo)
	s.fsLock.RLock()
	defer s.fsLock.RUnlock()
	fs := s.storage(ctx, repo)

	list := &sourcegraph.ChangesetReviewList{Reviews: []*sourcegraph.ChangesetReview{}}
	err := s.readFile(ctx, fs, changesetID, changesetReviewsFile, &list.Reviews)
	if os.IsNotExist(err) {
		err = nil
	}
	return list, err
}

// readFile JSON decodes the contents of the file named 'filename' from
// the folder of the given changeset into v.
//
// Callers must guard by holding the s.fsLock lock.
func (s *Changesets) readFile(ctx context.Context, fs sfs.FileSystem, changesetID int64, filename string, v interface{}) error {
	f, err := fs.Open(filepath.Join(strconv.FormatInt(changesetID, 10), filename))
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}

var errInvalidUpdateOp = errors.New("invalid update operation")

func (s *Changesets) Update(ctx context.Context, opt *store.ChangesetUpdateOp) (*sourcegraph.ChangesetEvent, error) {
	s.migrate(ctx, opt.Op.Repo.URI)
	s.fsLock.Lock()
	defer s.fsLock.Unlock()
	fs := s.storage(ctx, opt.Op.Repo.URI)

	op := opt.Op

	dir := absolutePathForRepo(ctx, op.Repo.URI)
	if (op.Close && op.Open) || (op.Open && op.Merged) {
		return nil, errInvalidUpdateOp
	}
	current, err := s.get(ctx, op.Repo.URI, op.ID)
	if err != nil {
		return nil, err
	}
	after := *current
	ts := pbtypes.NewTimestamp(time.Now())

	if op.Title != "" {
		after.Title = op.Title
	}
	if op.Description != "" {
		after.Description = op.Description
	}
	if op.Open {
		after.ClosedAt = nil
	}
	if op.Close && (current.ClosedAt == nil || (op.Merged && !current.Merged)) {
		after.ClosedAt = &ts
	}
	if op.Merged {
		after.Merged = true
	}

	// Update the index with the changeset's current state.
	s.updateIndex(ctx, fs, op.ID, after.ClosedAt == nil)

	if opt.Head != "" {
		// We need to track the tip of this branch so that we can access its
		// data even after a potential deletion or merge.
		id := strconv.FormatInt(current.ID, 10)
		if err := updateRef(dir, "refs/changesets/"+id+"/head", opt.Head); err != nil {
			return nil, err
		}
		after.DeltaSpec.Head.CommitID = opt.Head
	}
	if opt.Base != "" {
		id := strconv.FormatInt(current.ID, 10)
		if err := updateRef(dir, "refs/changesets/"+id+"/base", opt.Base); err != nil {
			return nil, err
		}
		after.DeltaSpec.Base.CommitID = opt.Base
	}

	// If the resulting changeset is the same as the inital one, no event
	// has occurred.
	if reflect.DeepEqual(after, current) {
		return &sourcegraph.ChangesetEvent{}, nil
	}

	err = writeChangeset(ctx, fs, &after)
	if err != nil {
		return nil, err
	}

	var evt *sourcegraph.ChangesetEvent
	if shouldRegisterEvent(op) {
		evt = &sourcegraph.ChangesetEvent{
			Before:    current,
			After:     &after,
			Op:        op,
			CreatedAt: &ts,
		}
		evts := []*sourcegraph.ChangesetEvent{}
		err = s.readFile(ctx, fs, op.ID, changesetEventsFile, &evts)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		evts = append(evts, evt)
		b, err := json.MarshalIndent(evts, "", "\t")
		if err != nil {
			return nil, err
		}
		p := filepath.Join(strconv.FormatInt(op.ID, 10), changesetEventsFile)
		f, err := fs.Open(p)
		if err != nil && os.IsNotExist(err) {
			f, err = fs.Create(p)
		}
		if err != nil {
			return nil, err
		}
		defer f.Close()
		_, err = f.Write(b)
		return evt, err
	}

	return evt, nil
}

func (s *Changesets) Merge(ctx context.Context, opt *sourcegraph.ChangesetMergeOp) error {
	s.migrate(ctx, opt.Repo.URI)
	cs, _ := s.Get(ctx, opt.Repo.URI, opt.ID)
	base, head := cs.DeltaSpec.Base.Rev, cs.DeltaSpec.Head.Rev
	if cs.Merged {
		return grpc.Errorf(codes.FailedPrecondition, "changeset #%d already merged", cs.ID)
	}
	if cs.ClosedAt != nil {
		return grpc.Errorf(codes.FailedPrecondition, "changeset #%d already closed", cs.ID)
	}

	repo, err := svc.Repos(ctx).Get(ctx, &sourcegraph.RepoSpec{
		URI: opt.Repo.URI,
	})
	if err != nil {
		return err
	}

	var msgTmpl string
	if opt.Message != "" {
		msgTmpl = opt.Message
	} else {
		msgTmpl = defaultCommitMessageTmpl
	}
	msg, err := mergeCommitMessage(cs, msgTmpl)
	if err != nil {
		return err
	}

	dir := absolutePathForRepo(ctx, repo.URI)
	rs, err := NewRepoStage(dir, base)
	if err != nil {
		return err
	}
	defer rs.Free()

	p := notif.PersonFromContext(ctx)
	if err != nil {
		return err
	}
	merger := vcs.Signature{
		Name:  p.FullName,
		Email: p.Email,
		Date:  pbtypes.NewTimestamp(time.Now()),
	}

	if err := rs.Pull(head, opt.Squash); err != nil {
		return err
	}
	if err := rs.Commit(merger, merger, msg); err != nil {
		return err
	}

	return nil
}

func mergeCommitMessage(cs *sourcegraph.Changeset, msgTmpl string) (string, error) {
	t, err := template.New("commit message").Parse(msgTmpl)
	if err != nil {
		return "", err
	}

	var b bytes.Buffer
	err = t.Execute(&b, cs)
	if err != nil {
		return "", err
	}

	return b.String(), nil
}

// shouldRegisterEvent returns true if the passed update operation will cause an event
// that needs to be registered. Registered events are be visible in the changeset's
// timeline.
func shouldRegisterEvent(op *sourcegraph.ChangesetUpdateOp) bool {
	return op.Merged || op.Close || op.Open || op.Title != "" || op.Description != ""
}

func (s *Changesets) List(ctx context.Context, op *sourcegraph.ChangesetListOp) (*sourcegraph.ChangesetList, error) {
	s.migrate(ctx, op.Repo)
	s.fsLock.RLock()
	defer s.fsLock.RUnlock()
	fs := s.storage(ctx, op.Repo)

	// by default, retrieve all changesets
	if !op.Open && !op.Closed {
		op.Open = true
		op.Closed = true
	}
	list := sourcegraph.ChangesetList{Changesets: []*sourcegraph.Changeset{}}
	fis, err := fs.ReadDir(".")
	if err != nil {
		if os.IsNotExist(err) {
			return &list, nil
		}
		return nil, err
	}

	var buildIndex bool // auto-migration

	var open, closed map[int64]struct{}
	if op.Open {
		open, err = s.indexList(ctx, fs, changesetIndexOpenDir)
		if open == nil {
			buildIndex = true
		}
	}
	if op.Closed {
		closed, err = s.indexList(ctx, fs, changesetIndexClosedDir)
		if closed == nil {
			buildIndex = true
		}
	}
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, fi := range fis {
		// skip files
		if !fi.IsDir() {
			continue
		}

		// skip non-numeric folders
		id, err := strconv.ParseInt(fi.Name(), 10, 64)
		if err != nil {
			continue
		}

		// If requesting only open changests, check the index.
		if op.Open && !buildIndex {
			if _, ok := open[id]; !ok {
				continue
			}
		}

		// If requesting only closed changesets, check the index.
		if op.Closed && !buildIndex {
			if _, ok := closed[id]; !ok {
				continue
			}
		}
		paths = append(paths, filepath.Join(fi.Name(), changesetMetadataFile))
	}

	// Sort the filepaths by ID in reverse, i.e. descending order. We use
	// descending order as higher IDs were created most recently.
	//
	// TODO(slimsag): let the user choose the sorting order.
	sort.Sort(sort.Reverse(byChangesetID(paths)))

	// TODO(slimsag): we currently do not index by Head/Base like we should. For
	// now, we special case it by iterating over each path element, which is
	// slower.
	headBaseSearch := op.Head != "" || op.Base != ""
	if !buildIndex && !headBaseSearch {
		start := op.Offset()
		if start < 0 {
			start = 0
		}
		if start > len(paths) {
			start = len(paths)
		}
		end := start + op.Limit()
		if end < 0 {
			end = 0
		}
		if end > len(paths) {
			end = len(paths)
		}
		paths = paths[start:end]
	}

	// Read each metadata file from the VFS.
	skip := op.Offset()
	for _, p := range paths {
		f, err := fs.Open(p)
		if err != nil {
			return nil, err
		}
		var cs sourcegraph.Changeset
		err = json.NewDecoder(f).Decode(&cs)
		f.Close()
		if err != nil {
			return nil, err
		}

		// Handle auto-migration of data (create the index).
		if buildIndex {
			// Exit our read lock and acquire write lock.
			s.fsLock.RUnlock()
			s.fsLock.Lock()

			// Update the index.
			if err := s.updateIndex(ctx, fs, cs.ID, cs.ClosedAt == nil); err != nil {
				log.Println("changeset data migration failure:", err)
			}

			// Exit our write lock and reacquire read lock.
			s.fsLock.Unlock()
			s.fsLock.RLock()
		}

		// If we're only interested in a changeset with a specific branch for head
		// or base, check that now or simply continue.
		if (op.Head != "" && op.Head != cs.DeltaSpec.Head.Rev) || (op.Base != "" && op.Base != cs.DeltaSpec.Base.Rev) {
			continue
		}

		// Handle offset.
		if !buildIndex && headBaseSearch && skip > 0 {
			skip--
			continue
		}

		list.Changesets = append(list.Changesets, &cs)

		// If we're not migrating data, abort early once we have gotten enough
		// changesets.
		if !buildIndex && headBaseSearch && len(list.Changesets) >= op.Limit() {
			break
		}
	}
	return &list, nil
}

func (s *Changesets) ListEvents(ctx context.Context, spec *sourcegraph.ChangesetSpec) (*sourcegraph.ChangesetEventList, error) {
	s.migrate(ctx, spec.Repo.URI)
	fs := s.storage(ctx, spec.Repo.URI)
	list := sourcegraph.ChangesetEventList{Events: []*sourcegraph.ChangesetEvent{}}
	err := s.readFile(ctx, fs, spec.ID, changesetEventsFile, &list.Events)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return &list, nil
}

// updateIndex updates the index with the given changeset state (whether or not
// it is opened or closed).
//
// Callers must guard by holding the s.fsLock lock.
func (s *Changesets) updateIndex(ctx context.Context, fs sfs.FileSystem, cid int64, open bool) error {
	var adds, removes []string
	if open {
		// Changeset opened.
		adds = append(adds, changesetIndexOpenDir)
		removes = append(removes, changesetIndexClosedDir)
	} else {
		// Changeset closed.
		adds = append(adds, changesetIndexClosedDir)
		removes = append(removes, changesetIndexOpenDir)
	}

	// Perform additions.
	for _, indexDir := range adds {
		if err := s.indexAdd(ctx, fs, cid, indexDir); err != nil {
			return err
		}
	}

	// Perform removals.
	for _, indexDir := range removes {
		if err := s.indexRemove(ctx, fs, cid, indexDir); err != nil {
			return err
		}
	}
	return nil
}

// indexAdd adds the given changeset ID to the index directory if it does not
// already exist.
//
// Callers must guard by holding the s.fsLock lock.
func (s *Changesets) indexAdd(ctx context.Context, fs sfs.FileSystem, cid int64, indexDir string) error {
	// If the file exists nothing needs to be done.
	if _, err := s.indexStatName(ctx, fs, cid, indexDir); err == nil {
		return nil
	}

	p := filepath.Join(indexDir, strconv.FormatInt(cid, 10))
	f, err := fs.Create(p)
	if err != nil {
		return err
	}
	f.Close()
	return nil
}

// indexRemove removes the given changeset ID from the index directory if it
// exists.
//
// Callers must guard by holding the s.fsLock lock.
func (s *Changesets) indexRemove(ctx context.Context, fs sfs.FileSystem, cid int64, indexDir string) error {
	// If the file does not exist nothing needs to be done.
	if _, err := s.indexStatName(ctx, fs, cid, indexDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}

	return fs.RemoveAll(filepath.Join(indexDir, strconv.FormatInt(cid, 10)))
}

// indexList returns a list of changeset IDs found in the given index directory.
//
// Callers must guard by holding the s.fsLock lock.
func (s *Changesets) indexList(ctx context.Context, fs sfs.FileSystem, indexDir string) (map[int64]struct{}, error) {
	infos, err := fs.ReadDir(indexDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	ids := make(map[int64]struct{})
	for _, fi := range infos {
		id, err := strconv.ParseInt(fi.Name(), 10, 64)
		if err != nil {
			return nil, err
		}
		ids[id] = struct{}{}
	}
	return ids, nil
}

// indexStat stats the changeset ID in the given index directory.
//
// Callers must guard by holding the s.fsLock lock.
func (s *Changesets) indexStatName(ctx context.Context, fs sfs.FileSystem, cid int64, indexDir string) (os.FileInfo, error) {
	return fs.Stat(filepath.Join(indexDir, strconv.FormatInt(cid, 10)))
}

func (s *Changesets) storage(ctx context.Context, repoPath string) sfs.FileSystem {
	return storage.Namespace(ctx, "changesets", repoPath)
}

var (
	csMigrateMusMu sync.Mutex
	csMigrateMus   map[string]*sync.Once
)

func csGetMigrateOnce(repo string) *sync.Once {
	csMigrateMusMu.Lock()
	defer csMigrateMusMu.Unlock()
	if csMigrateMus == nil {
		csMigrateMus = map[string]*sync.Once{}
	}
	if once, ok := csMigrateMus[repo]; ok {
		return once
	}
	once := &sync.Once{}
	csMigrateMus[repo] = once
	return once
}

func (s *Changesets) migrate(ctx context.Context, repoPath string) {
	once := csGetMigrateOnce(repoPath)
	once.Do(func() {
		fs := s.storage(ctx, repoPath)
		if _, err := fs.Stat("version"); err == nil {
			// Migration has already happened
			return
		}
		log15.Info("Starting migration of Changesets to Platform Storage", "repo", repoPath)
		err := s.doMigration(ctx, repoPath, fs)
		if err != nil {
			log15.Info("Changesets Migration failed", "repo", repoPath, "error", err)
		} else {
			log15.Info("Finished migration of Changesets to Platform Storage", "repo", repoPath)
		}
	})
}

func vfsRecursiveCopy(from vfs.FileSystem, to sfs.FileSystem, dir string) error {
	children, err := from.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, c := range children {
		p := filepath.Join(dir, c.Name())
		if c.IsDir() {
			err = vfsRecursiveCopy(from, to, p)
			if err != nil {
				return err
			}
		} else {
			src, err := from.Open(p)
			if err != nil {
				return err
			}
			dst, err := to.Create(p)
			if err != nil {
				src.Close()
				return err
			}
			_, err = io.Copy(dst, src)
			src.Close()
			dst.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Changesets) doMigration(ctx context.Context, repo string, to sfs.FileSystem) error {
	dir := absolutePathForRepo(ctx, repo)
	r, err := vcs.Open("git", dir)
	if err != nil {
		return err
	}
	cid, err := s.getReviewRefTip(r)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// Changesets is empty, no migration necessary
	} else {
		from, err := r.FileSystem(cid)
		if err != nil {
			return err
		}
		err = vfsRecursiveCopy(from, to, "/")
		if err != nil {
			return err
		}
	}
	f, err := to.Create("version")
	if err != nil {
		return err
	}
	return f.Close()
}

type byChangesetID []string

func (v byChangesetID) Len() int      { return len(v) }
func (v byChangesetID) Swap(i, j int) { v[i], v[j] = v[j], v[i] }
func (v byChangesetID) Less(i, j int) bool {
	return v.id(v[i]) < v.id(v[j])
}
func (v byChangesetID) id(path string) int64 {
	split := strings.Split(path, string(os.PathSeparator))
	id, err := strconv.ParseInt(split[len(split)-2], 10, 64)
	if err != nil {
		panic(err)
	}
	return id
}
