package cli

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/rogpeppe/rog-go/parallel"
	"sourcegraph.com/sourcegraph/sourcegraph/cli/cli"

	"sourcegraph.com/sourcegraph/sourcegraph/cli/client"
	"sourcegraph.com/sourcegraph/sourcegraph/go-sourcegraph/sourcegraph"
	"sourcegraph.com/sourcegraph/sourcegraph/util/textutil"
	"sourcegraph.com/sourcegraph/sourcegraph/util/timeutil"
)

func init() {
	reposGroup, err := cli.CLI.AddCommand("repo",
		"manage repos",
		"The repo subcommands manage repos.",
		&reposCmd{},
	)
	if err != nil {
		log.Fatal(err)
	}
	reposGroup.Aliases = []string{"repos", "r"}

	_, err = reposGroup.AddCommand("get",
		"get a repo",
		"The `sgx repo get` command gets a repo.",
		&repoGetCmd{},
	)
	if err != nil {
		log.Fatal(err)
	}

	_, err = reposGroup.AddCommand("resolve",
		"resolve a repo",
		"The `src repo resolve` command resolves a repo.",
		&repoResolveCmd{},
	)
	if err != nil {
		log.Fatal(err)
	}

	listC, err := reposGroup.AddCommand("list",
		"list repos",
		"The `sgx repo list` command lists repos.",
		&repoListCmd{},
	)
	if err != nil {
		log.Fatal(err)
	}
	listC.Aliases = []string{"ls"}

	_, err = reposGroup.AddCommand("create",
		"create a repo",
		"The `sgx repo create` command creates a new repo.",
		&repoCreateCmd{},
	)
	if err != nil {
		log.Fatal(err)
	}

	_, err = reposGroup.AddCommand("update",
		"update a repo",
		"The `sgx repo update` command updates a repo.",
		&repoUpdateCmd{},
	)
	if err != nil {
		log.Fatal(err)
	}

	deleteC, err := reposGroup.AddCommand("delete",
		"delete a repo",
		"The `sgx repo rm` command deletes a repo.",
		&repoDeleteCmd{},
	)
	if err != nil {
		log.Fatal(err)
	}
	deleteC.Aliases = []string{"rm"}

	_, err = reposGroup.AddCommand("sync",
		"syncs repos and triggers builds for recent commits",
		`The 'sgx repo sync' command syncs repos and triggers builds for recent commits.

If multiple REPO-URIs are provided, the syncs are performed concurrently.


TIPS

Sync all of a person/org's repos:

	sgx repo sync `+"`"+`sgx repo list --owner USER`+"`"+`

Same as above, but for a deployed site:

	sgx env exec sgx repo sync `+"`"+`sgx env exec sgx repo list --owner USER`+"`"+`
`,
		&repoSyncCmd{},
	)
	if err != nil {
		log.Fatal(err)
	}

	_, err = reposGroup.AddCommand("refresh-vcs",
		"refresh repo VCS data",
		"The 'sgx repo refresh-vcs' command refreshes VCS data for the specified repositories.",
		&repoRefreshVCSCmd{},
	)
	if err != nil {
		log.Fatal(err)
	}

	_, err = reposGroup.AddCommand("inventory",
		"list inventory of a repo",
		"The 'src repo inventory' command lists the inventory of a repository at a specific commit (e.g., which programming languages are used).",
		&repoInventoryCmd{},
	)
	if err != nil {
		log.Fatal(err)
	}

	initRepoConfigCmds(reposGroup)
}

type reposCmd struct{}

func (c *reposCmd) Execute(args []string) error { return nil }

type repoGetCmd struct {
	Args struct {
		URI string `name:"REPO-URI" description:"repository URI (e.g., host.com/myrepo)"`
	} `positional-args:"yes" required:"yes" count:"1"`

	Config bool `long:"config" description:"also get repo config"`
}

func (c *repoGetCmd) Execute(args []string) error {
	cl := client.Client()

	repoSpec := &sourcegraph.RepoSpec{URI: c.Args.URI}

	repo, err := cl.Repos.Get(client.Ctx, repoSpec)
	if err != nil {
		return err
	}

	b, err := json.MarshalIndent(repo, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))

	if c.Config {
		conf, err := cl.Repos.GetConfig(client.Ctx, repoSpec)
		if err != nil {
			return err
		}
		log.Println()
		log.Println("# Config")
		b, err := json.MarshalIndent(conf, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
	}

	return nil
}

type repoResolveCmd struct {
	Args struct {
		Path []string `name:"PATH" description:"repository path (ex: host.com/myrepo)"`
	} `positional-args:"yes" required:"yes"`
}

