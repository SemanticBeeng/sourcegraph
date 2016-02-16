package ui

import (
	"encoding/json"
	"errors"
	"net/http"

	"src.sourcegraph.com/sourcegraph/auth"
	"src.sourcegraph.com/sourcegraph/auth/authutil"
	"src.sourcegraph.com/sourcegraph/go-sourcegraph/sourcegraph"
	"src.sourcegraph.com/sourcegraph/util/handlerutil"
	"src.sourcegraph.com/sourcegraph/util/httputil/httpctx"
)

func serveUserInvite(w http.ResponseWriter, r *http.Request) error {
	ctx := httpctx.FromRequest(r)
	cl := handlerutil.APIClient(r)

	ctxActor := auth.ActorFromContext(ctx)
	if !ctxActor.HasAdminAccess() {
		// current user is not an admin of the instance
		return errors.New("user not authenticated to complete this request")
	}
	if authutil.ActiveFlags.PrivateMirrors {
		return errors.New("this endpoint is disabled on the server. use invite-bulk instead.")
	}

	query := struct {
		Email      string
		Permission string
	}{}
	if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
		return err
	}
	defer r.Body.Close()

	if query.Email == "" {
		return errors.New("no email specified")
	}

	var write, admin bool
	switch query.Permission {
	case "write":
		write = true
	case "admin":
		write = true
		admin = true
	case "read":
		// no-op
	default:
		return errors.New("unknown permission type")
	}

	pendingInvite, err := cl.Accounts.Invite(ctx, &sourcegraph.AccountInvite{
		Email: query.Email,
		Write: write,
		Admin: admin,
	})
	if err != nil {
		return err
	}

	return json.NewEncoder(w).Encode(pendingInvite)
}

type inviteResult struct {
	Email      string
	InviteLink string
	EmailSent  bool
	Err        error
}

func serveUserInviteBulk(w http.ResponseWriter, r *http.Request) error {
	ctx := httpctx.FromRequest(r)
	cl := handlerutil.APIClient(r)
	currentUser := handlerutil.UserFromRequest(r)
	if currentUser == nil {
		return errors.New("user not authenticated to complete this request")
	}
	if !authutil.ActiveFlags.PrivateMirrors {
		return errors.New("this endpoint is disabled on the server. use invite-bulk instead.")
	}

	query := struct {
		Emails []string
	}{}
	if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
		return err
	}
	defer r.Body.Close()

	if len(query.Emails) == 0 {
		return errors.New("no emails specified")
	}

	inviteResults := make([]*inviteResult, len(query.Emails))
	for i, email := range query.Emails {
		inviteResults[i] = &inviteResult{Email: email}
		pendingInvite, err := cl.Accounts.Invite(ctx, &sourcegraph.AccountInvite{Email: email})
		if err != nil {
			inviteResults[i].Err = err
		} else {
			inviteResults[i].EmailSent = pendingInvite.EmailSent
			inviteResults[i].InviteLink = pendingInvite.Link
		}
	}

	teammates, err := cl.Users.ListTeammates(ctx, currentUser)
	if err != nil {
		return err
	}

	return json.NewEncoder(w).Encode(teammates)
}
