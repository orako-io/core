// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/infra/api/oauth"
)

// --- fakes ---

type fakeAccountAuth struct {
	identity oauth.AccountIdentity
	err      error
	gotFn    func(bearer string)
}

func (f fakeAccountAuth) AuthenticateAccount(_ context.Context, bearer string) (oauth.AccountIdentity, error) {
	if f.gotFn != nil {
		f.gotFn(bearer)
	}

	if f.err != nil {
		return oauth.AccountIdentity{}, f.err
	}

	return f.identity, nil
}

type fakeJoinRedeem struct {
	result command.RedeemJoinTokenResult
	err    error
	got    command.RedeemJoinTokenCommand
}

func (f *fakeJoinRedeem) Handle(_ context.Context, cmd command.RedeemJoinTokenCommand) (command.RedeemJoinTokenResult, error) {
	f.got = cmd
	if f.err != nil {
		return command.RedeemJoinTokenResult{}, f.err
	}

	return f.result, nil
}

type fakeProjectLocator struct {
	project model.Project
	err     error
}

func (f fakeProjectLocator) ByID(_ context.Context, _ uuid.UUID) (model.Project, error) {
	return f.project, f.err
}

type fakeOrgReader struct {
	view query.OrgIdentityView
	err  error
}

func (f fakeOrgReader) Handle(_ context.Context, _ query.GetOrganizationQuery) (query.OrgIdentityView, error) {
	return f.view, f.err
}

type fakeJoinTokenReader struct {
	tok repository.JoinToken
	err error
}

func (f fakeJoinTokenReader) JoinTokenByToken(_ context.Context, _ string) (repository.JoinToken, error) {
	return f.tok, f.err
}

func newJoinTestHandler(auth accountAuthenticator, redeem joinCodeRedeemer, projects joinProjectLocator, orgs joinOrgReader) http.Handler {
	return newJoinTestHandlerWithTokens(auth, redeem, projects, orgs, fakeJoinTokenReader{})
}

func newJoinTestHandlerWithTokens(auth accountAuthenticator, redeem joinCodeRedeemer, projects joinProjectLocator, orgs joinOrgReader, tokens joinTokenReader) http.Handler {
	h := NewJoinHandler(auth, redeem, projects, orgs, tokens, slog.New(slog.DiscardHandler))
	r := chi.NewRouter()
	h.RegisterRoutes(r)

	return r
}

func postJoin(t *testing.T, handler http.Handler, bearer, code string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/join", strings.NewReader(`{"code":"`+code+`"}`))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// --- tests ---

func TestJoin_SuccessRevealsOrgName(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	projID := uuid.New()

	auth := fakeAccountAuth{identity: oauth.AccountIdentity{AccountID: uuid.New(), Email: "new@joiner.io", DisplayName: "New Joiner"}}
	redeem := &fakeJoinRedeem{result: command.RedeemJoinTokenResult{MemberID: uuid.New(), ProjectID: projID}}
	projects := fakeProjectLocator{project: model.Project{ID: projID, OrgID: orgID}}
	orgs := fakeOrgReader{view: query.OrgIdentityView{ID: orgID, Name: "Acme Corp"}}

	rec := postJoin(t, newJoinTestHandler(auth, redeem, projects, orgs), "dummy", "shiny-code")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	var body joinResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.OrgName != "Acme Corp" {
		t.Errorf("orgName = %q, want Acme Corp (revealed on success)", body.OrgName)
	}

	if body.AlreadyMember {
		t.Error("alreadyMember = true, want false for a fresh redeem")
	}

	// The code + account identity must be threaded into the command.
	if redeem.got.Token != "shiny-code" || redeem.got.Email != "new@joiner.io" || redeem.got.DisplayName != "New Joiner" {
		t.Errorf("command = %+v, want token=shiny-code and the account's email/name", redeem.got)
	}
}

func TestJoin_AlreadyMember(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	projID := uuid.New()

	auth := fakeAccountAuth{identity: oauth.AccountIdentity{AccountID: uuid.New()}}
	redeem := &fakeJoinRedeem{result: command.RedeemJoinTokenResult{MemberID: uuid.New(), ProjectID: projID, AlreadyMember: true}}
	projects := fakeProjectLocator{project: model.Project{ID: projID, OrgID: orgID}}
	orgs := fakeOrgReader{view: query.OrgIdentityView{ID: orgID, Name: "Acme Corp"}}

	rec := postJoin(t, newJoinTestHandler(auth, redeem, projects, orgs), "dummy", "shiny-code")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body joinResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	if !body.AlreadyMember || body.OrgName != "Acme Corp" {
		t.Errorf("body = %+v, want alreadyMember=true and the org name", body)
	}
}

func TestJoin_InvalidCode_400_NoOrgLeak(t *testing.T) {
	t.Parallel()

	auth := fakeAccountAuth{identity: oauth.AccountIdentity{AccountID: uuid.New()}}
	redeem := &fakeJoinRedeem{err: command.ErrJoinTokenInvalid}
	// A leaky project/org reader: it must never be consulted on failure.
	projects := fakeProjectLocator{project: model.Project{OrgID: uuid.New()}}
	orgs := fakeOrgReader{view: query.OrgIdentityView{Name: "Secret Org"}}

	rec := postJoin(t, newJoinTestHandler(auth, redeem, projects, orgs), "dummy", "bad")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "Secret Org") {
		t.Errorf("body leaked an org name for an invalid code: %q", rec.Body.String())
	}
}

