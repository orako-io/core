// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/pkg/edition"
	"github.com/orako-io/core/internal/pkg/license"
)

// --- fakes ---

// fakeLicenseStore is an in-memory licenseStore that records writes so a test can
// assert verify-before-persist (a rejected key must never reach Set).
type fakeLicenseStore struct {
	key     string
	setBy   uuid.UUID
	setAt   time.Time
	present bool

	getErr, setErr, clearErr error

	setCalls   int
	clearCalls int
	lastSetKey string
	lastSetBy  uuid.UUID
}

func (f *fakeLicenseStore) Get(context.Context) (string, uuid.UUID, time.Time, bool, error) {
	if f.getErr != nil {
		return "", uuid.Nil, time.Time{}, false, f.getErr
	}

	return f.key, f.setBy, f.setAt, f.present, nil
}

func (f *fakeLicenseStore) Set(_ context.Context, key string, setBy uuid.UUID) error {
	f.setCalls++
	f.lastSetKey = key
	f.lastSetBy = setBy

	if f.setErr != nil {
		return f.setErr
	}

	f.key, f.setBy, f.setAt, f.present = key, setBy, time.Now().UTC(), true

	return nil
}

func (f *fakeLicenseStore) Clear(context.Context) error {
	f.clearCalls++

	if f.clearErr != nil {
		return f.clearErr
	}

	f.key, f.present = "", false

	return nil
}

// fakeAuthenticator resolves a fixed identity (or error), standing in for the
// dashboard authenticator so a handler test can exercise the admin gate.
type fakeAuthenticator struct {
	identity CallerIdentity
	err      error
}

func (f fakeAuthenticator) Authenticate(context.Context, string) (CallerIdentity, error) {
	if f.err != nil {
		return CallerIdentity{}, f.err
	}

	return f.identity, nil
}

func (f fakeAuthenticator) AuthenticateAccount(context.Context, string) (CallerIdentity, error) {
	if f.err != nil {
		return CallerIdentity{}, f.err
	}

	return f.identity, nil
}

// --- helpers ---

// signTestLicense signs lic and returns the base64 envelope + base64 public key,
// exactly as an admin would paste the key and Config supplies the public key.
func signTestLicense(t *testing.T, lic license.License) (keyB64, pubB64 string) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	payload, err := json.Marshal(lic)
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}

	env := license.Envelope{Payload: payload, Signature: ed25519.Sign(priv, payload)}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	return base64.StdEncoding.EncodeToString(data), base64.StdEncoding.EncodeToString(pub)
}

func newLicenseTestHandler(live *edition.Live, store licenseStore, auth Authenticator, pubB64 string, managed bool) http.Handler {
	h := NewLicenseHandler(live, store, auth, pubB64, managed, slog.New(slog.DiscardHandler))
	r := chi.NewRouter()
	h.RegisterRoutes(r)

	return r
}

func communityLive() *edition.Live {
	return edition.NewLive(edition.Edition{Kind: edition.Community, Limits: edition.CommunityLimits, Features: nil})
}

func adminAuth() fakeAuthenticator {
	return fakeAuthenticator{identity: CallerIdentity{MemberID: uuid.New(), IsOrgAdmin: true}, err: nil}
}

func doReq(t *testing.T, handler http.Handler, method, path, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}

	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// --- GET /api/edition ---

