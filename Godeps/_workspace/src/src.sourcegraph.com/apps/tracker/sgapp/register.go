package sgapp

import (
	"net/http"

	"golang.org/x/net/context"

	issuesapp "src.sourcegraph.com/apps/tracker"
	"src.sourcegraph.com/apps/tracker/common"
	"src.sourcegraph.com/apps/tracker/issues"
	"src.sourcegraph.com/sourcegraph/platform"
	"src.sourcegraph.com/sourcegraph/platform/pctx"
	"src.sourcegraph.com/sourcegraph/platform/putil"

	fsissues "src.sourcegraph.com/apps/tracker/issues/fs"
)

func init() {
	service := fsissues.NewService("tracker")

	opt := issuesapp.Options{
		Context: func(req *http.Request) context.Context {
			return putil.Context(req)
		},
		RepoSpec: func(req *http.Request) issues.RepoSpec {
			ctx := putil.Context(req)
			repoRevSpec, _ := pctx.RepoRevSpec(ctx)
			return issues.RepoSpec{URI: repoRevSpec.URI}
		},
		BaseURI: func(req *http.Request) string {
			ctx := putil.Context(req)
			return pctx.BaseURI(ctx)
		},
		CSRFToken: func(req *http.Request) string {
			ctx := putil.Context(req)
			return pctx.CSRFToken(ctx)
		},
		Verbatim: func(w http.ResponseWriter) {
			w.Header().Set("X-Sourcegraph-Verbatim", "true")
		},
		BaseState: func(req *http.Request) issuesapp.BaseState {
			ctx := putil.Context(req)
			reqPath := req.URL.Path
			if reqPath == "/" {
				reqPath = ""
			}
			return issuesapp.BaseState{
				State: common.State{
					BaseURI:   pctx.BaseURI(ctx),
					ReqPath:   reqPath,
					CSRFToken: pctx.CSRFToken(ctx),
				},
			}
		},
		HeadPre: `<style type="text/css">
	#main {
		margin: 0 auto;
		line-height: initial;
	}
</style>`,
	}
	handler := issuesapp.New(service, opt)

	platform.RegisterFrame(platform.RepoFrame{
		ID:      "tracker",
		Title:   "Tracker",
		Icon:    "issue-opened",
		Handler: handler,
	})
}