func (c *repoResolveCmd) Execute(args []string) error {
	cl := client.Client()

	for _, path := range c.Args.Path {
		log.Printf("# %s", path)
		res, err := cl.Repos.Resolve(client.Ctx, &sourcegraph.RepoResolveOp{Path: path})
		if err != nil {
			return err
		}
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
	}
	return nil
}

type repoListCmd struct {
	Owner     string `long:"owner" description:"login of user whose owned repositories to list"`
	Query     string `short:"q" long:"query" description:"query"`
	Sort      string `long:"sort" description:"sort-by field"`
	Direction string `long:"direction" description:"sort direction (asc or desc)" default:"asc"`
}

func (c *repoListCmd) Execute(args []string) error {
	cl := client.Client()

	for page := 1; ; page++ {
		repos, err := cl.Repos.List(client.Ctx, &sourcegraph.RepoListOptions{
			Owner:       c.Owner,
			Query:       c.Query,
			Sort:        c.Sort,
			Direction:   c.Direction,
			ListOptions: sourcegraph.ListOptions{Page: int32(page)},
		})

		if err != nil {
			return err
		}
		if len(repos.Repos) == 0 {
			break
		}
		for _, repo := range repos.Repos {
			fmt.Println(repo.URI)
		}
	}
	return nil
}

type repoCreateCmd struct {
	Args struct {
		URI string `name:"REPO-URI" description:"desired repository URI (e.g., host.com/myrepo)"`
	} `positional-args:"yes" required:"yes" count:"1"`
	CloneURL      string `short:"u" long:"clone-url" description:"clone URL of existing repo"`
	DefaultBranch string `short:"b" long:"default-branch" description:"default branch" default:"master"`
	Mirror        bool   `short:"m" long:"mirror" description:"create the repo as a mirror"`
	Description   string `short:"d" long:"description" description:"repo description"`
	Language      string `short:"l" long:"lang" description:"primary programming language"`
}

func (c *repoCreateCmd) Execute(args []string) error {
	cl := client.Client()

	repo, err := cl.Repos.Create(client.Ctx, &sourcegraph.ReposCreateOp{
		Op: &sourcegraph.ReposCreateOp_New{
			New: &sourcegraph.ReposCreateOp_NewRepo{
				URI:           c.Args.URI,
				CloneURL:      c.CloneURL,
				DefaultBranch: c.DefaultBranch,
				Mirror:        c.Mirror,
				Description:   c.Description,
				Language:      c.Language,
			},
		},
	})
	if err != nil {
		return err
	}

	fmt.Println(repo.HTMLURL)
	return nil
}

type repoUpdateCmd struct {
	Args struct {
		URI string `name:"REPO-URI" description:"desired repository URI (e.g., host.com/myrepo)"`
	} `positional-args:"yes" required:"yes" count:"1"`
	Description string `long:"description" description:"new description for repository"`
	Language    string `short:"l" long:"lang" description:"new primary programming language for repository"`
}

func (c *repoUpdateCmd) Execute(args []string) error {
	cl := client.Client()

	repo, err := cl.Repos.Update(client.Ctx, &sourcegraph.ReposUpdateOp{
		Repo:        sourcegraph.RepoSpec{URI: c.Args.URI},
		Description: c.Description,
		Language:    c.Language,
	})
	if err != nil {
		return err
	}
	log.Printf("# updated: %s", repo.URI)
	return nil
}

type repoDeleteCmd struct {
	Args struct {
		URIs []string `name:"REPO-URIs" description:"repository URIs to delete (e.g., host.com/myrepo)"`
	} `positional-args:"yes" required:"yes"`
}

func (c *repoDeleteCmd) Execute(args []string) error {
	cl := client.Client()

	for _, uri := range c.Args.URIs {
		if _, err := cl.Repos.Delete(client.Ctx, &sourcegraph.RepoSpec{URI: uri}); err != nil {
			return err
		}
		log.Printf("# deleted: %s", uri)
	}
	return nil
}

type repoSyncCmd struct {
	Force bool `long:"force" short:"f" description:"force rebuild even if build exists for the latest commit"`

	Args struct {
		URIs []string `name:"REPO-URI" description:"repository URIs (e.g., host.com/myrepo)"`
	} `positional-args:"yes" required:"yes"`
}

