// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	maxRegistrationBodyBytes = 64 << 10
	maxClientNameBytes       = 200
	maxRedirectURIs          = 10
	maxRedirectURIBytes      = 2048
)

// loopbackHosts are the only hosts allowed with an http (non-TLS) redirect URI:
// a native/CLI client's loopback listener (RFC 8252). Everything else must be
// https so an authorization code is never sent over cleartext.
var loopbackHosts = map[string]struct{}{ //nolint:gochecknoglobals // compile-time set
	"localhost": {},
	"127.0.0.1": {},
	"[::1]":     {},
	"::1":       {},
}

// validateRedirectURI rejects a registered redirect_uri that is not an absolute
// https URL (or an http loopback for native clients) or that carries a
// fragment. Exact-string matching at authorize/token means a malformed or
// cleartext URI would otherwise be honoured verbatim.
func validateRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("redirect_uri %q is not a valid URL", raw)
	}

	if !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("redirect_uri %q must be an absolute URL", raw)
	}

	if u.Fragment != "" || u.RawFragment != "" {
		return fmt.Errorf("redirect_uri %q must not contain a fragment", raw)
	}

	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if _, ok := loopbackHosts[u.Hostname()]; ok {
			return nil
		}

		return fmt.Errorf("redirect_uri %q: http is only allowed for loopback (localhost/127.0.0.1)", raw)
	default:
		return fmt.Errorf("redirect_uri %q must use https", raw)
	}
}

// registerRequest is the subset of RFC 7591 client metadata Orako accepts —
// exactly the fields real Claude Code sends at DCR time (see
// spike-findings.md §3): client_name, redirect_uris, grant_types,
// response_types, token_endpoint_auth_method.
type registerRequest struct {
	ClientName    string   `json:"client_name"`
	RedirectURIs  []string `json:"redirect_uris"`
	GrantTypes    []string `json:"grant_types"`
	ResponseTypes []string `json:"response_types"`
	AuthMethod    string   `json:"token_endpoint_auth_method"`
}

// ServeRegister handles RFC 7591 Dynamic Client Registration. Every
// registered client is public (PKCE, no secret) regardless of the requested
// token_endpoint_auth_method — Orako's AS never issues a client secret, so the
// response always reports "none".
func (s *Server) ServeRegister(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRegistrationBodyBytes)

	var req registerRequest

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "malformed client metadata")

		return
	}

	if err := validateRegistration(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())

		return
	}

	if err := s.store.CleanupExpired(r.Context(), s.now().Add(-24*time.Hour)); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not maintain client registry")

		return
	}

	clientID, err := newClientID()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not generate client_id")

		return
	}

	client := Client{
		ID:            clientID,
		Name:          req.ClientName,
		RedirectURIs:  req.RedirectURIs,
		GrantTypes:    defaultOr(req.GrantTypes, []string{GrantTypeAuthorizationCode}),
		ResponseTypes: defaultOr(req.ResponseTypes, []string{ResponseTypeCode}),
		AuthMethod:    TokenAuthMethodNone,
	}

	if err := s.store.CreateClient(r.Context(), client); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not register client")

		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  client.ID,
		"client_id_issued_at":        s.now().Unix(),
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                client.GrantTypes,
		"response_types":             client.ResponseTypes,
		"token_endpoint_auth_method": client.AuthMethod,
	})
}

func validateRegistration(req *registerRequest) error {
	req.ClientName = strings.TrimSpace(req.ClientName)

	if len(req.ClientName) > maxClientNameBytes {
		return fmt.Errorf("client_name must not exceed %d bytes", maxClientNameBytes)
	}

	if err := validateRegistrationRedirects(req.RedirectURIs); err != nil {
		return err
	}

	if err := validateRegistrationGrantTypes(req); err != nil {
		return err
	}

	if err := validateRegistrationResponseTypes(req); err != nil {
		return err
	}

	if req.AuthMethod != "" && req.AuthMethod != TokenAuthMethodNone {
		return errors.New("token_endpoint_auth_method must be none")
	}

	return nil
}

func validateRegistrationRedirects(redirectURIs []string) error {
	if len(redirectURIs) == 0 || len(redirectURIs) > maxRedirectURIs {
		return fmt.Errorf("redirect_uris must contain between 1 and %d entries", maxRedirectURIs)
	}

	seen := make(map[string]struct{}, len(redirectURIs))

	for _, uri := range redirectURIs {
		if len(uri) > maxRedirectURIBytes {
			return fmt.Errorf("redirect_uri must not exceed %d bytes", maxRedirectURIBytes)
		}

		if _, duplicate := seen[uri]; duplicate {
			return errors.New("redirect_uris must not contain duplicates")
		}

		seen[uri] = struct{}{}

		if err := validateRedirectURI(uri); err != nil {
			return err
		}
	}

	return nil
}

func validateRegistrationGrantTypes(req *registerRequest) error {
	if len(req.GrantTypes) == 0 {
		req.GrantTypes = []string{GrantTypeAuthorizationCode}
	}

	for _, grantType := range req.GrantTypes {
		if grantType != GrantTypeAuthorizationCode && grantType != GrantTypeRefreshToken {
			return fmt.Errorf("unsupported grant_type %q", grantType)
		}
	}

	if !slices.Contains(req.GrantTypes, GrantTypeAuthorizationCode) {
		return errors.New("grant_types must include authorization_code")
	}

	return nil
}

func validateRegistrationResponseTypes(req *registerRequest) error {
	if len(req.ResponseTypes) == 0 {
		req.ResponseTypes = []string{ResponseTypeCode}
	}

	if len(req.ResponseTypes) != 1 || req.ResponseTypes[0] != ResponseTypeCode {
		return errors.New("response_types must contain only code")
	}

	return nil
}
