package ui

import (
	"encoding/json"
	"net/http"

	"github.com/sourcegraph/mux"

	"src.sourcegraph.com/sourcegraph/util/handlerutil"
)

func serveRepoFileFinder(w http.ResponseWriter, r *http.Request) error {
	ctx, _, _, err := handlerutil.RepoClient(r)
	if err != nil {
		return err
	}

	e := json.NewEncoder(w)

	res, err := handlerutil.GetRepoTreeListCommon(ctx, mux.Vars(r))
	if err != nil {
		return err
	}

	// TODO(pararth): perform fuzzy search on the app here instead of
	// returning all files and doing fuzzy search on the client.
	return e.Encode(res.Files)
}