func TestEdition_CommunityStatus(t *testing.T) {
	t.Parallel()

	handler := newLicenseTestHandler(communityLive(), &fakeLicenseStore{}, adminAuth(), "", false)

	rec := doReq(t, handler, http.MethodGet, "/api/edition", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body editionStatusDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Edition != "community" {
		t.Errorf("edition = %q, want community", body.Edition)
	}

	if body.Limits.MaxMembers != 5 || body.Limits.MaxOrgs != 1 || body.Limits.MaxProjects != 1 {
		t.Errorf("limits = %+v, want 5/1/1", body.Limits)
	}

	if body.Subject != "" || body.ExpiresAt != "" {
		t.Errorf("community status leaked license metadata: %+v", body)
	}
}

func TestEdition_LicensedStatusIncludesMeta(t *testing.T) {
	t.Parallel()

	expires := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	keyB64, pubB64 := signTestLicense(t, license.License{
		Subject: "acme", Seats: 25, MaxOrgs: 5, MaxProjects: 50,
		IssuedAt: time.Now().UTC().Add(-time.Hour), ExpiresAt: expires,
	})

	licensed, err := edition.Resolve(false, keyB64, pubB64)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	setBy := uuid.New()
	setAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	store := &fakeLicenseStore{key: keyB64, setBy: setBy, setAt: setAt, present: true}

	handler := newLicenseTestHandler(edition.NewLive(licensed), store, adminAuth(), pubB64, false)

	// Authenticated read (bearer present) → the license metadata is included.
	rec := doReq(t, handler, http.MethodGet, "/api/edition", "tok", "")

	var body editionStatusDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Edition != "licensed" {
		t.Fatalf("edition = %q, want licensed", body.Edition)
	}

	if body.Subject != "acme" {
		t.Errorf("subject = %q, want acme", body.Subject)
	}

	if body.ExpiresAt != expires.Format(time.RFC3339) {
		t.Errorf("expiresAt = %q, want %q", body.ExpiresAt, expires.Format(time.RFC3339))
	}

	if body.SetBy != setBy.String() {
		t.Errorf("setBy = %q, want %q", body.SetBy, setBy.String())
	}

	if body.SetAt != setAt.Format(time.RFC3339) {
		t.Errorf("setAt = %q, want %q", body.SetAt, setAt.Format(time.RFC3339))
	}
}

// TestEdition_UnauthenticatedHidesMeta proves the PUBLIC edition read exposes
// the label + caps but never the license metadata (subject/expiry/set-by) when
// the caller is unauthenticated — the metadata is gated behind a valid session.
func TestEdition_UnauthenticatedHidesMeta(t *testing.T) {
	t.Parallel()

	expires := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	keyB64, pubB64 := signTestLicense(t, license.License{
		Subject: "acme", Seats: 25, MaxOrgs: 5, MaxProjects: 50,
		IssuedAt: time.Now().UTC().Add(-time.Hour), ExpiresAt: expires,
	})

	licensed, err := edition.Resolve(false, keyB64, pubB64)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	store := &fakeLicenseStore{key: keyB64, setBy: uuid.New(), setAt: time.Now().UTC(), present: true}
	// An authenticator that rejects a missing/invalid bearer, like the real one.
	unauth := fakeAuthenticator{identity: CallerIdentity{}, err: errors.New("no session")}
	handler := newLicenseTestHandler(edition.NewLive(licensed), store, unauth, pubB64, false)

	rec := doReq(t, handler, http.MethodGet, "/api/edition", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (public read must not 401)", rec.Code)
	}

	var body editionStatusDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Public fields stay visible.
	if body.Edition != "licensed" {
		t.Errorf("edition = %q, want licensed", body.Edition)
	}

	// Metadata must be withheld.
	if body.Subject != "" || body.ExpiresAt != "" || body.SetAt != "" || body.SetBy != "" {
		t.Errorf("unauthenticated read leaked license metadata: %+v", body)
	}
}

// --- POST /api/license: verify-before-persist ---

func TestSetLicense_ValidKeyPersistsAndAppliesLive(t *testing.T) {
	t.Parallel()

	keyB64, pubB64 := signTestLicense(t, license.License{
		Subject: "acme", Seats: 25, MaxOrgs: 5, MaxProjects: 50,
		IssuedAt: time.Now().UTC().Add(-time.Hour), ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	})

	store := &fakeLicenseStore{}
	auth := adminAuth()
	live := communityLive()
	handler := newLicenseTestHandler(live, store, auth, pubB64, false)

	rec := doReq(t, handler, http.MethodPost, "/api/license", "tok", `{"key":"`+keyB64+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	if store.setCalls != 1 || store.lastSetKey != keyB64 {
		t.Errorf("Set calls = %d lastKey set? %v; want the key persisted once", store.setCalls, store.lastSetKey == keyB64)
	}

	if store.lastSetBy != auth.identity.MemberID {
		t.Errorf("set_by = %s, want the caller member id %s", store.lastSetBy, auth.identity.MemberID)
	}

	// Applied live without a restart.
	if got := live.Current().Kind; got != edition.Licensed {
		t.Errorf("live edition = %s, want licensed after activation", got)
	}
}

func TestSetLicense_InvalidKeyRejectedNotPersisted(t *testing.T) {
	t.Parallel()

	// Sign with one keypair but advertise a DIFFERENT public key → unverifiable.
	keyB64, _ := signTestLicense(t, license.License{Seats: 999})
	_, otherPub := signTestLicense(t, license.License{Seats: 1})

	store := &fakeLicenseStore{}
	live := communityLive()
	handler := newLicenseTestHandler(live, store, adminAuth(), otherPub, false)

	rec := doReq(t, handler, http.MethodPost, "/api/license", "tok", `{"key":"`+keyB64+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an invalid key", rec.Code)
	}

	if store.setCalls != 0 {
		t.Errorf("Set was called %d times; an invalid key must NOT be persisted", store.setCalls)
	}

	if live.Current().Kind != edition.Community {
		t.Error("live edition changed on an invalid key; must stay community")
	}

	if strings.Contains(strings.ToLower(rec.Body.String()), "signature") {
		t.Errorf("400 body leaked internal detail: %q", rec.Body.String())
	}
}

func TestSetLicense_ExpiredKeyRejectedNotPersisted(t *testing.T) {
	t.Parallel()

	keyB64, pubB64 := signTestLicense(t, license.License{
		Subject: "acme", Seats: 25,
		IssuedAt: time.Now().UTC().Add(-48 * time.Hour), ExpiresAt: time.Now().UTC().Add(-time.Hour),
	})

	store := &fakeLicenseStore{}
	handler := newLicenseTestHandler(communityLive(), store, adminAuth(), pubB64, false)

	rec := doReq(t, handler, http.MethodPost, "/api/license", "tok", `{"key":"`+keyB64+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an expired key", rec.Code)
	}

	if store.setCalls != 0 {
		t.Errorf("Set was called %d times; an expired key must NOT be persisted", store.setCalls)
	}
}

func TestSetLicense_NonAdminForbidden(t *testing.T) {
	t.Parallel()

	keyB64, pubB64 := signTestLicense(t, license.License{Seats: 25, ExpiresAt: time.Now().UTC().Add(24 * time.Hour)})

	store := &fakeLicenseStore{}
	auth := fakeAuthenticator{identity: CallerIdentity{MemberID: uuid.New(), IsOrgAdmin: false}, err: nil}
	handler := newLicenseTestHandler(communityLive(), store, auth, pubB64, false)

	rec := doReq(t, handler, http.MethodPost, "/api/license", "tok", `{"key":"`+keyB64+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-admin", rec.Code)
	}

	if store.setCalls != 0 {
		t.Error("a non-admin must not reach Set")
	}
}

func TestSetLicense_Unauthenticated401(t *testing.T) {
	t.Parallel()

	store := &fakeLicenseStore{}
	auth := fakeAuthenticator{identity: CallerIdentity{}, err: errors.New("no session")}
	handler := newLicenseTestHandler(communityLive(), store, auth, "", false)

	rec := doReq(t, handler, http.MethodPost, "/api/license", "", `{"key":"x"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	if store.setCalls != 0 {
		t.Error("an unauthenticated caller must not reach Set")
	}
}

// --- Managed deployments reject manual license writes. ---

func TestSetLicense_SaaSRejected409_NotPersisted(t *testing.T) {
	t.Parallel()

	keyB64, pubB64 := signTestLicense(t, license.License{Seats: 25, ExpiresAt: time.Now().UTC().Add(24 * time.Hour)})

	store := &fakeLicenseStore{}
	// The private overlay governs this edition.
	live := edition.NewLive(edition.Edition{Kind: edition.SaaS, Limits: edition.Limits{}, Features: nil})
	handler := newLicenseTestHandler(live, store, adminAuth(), pubB64, true)

	rec := doReq(t, handler, http.MethodPost, "/api/license", "tok", `{"key":"`+keyB64+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 under SaaS", rec.Code)
	}

	if store.setCalls != 0 {
		t.Error("a DB license must be ignored under SaaS; Set must not be called")
	}

	if live.Current().Kind != edition.SaaS {
		t.Error("edition changed under SaaS; must stay saas")
	}
}

func TestClearLicense_SaaSRejected409(t *testing.T) {
	t.Parallel()

	store := &fakeLicenseStore{present: true, key: "whatever"}
	live := edition.NewLive(edition.Edition{Kind: edition.SaaS, Limits: edition.Limits{}, Features: nil})
	handler := newLicenseTestHandler(live, store, adminAuth(), "", true)

	rec := doReq(t, handler, http.MethodDelete, "/api/license", "tok", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 under SaaS", rec.Code)
	}

	if store.clearCalls != 0 {
		t.Error("Clear must not be called under SaaS")
	}
}

// --- DELETE /api/license ---

func TestClearLicense_RevertsToCommunityLive(t *testing.T) {
	t.Parallel()

	keyB64, pubB64 := signTestLicense(t, license.License{
		Subject: "acme", Seats: 25, ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	})

	licensed, err := edition.Resolve(false, keyB64, pubB64)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	store := &fakeLicenseStore{key: keyB64, present: true}
	live := edition.NewLive(licensed)
	handler := newLicenseTestHandler(live, store, adminAuth(), pubB64, false)

	rec := doReq(t, handler, http.MethodDelete, "/api/license", "tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	if store.clearCalls != 1 {
		t.Errorf("Clear calls = %d, want 1", store.clearCalls)
	}

	if got := live.Current().Kind; got != edition.Community {
		t.Errorf("live edition = %s, want community after clear", got)
	}

	var body editionStatusDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	if body.Edition != "community" {
		t.Errorf("response edition = %q, want community", body.Edition)
	}
}

func TestClearLicense_NonAdminForbidden(t *testing.T) {
	t.Parallel()

	store := &fakeLicenseStore{present: true}
	auth := fakeAuthenticator{identity: CallerIdentity{MemberID: uuid.New(), IsOrgAdmin: false}, err: nil}
	handler := newLicenseTestHandler(communityLive(), store, auth, "", false)

	rec := doReq(t, handler, http.MethodDelete, "/api/license", "tok", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}

	if store.clearCalls != 0 {
		t.Error("a non-admin must not reach Clear")
	}
}