func (c *repoSyncCmd) Execute(args []string) error {
	par := parallel.NewRun(30)
	for _, repo_ := range c.Args.URIs {
		repo := repo_
		par.Do(func() error {
			if err := c.sync(repo); err != nil {
				return fmt.Errorf(red("%s:")+" %s", repo, err)
			}
			return nil
		})
	}
	if err := par.Wait(); err != nil {
		if errs, ok := err.(parallel.Errors); ok {
			for _, err := range errs {
				log.Println(err)
			}
			return fmt.Errorf("encountered %d errors (see above)", len(errs))
		}
		return err
	}
	return nil
}

func (c *repoSyncCmd) sync(repoURI string) error {
	log := log.New(os.Stderr, cyan(strings.TrimPrefix(strings.TrimPrefix(repoURI+": ", "github.com/"), "sourcegraph.com/")), 0)

	cl := client.Client()

	repoSpec := sourcegraph.RepoSpec{URI: repoURI}

	repo, err := cl.Repos.Get(client.Ctx, &repoSpec)
	if err != nil {
		return err
	}

	repoRevSpec := sourcegraph.RepoRevSpec{RepoSpec: repo.RepoSpec(), Rev: repo.DefaultBranch}
	commit, err := cl.Repos.GetCommit(client.Ctx, &repoRevSpec)
	if err != nil {
		return err
	}
	repoRevSpec.CommitID = string(commit.ID)
	log.Printf("Got latest commit %s (%s): %s (%s %s).", commit.ID[:8], repo.DefaultBranch, textutil.Truncate(50, commit.Message), commit.Author.Email, timeutil.TimeAgo(commit.Author.Date))

	builds, err := cl.Builds.List(client.Ctx, &sourcegraph.BuildListOptions{
		Repo:      repoRevSpec.URI,
		CommitID:  repoRevSpec.CommitID,
		Succeeded: true,
	})
	if err != nil {
		return err
	}
	if c.Force || len(builds.Builds) == 0 {
		b, err := cl.Builds.Create(client.Ctx, &sourcegraph.BuildsCreateOp{
			Repo:     repoRevSpec.RepoSpec,
			CommitID: repoRevSpec.CommitID,
			Config:   sourcegraph.BuildConfig{Queue: true},
		})
		if err != nil {
			return err
		}
		log.Printf("Created build #%s for commit %s.", b.Spec().IDString(), commit.ID[:8])
	} else if err != nil {
		return err
	} else {
		log.Printf("Latest commit is already built.")
	}

	return nil
}

type repoRefreshVCSCmd struct {
	Args struct {
		URIs []string `name:"REPO-URI" description:"repository URIs (e.g., host.com/myrepo)"`
	} `positional-args:"yes" required:"yes"`
}

func (c *repoRefreshVCSCmd) Execute(args []string) error {
	cl := client.Client()
	for _, repoURI := range c.Args.URIs {
		repo, err := cl.Repos.Get(client.Ctx, &sourcegraph.RepoSpec{URI: repoURI})
		if err != nil {
			return err
		}

		repoRevSpec := sourcegraph.RepoRevSpec{RepoSpec: repo.RepoSpec(), Rev: repo.DefaultBranch}
		preCommit, err := cl.Repos.GetCommit(client.Ctx, &repoRevSpec)
		if err != nil {
			return err
		}

		if _, err := cl.MirrorRepos.RefreshVCS(client.Ctx, &sourcegraph.MirrorReposRefreshVCSOp{Repo: repoRevSpec.RepoSpec}); err != nil {
			return err
		}

		postCommit, err := cl.Repos.GetCommit(client.Ctx, &repoRevSpec)
		if err != nil {
			return err
		}

		if preCommit.ID == postCommit.ID {
			log.Printf("%s: latest commit on %s unchanged: %s %s", repo.URI, repo.DefaultBranch, preCommit.ID, timeutil.TimeAgo(preCommit.Author.Date))
		} else {
			log.Printf("%s: updated latest commit on %s: %s %s (was %s %s)", repo.URI, repo.DefaultBranch, postCommit.ID, timeutil.TimeAgo(postCommit.Author.Date), preCommit.ID, timeutil.TimeAgo(preCommit.Author.Date))
		}
	}
	return nil
}

type repoInventoryCmd struct {
	Args struct {
		Repo string `name:"REPO" description:"repository URI (e.g., host.com/myrepo)"`
	} `positional-args:"yes" required:"yes"`

	Rev string `long:"rev" description:"revision (if unset, uses default branch)"`
}

func (c *repoInventoryCmd) Execute(args []string) error {
	cl := client.Client()

	inv, err := cl.Repos.GetInventory(client.Ctx, &sourcegraph.RepoRevSpec{
		RepoSpec: sourcegraph.RepoSpec{URI: c.Args.Repo},
		Rev:      c.Rev,
	})
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}