func TestJoin_RevokedCode_403(t *testing.T) {
	t.Parallel()

	auth := fakeAccountAuth{identity: oauth.AccountIdentity{AccountID: uuid.New()}}
	redeem := &fakeJoinRedeem{err: command.ErrJoinTokenRevoked}

	rec := postJoin(t, newJoinTestHandler(auth, redeem, fakeProjectLocator{}, fakeOrgReader{}), "dummy", "revoked")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestJoin_NoBearer_401(t *testing.T) {
	t.Parallel()

	redeem := &fakeJoinRedeem{}
	rec := postJoin(t, newJoinTestHandler(fakeAccountAuth{}, redeem, fakeProjectLocator{}, fakeOrgReader{}), "", "code")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	if redeem.got.Token != "" {
		t.Error("redeem was called without a verified session")
	}
}

func TestJoin_EmptyCode_400(t *testing.T) {
	t.Parallel()

	auth := fakeAccountAuth{identity: oauth.AccountIdentity{AccountID: uuid.New()}}
	redeem := &fakeJoinRedeem{}

	rec := postJoin(t, newJoinTestHandler(auth, redeem, fakeProjectLocator{}, fakeOrgReader{}), "dummy", "")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJoin_OrgLookupFailure_StillSucceeds(t *testing.T) {
	t.Parallel()

	auth := fakeAccountAuth{identity: oauth.AccountIdentity{AccountID: uuid.New()}}
	redeem := &fakeJoinRedeem{result: command.RedeemJoinTokenResult{ProjectID: uuid.New()}}
	// Project lookup fails — the redeem already succeeded, so 200 with empty name.
	projects := fakeProjectLocator{err: context.DeadlineExceeded}

	rec := postJoin(t, newJoinTestHandler(auth, redeem, projects, fakeOrgReader{}), "dummy", "code")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when org name lookup fails", rec.Code)
	}

	var body joinResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	if body.OrgName != "" {
		t.Errorf("orgName = %q, want empty on lookup failure", body.OrgName)
	}
}

// TestJoinOrgLookup covers GET /join/{code}/org: a valid, non-revoked code
// reveals the org name; a revoked or unknown code returns a detail-free 404 so a
// guesser learns nothing.
func TestJoinOrgLookup(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()

	cases := []struct {
		name       string
		tokens     fakeJoinTokenReader
		orgs       fakeOrgReader
		wantStatus int
		wantName   string
	}{
		{
			name:       "valid code reveals org name",
			tokens:     fakeJoinTokenReader{tok: repository.JoinToken{OrgID: orgID, ProjectID: uuid.New()}},
			orgs:       fakeOrgReader{view: query.OrgIdentityView{ID: orgID, Name: "Acme Corp"}},
			wantStatus: http.StatusOK,
			wantName:   "Acme Corp",
		},
		{
			name:       "revoked code is a 404",
			tokens:     fakeJoinTokenReader{tok: repository.JoinToken{OrgID: orgID, ProjectID: uuid.New(), Revoked: true}},
			orgs:       fakeOrgReader{view: query.OrgIdentityView{ID: orgID, Name: "Acme Corp"}},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown code is a 404",
			tokens:     fakeJoinTokenReader{err: context.DeadlineExceeded},
			orgs:       fakeOrgReader{view: query.OrgIdentityView{Name: "Acme Corp"}},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newJoinTestHandlerWithTokens(fakeAccountAuth{}, &fakeJoinRedeem{}, fakeProjectLocator{}, tc.orgs, tc.tokens)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/join/some-code/org", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}

			if tc.wantStatus == http.StatusOK {
				var body struct {
					OrgName string `json:"orgName"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &body)
				if body.OrgName != tc.wantName {
					t.Errorf("orgName = %q, want %q", body.OrgName, tc.wantName)
				}
			} else if strings.Contains(rec.Body.String(), "Acme Corp") {
				t.Errorf("404 body leaked the org name: %q", rec.Body.String())
			}
		})
	}
}
