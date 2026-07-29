// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/edition"
	"github.com/orako-io/core/internal/pkg/license"
)

// maxLicenseBody caps the tiny {"key":"…"} payload. A base64 license envelope is
// a few hundred bytes; 16 KiB is generous headroom while closing an oversize-body
// vector on this authenticated endpoint.
const maxLicenseBody = 16 << 10 // 16 KiB

// licenseStore persists the single self-host license key. *licensing.LicenseStore
// satisfies it. Get returns found=false (nil error) when no key is set.
type licenseStore interface {
	Get(ctx context.Context) (key string, setBy uuid.UUID, setAt time.Time, found bool, err error)
	Set(ctx context.Context, key string, setBy uuid.UUID) error
	Clear(ctx context.Context) error
}

// LicenseHandler serves the self-host license surface next to the dashboard:
//
//   - GET  /api/edition — the resolved edition + caps (read live, unauthenticated),
//     extended with the license subject/expiry/set-by so the License page shows a
//     real status. No secrets: the key itself is never echoed.
//   - POST /api/license {key} — an org admin activates (or replaces) a license.
//     The key is verified OFFLINE and only persisted when it resolves to Licensed;
//     an invalid/expired key is rejected 400 WITHOUT being stored.
//   - DELETE /api/license — an org admin clears the license (revert to Community).
//
// Both writes apply at runtime (live.Store) with no restart. Managed deployments
// reject manual key changes because their private overlay owns entitlement state.
type LicenseHandler struct {
	live      *edition.Live
	store     licenseStore
	auth      Authenticator
	pubKeyB64 string
	managed   bool
	log       *slog.Logger
}

// NewLicenseHandler builds the handler.
func NewLicenseHandler(live *edition.Live, store licenseStore, auth Authenticator, pubKeyB64 string, managed bool, log *slog.Logger) *LicenseHandler {
	return &LicenseHandler{
		live:      live,
		store:     store,
		auth:      auth,
		pubKeyB64: pubKeyB64,
		managed:   managed,
		log:       log,
	}
}

// RegisterRoutes mounts the edition read and the two license admin endpoints.
func (h *LicenseHandler) RegisterRoutes(r chi.Router) {
	r.Get("/api/edition", h.getEdition)
	r.Post("/api/license", h.postLicense)
	r.Delete("/api/license", h.deleteLicense)
}

// editionStatusDTO is the GET /api/edition body. A 0 limit means unlimited.
// License metadata is populated only for a Licensed edition.
type editionStatusDTO struct {
	Edition   string    `json:"edition"`
	Limits    limitsDTO `json:"limits"`
	Features  []string  `json:"features"`
	Managed   bool      `json:"managed"`
	Subject   string    `json:"subject,omitempty"`
	ExpiresAt string    `json:"expiresAt,omitempty"`
	SetAt     string    `json:"setAt,omitempty"`
	SetBy     string    `json:"setBy,omitempty"`
}

type limitsDTO struct {
	MaxMembers  int `json:"maxMembers"`
	MaxOrgs     int `json:"maxOrgs"`
	MaxProjects int `json:"maxProjects"`
}

// getEdition serves the CURRENT edition (read live, so a runtime activation or
// expiry is reflected without a restart). Unauthenticated: it exposes only the
// edition label, caps, feature flags, and non-secret license metadata.
func (h *LicenseHandler) getEdition(w http.ResponseWriter, r *http.Request) {
	// The edition label + caps + features + managed are PUBLIC (the SPA reads
	// them before a session exists). The license METADATA (subject/expiry/set-by)
	// is not: include it only for an authenticated caller. Best-effort auth — a
	// failed Authenticate simply omits the metadata, never a 401 on this read.
	_, authErr := h.auth.Authenticate(r.Context(), r.Header.Get("Authorization"))
	h.writeStatus(r.Context(), w, http.StatusOK, authErr == nil)
}

