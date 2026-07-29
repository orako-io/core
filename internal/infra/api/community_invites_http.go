// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// communityInvitesAuth resolves the request bearer to a caller (member) so the
// org invite lookup runs against the caller's own org. api.Authenticator
// satisfies it. A pending member has a full CallerIdentity after redeem, so this
// succeeds for the freshly-joined member the onboarding wizard serves.
type communityInvitesAuth interface {
	Authenticate(ctx context.Context, authorizationHeader string) (CallerIdentity, error)
}

// communityInvitesLoader loads an org's stored provider credentials by the
// caller's project (it resolves project→org internally).
// *integration.OrgResolvingProviderLoader satisfies it.
type communityInvitesLoader interface {
	LoadProvider(ctx context.Context, projectID uuid.UUID, kind string) ([]byte, error)
}

// CommunityInvitesHandler serves GET /org/community-invites: the caller's org
// community server invite URLs (Discord/Slack), for the onboarding "Join your
// team's server" step. It returns ONLY the admin-set invite URLs — never a
// credential — and only for the caller's own org (resolved from the token).
type CommunityInvitesHandler struct {
	auth   communityInvitesAuth
	loader communityInvitesLoader
	log    *slog.Logger
}

// NewCommunityInvitesHandler builds the handler over the member authenticator
// and the org provider-credential loader.
func NewCommunityInvitesHandler(auth communityInvitesAuth, loader communityInvitesLoader, log *slog.Logger) *CommunityInvitesHandler {
	return &CommunityInvitesHandler{auth: auth, loader: loader, log: log}
}

// RegisterRoutes mounts GET /org/community-invites.
func (h *CommunityInvitesHandler) RegisterRoutes(r chi.Router) {
	r.Get("/org/community-invites", h.get)
}

// communityInvitesResponse carries the org's configured community invite URLs.
// Fields are omitted when unset, so the client gets {} when nothing is set.
type communityInvitesResponse struct {
	Discord string `json:"discord,omitempty"`
	Slack   string `json:"slack,omitempty"`
}

// get authenticates the caller and returns their org's community invite URLs.
func (h *CommunityInvitesHandler) get(w http.ResponseWriter, r *http.Request) {
	caller, err := h.auth.Authenticate(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		writeJoinError(w, http.StatusUnauthorized, "could not verify your session")

		return
	}

	resp := communityInvitesResponse{
		Discord: h.inviteURL(r.Context(), caller.ProjectID, kindDiscord),
		Slack:   h.inviteURL(r.Context(), caller.ProjectID, kindSlack),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// kind strings for the two providers that carry a community invite URL. Kept
// local so this file needs no cross-package coupling.
const (
	kindDiscord = "discord"
	kindSlack   = "slack"
)

// inviteURL best-effort reads the community_invite_url from the org's stored
// credentials for a kind. A miss (no provider, no URL, decode error) yields ""
// — the field is simply omitted from the response.
func (h *CommunityInvitesHandler) inviteURL(ctx context.Context, projectID uuid.UUID, kind string) string {
	raw, err := h.loader.LoadProvider(ctx, projectID, kind)
	if err != nil || len(raw) == 0 {
		return ""
	}

	var creds struct {
		CommunityInviteURL string `json:"community_invite_url"`
	}

	if err := json.Unmarshal(raw, &creds); err != nil {
		return ""
	}

	return creds.CommunityInviteURL
}
