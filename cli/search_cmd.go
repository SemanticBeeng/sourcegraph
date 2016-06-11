package cli

import (
	"bufio"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/rogpeppe/rog-go/parallel"

	"sourcegraph.com/sourcegraph/sourcegraph/api/sourcegraph"
	"sourcegraph.com/sourcegraph/sourcegraph/cli/cli"
)

func init() {
	_, err := cli.CLI.AddCommand("search",
		"search code indexed on Sourcegraph",
		"Search code indexed on Sourcegraph.",
		&searchCmd{},
	)
	if err != nil {
		log.Fatal(err)
	}
}

type searchCmd struct {
	RefreshList      string `long:"refresh-list" description:"refresh every branch for all repositories in given file list"`
	RefreshListRate  int    `long:"refresh-list-rate" description:"how many repositories to refresh in parallel" default:"8"`
	RefreshListStart int    `long:"refresh-list-start" description:"start at the given index instead of repo 0 in the list"`

	Refresh       string `long:"refresh" description:"repository URI for which to update the search index and counts (implies --refresh-counts)"`
	RefreshCounts string `long:"refresh-counts" description:"repository URI for which to update the search counts"`
	Limit         int32  `long:"limit" description:"limit # of search results" default:"10"`
	Args          struct {
		Query []string `name:"QUERY" description:"search query"`
	} `positional-args:"yes" required:"yes"`
}

func (c *searchCmd) Execute(args []string) error {
	cl := cliClient
	if c.RefreshList != "" {
		file, err := os.Open(c.RefreshList)
		if err != nil {
			return err
		}
		defer file.Close()

		var (
			scanner = bufio.NewScanner(file)
			run     = parallel.NewRun(c.RefreshListRate)

			indexMu sync.Mutex
			index   = -1
		)
		for scanner.Scan() {
			indexMu.Lock()
			index++
			index := index
			indexMu.Unlock()

			if index < c.RefreshListStart {
				continue
			}
			repoURI := scanner.Text()

			run.Do(func() error {
				log.Printf("Refreshing %d: %q\n", index, repoURI)
				res, err := cl.Repos.Resolve(cliContext, &sourcegraph.RepoResolveOp{Path: repoURI})
				if err != nil {
					log.Println(err)
					return nil
				}
				_, err = cl.Defs.RefreshIndex(cliContext, &sourcegraph.DefsRefreshIndexOp{
					Repo:                res.Repo,
					RefreshRefLocations: true,
					Force:               true,
				})
				if err != nil {
					log.Println(err)
					return nil
				}
				_, err = cl.Search.RefreshIndex(cliContext, &sourcegraph.SearchRefreshIndexOp{
					Repos:         []int32{res.Repo},
					RefreshCounts: true,
					RefreshSearch: true,
				})
				if err != nil {
					log.Println(err)
					return nil
				}
				return nil
			})
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		return run.Wait()
	} else if c.Refresh != "" {
		log.Printf("Def.RefreshIndex")
		res, err := cl.Repos.Resolve(cliContext, &sourcegraph.RepoResolveOp{Path: c.Refresh})
		if err != nil {
			return err
		}

		_, err = cl.Defs.RefreshIndex(cliContext, &sourcegraph.DefsRefreshIndexOp{
			Repo:                res.Repo,
			RefreshRefLocations: true,
			Force:               true,
		})
		if err != nil {
			return err
		}
		log.Printf("Search.RefreshIndex")
		_, err = cl.Search.RefreshIndex(cliContext, &sourcegraph.SearchRefreshIndexOp{
			Repos:         []int32{res.Repo},
			RefreshCounts: true,
			RefreshSearch: true,
		})
		if err != nil {
			return err
		}
		log.Printf("refresh complete")
		return nil
	} else if c.RefreshCounts != "" {
		res, err := cl.Repos.Resolve(cliContext, &sourcegraph.RepoResolveOp{Path: c.RefreshCounts})
		if err != nil {
			return err
		}

		log.Printf("Search.RefreshIndex (counts only)")
		_, err = cl.Search.RefreshIndex(cliContext, &sourcegraph.SearchRefreshIndexOp{
			Repos:         []int32{res.Repo},
			RefreshCounts: true,
		})
		if err != nil {
			return err
		}
		log.Printf("refresh complete")
		return nil
	}

	query := strings.Join(c.Args.Query, " ")
	if query == "" {
		log.Fatal("src search: empty query")
	}

	results, err := cl.Search.Search(cliContext, &sourcegraph.SearchOp{
		Query: query,
		Opt: &sourcegraph.SearchOptions{
			ListOptions: sourcegraph.ListOptions{
				PerPage: c.Limit,
			},
		},
	})
	if err != nil {
		return err
	}

	if len(results.DefResults) == 0 {
		log.Printf("No results found.\n")
		return nil
	}

	for _, r := range results.DefResults {
		name, link := parseDef(&r.Def)
		log.Printf("%6.2f "+bold("%s")+" <%s>\n", r.Score, name, link)
	}
	return nil
}

func parseDef(def *sourcegraph.Def) (name, link string) {
	name = def.Path

	// TODO: use the react-router urlPattern for defs route instead of hard coding it.
	link = "https://sourcegraph.com/" + def.Repo + "/-/def/" + def.UnitType + "/" + def.Unit + "/-/" + def.Path
	return
}
