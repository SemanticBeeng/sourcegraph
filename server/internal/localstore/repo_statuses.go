package localstore

import (
	"time"

	"golang.org/x/net/context"
	"sourcegraph.com/sourcegraph/sourcegraph/go-sourcegraph/sourcegraph"
	"sourcegraph.com/sourcegraph/sourcegraph/server/accesscontrol"
	"sourcegraph.com/sourcegraph/sourcegraph/store"
	"sourcegraph.com/sqs/pbtypes"
)

func init() {
	AppSchema.Map.AddTableWithName(dbRepoStatus{}, "repo_status").SetKeys(false, "Repo", "Rev", "Context")
	AppSchema.CreateSQL = append(AppSchema.CreateSQL,
		`ALTER TABLE repo_status ALTER COLUMN repo TYPE citext;`,
		`ALTER TABLE repo_status ALTER COLUMN description TYPE text;`,
		`ALTER TABLE repo_status ALTER COLUMN created_at TYPE timestamp with time zone USING updated_at::timestamp with time zone;`,
		`ALTER TABLE repo_status ALTER COLUMN updated_at TYPE timestamp with time zone USING updated_at::timestamp with time zone;`,
	)
}

// dbRepoStatus DB-maps a sourcegraph.RepoStatus object.
type dbRepoStatus struct {
	Repo        string
	Rev         string
	State       string
	TargetURL   string `db:"target_url"`
	Description string
	Context     string
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   *time.Time `db:"updated_at"`
}

func (r *dbRepoStatus) toRepoStatus() *sourcegraph.RepoStatus {
	r2 := &sourcegraph.RepoStatus{
		State:       r.State,
		TargetURL:   r.TargetURL,
		Description: r.Description,
		Context:     r.Context,
		CreatedAt:   pbtypes.NewTimestamp(r.CreatedAt),
	}

	if r.UpdatedAt != nil {
		r2.UpdatedAt = pbtypes.NewTimestamp(*r.UpdatedAt)
	}

	return r2
}

func (r *dbRepoStatus) fromRepoStatus(repoRev *sourcegraph.RepoRevSpec, r2 *sourcegraph.RepoStatus) {
	r.Repo = repoRev.URI
	if repoRev.CommitID != "" {
		r.Rev = repoRev.CommitID
	} else {
		r.Rev = repoRev.Rev
	}
	r.State = r2.State
	r.TargetURL = r2.TargetURL
	r.Description = r2.Description
	r.Context = r2.Context
	r.CreatedAt = r2.CreatedAt.Time()
	if !r2.UpdatedAt.Time().IsZero() {
		ts := r2.UpdatedAt.Time()
		r.UpdatedAt = &ts
	}
}

func toRepoStatuses(rs []*dbRepoStatus) []*sourcegraph.RepoStatus {
	r2s := make([]*sourcegraph.RepoStatus, len(rs))
	for i, r := range rs {
		r2s[i] = r.toRepoStatus()
	}
	return r2s
}

type repoStatuses struct{}

var _ store.RepoStatuses = (*repoStatuses)(nil)

func (s *repoStatuses) GetCombined(ctx context.Context, repoRev sourcegraph.RepoRevSpec) (*sourcegraph.CombinedStatus, error) {
	if err := accesscontrol.VerifyUserHasReadAccess(ctx, "RepoStatuses.GetCombined", repoRev.URI); err != nil {
		return nil, err
	}
	var rev string
	if repoRev.CommitID != "" {
		rev = repoRev.CommitID
	} else {
		rev = repoRev.Rev
	}

	var dbRepoStatuses []*dbRepoStatus
	if _, err := appDBH(ctx).Select(&dbRepoStatuses, `SELECT * FROM repo_status WHERE repo=$1 AND rev=$2 ORDER BY created_at ASC;`, repoRev.URI, rev); err != nil {
		return nil, err
	}
	return &sourcegraph.CombinedStatus{
		Rev:      repoRev.Rev,
		CommitID: repoRev.CommitID,
		Statuses: toRepoStatuses(dbRepoStatuses),
	}, nil
}

func (s *repoStatuses) Create(ctx context.Context, repoRev sourcegraph.RepoRevSpec, status *sourcegraph.RepoStatus) error {
	if err := accesscontrol.VerifyUserHasWriteAccess(ctx, "RepoStatuses.Create", repoRev.URI); err != nil {
		return err
	}

	var dbRepoStatus dbRepoStatus
	dbRepoStatus.fromRepoStatus(&repoRev, status)
	if dbRepoStatus.CreatedAt.Unix() == 0 {
		dbRepoStatus.CreatedAt = time.Now()
	}

	// Upsert the status. Note that this is correct, because repo
	// statuses cannot be deleted. It is more robust to write it this
	// way than with inline SQL (which would have to be manually
	// updated if the fields of RepoStatus changed).
	err := appDBH(ctx).Insert(&dbRepoStatus)
	if err != nil {
		_, err := appDBH(ctx).Update(&dbRepoStatus)
		return err
	}
	return err
}