// postLicense activates (or replaces) the self-host license. Org-admin only;
// verify-before-persist; a no-op (409) under SaaS.
func (h *LicenseHandler) postLicense(w http.ResponseWriter, r *http.Request) {
	adminID, ok := h.requireOrgAdmin(w, r)
	if !ok {
		return
	}

	if h.managed {
		writeJoinError(w, http.StatusConflict, "license is managed by billing")

		return
	}

	var body struct {
		Key string `json:"key"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, maxLicenseBody)).Decode(&body); err != nil {
		writeJoinError(w, http.StatusBadRequest, "invalid request body")

		return
	}

	key := strings.TrimSpace(body.Key)
	if key == "" {
		writeJoinError(w, http.StatusBadRequest, "a license key is required")

		return
	}

	// Verify OFFLINE before persisting: only a key that resolves to Licensed is
	// stored. A malformed/expired/wrong-signature key returns Community + error;
	// respond 400 with a friendly message and store NOTHING (no detail leaked).
	ed, err := edition.Resolve(false, key, h.pubKeyB64)
	if err != nil || ed.Kind != edition.Licensed {
		writeJoinError(w, http.StatusBadRequest, "that license key is invalid or expired")

		return
	}

	if err := h.store.Set(r.Context(), key, adminID); err != nil {
		h.log.ErrorContext(r.Context(), "storing license key failed", slog.Any("error", err))
		writeJoinError(w, http.StatusInternalServerError, "could not save the license")

		return
	}

	// Apply instantly: the gates and /api/edition read the live edition.
	h.live.Store(ed)
	h.log.InfoContext(r.Context(), "license activated", slog.String("edition", ed.Kind.String()))

	// The caller is an authenticated org admin here, so echo the full metadata.
	h.writeStatus(r.Context(), w, http.StatusOK, true)
}

// deleteLicense clears the license and reverts to Community at runtime. Org-admin
// only; a no-op (409) under SaaS.
func (h *LicenseHandler) deleteLicense(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireOrgAdmin(w, r); !ok {
		return
	}

	if h.managed {
		writeJoinError(w, http.StatusConflict, "license is managed by billing")

		return
	}

	if err := h.store.Clear(r.Context()); err != nil {
		h.log.ErrorContext(r.Context(), "clearing license key failed", slog.Any("error", err))
		writeJoinError(w, http.StatusInternalServerError, "could not remove the license")

		return
	}

	// Re-resolve to Community offline (no billing, no key) and apply live.
	ed, _ := edition.Resolve(false, "", h.pubKeyB64)
	h.live.Store(ed)
	h.log.InfoContext(r.Context(), "license cleared; reverted to community")

	// The caller is an authenticated org admin here, so echo the full metadata.
	h.writeStatus(r.Context(), w, http.StatusOK, true)
}

// requireOrgAdmin authenticates the caller and enforces org-admin authority,
// writing the 401/403 itself and returning ok=false when the caller is rejected.
// On success it returns the caller's member id (recorded as set_by). Admin
// authority comes from the resolved CallerIdentity (org_members), never the
// request — the same signal the Connect interceptor gates admin RPCs on.
func (h *LicenseHandler) requireOrgAdmin(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	caller, err := h.auth.Authenticate(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		writeJoinError(w, http.StatusUnauthorized, "could not verify your session")

		return uuid.Nil, false
	}

	if !caller.IsOrgAdmin {
		writeJoinError(w, http.StatusForbidden, "org admin required")

		return uuid.Nil, false
	}

	return caller.MemberID, true
}

// writeStatus builds the edition status DTO from the LIVE edition and writes it
// as JSON. includeMeta gates the license metadata (subject/expiry/set-by): it is
// populated only for an authenticated caller and a Licensed edition — the
// public read (unauthenticated) never carries it.
func (h *LicenseHandler) writeStatus(ctx context.Context, w http.ResponseWriter, status int, includeMeta bool) {
	ed := h.live.Current()

	features := ed.Features
	if features == nil {
		features = []string{}
	}

	dto := editionStatusDTO{
		Edition: ed.Kind.String(),
		Limits: limitsDTO{
			MaxMembers:  ed.Limits.MaxMembers,
			MaxOrgs:     ed.Limits.MaxOrgs,
			MaxProjects: ed.Limits.MaxProjects,
		},
		Features:  features,
		Managed:   h.managed,
		Subject:   "",
		ExpiresAt: "",
		SetAt:     "",
		SetBy:     "",
	}

	if includeMeta && ed.Kind == edition.Licensed {
		h.fillLicenseMeta(ctx, &dto)
	}

	body, _ := json.Marshal(dto)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// fillLicenseMeta reads the stored key and augments the DTO with the license
// subject/expiry (by re-verifying the key OFFLINE) plus who set it and when. All
// best-effort: a read/verify miss simply leaves the fields empty.
func (h *LicenseHandler) fillLicenseMeta(ctx context.Context, dto *editionStatusDTO) {
	key, setBy, setAt, found, err := h.store.Get(ctx)
	if err != nil || !found {
		return
	}

	if !setAt.IsZero() {
		dto.SetAt = setAt.UTC().Format(time.RFC3339)
	}

	if setBy != uuid.Nil {
		dto.SetBy = setBy.String()
	}

	subject, expiresAt, ok := verifyLicenseMeta(h.pubKeyB64, key)
	if !ok {
		return
	}

	dto.Subject = subject

	if !expiresAt.IsZero() {
		dto.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	}
}

// verifyLicenseMeta decodes the base64 public key + envelope and verifies the
// license OFFLINE, returning its subject and expiry. ok is false on any
// decode/verify failure (the caller then omits the metadata). No network call.
func verifyLicenseMeta(pubKeyB64, keyB64 string) (subject string, expiresAt time.Time, ok bool) {
	pub, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return "", time.Time{}, false
	}

	envelope, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return "", time.Time{}, false
	}

	lic, err := license.Verify(envelope, ed25519.PublicKey(pub))
	if err != nil {
		return "", time.Time{}, false
	}

	return lic.Subject, lic.ExpiresAt, true
}
