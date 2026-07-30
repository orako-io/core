// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"encoding/json"
	"net/http"
)

// ServeASMetadata serves RFC 8414 OAuth 2.0 Authorization Server Metadata at
// /.well-known/oauth-authorization-server. code_challenge_methods_supported
// advertises S256 only (plain is never accepted) and grant_types_supported
// includes refresh_token — locked by the phase-1 spike: real Claude Code's DCR
// requests refresh_token even though nothing here required it, so it is
// supported rather than silently ignored. RFC 9207 issuer identification is
// advertised because authorization responses include the iss parameter.
func (s *Server) ServeASMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         s.baseURL,
		"authorization_endpoint":                         s.baseURL + "/authorize",
		"token_endpoint":                                 s.baseURL + "/token",
		"registration_endpoint":                          s.baseURL + "/register",
		"response_types_supported":                       []string{ResponseTypeCode},
		"grant_types_supported":                          []string{GrantTypeAuthorizationCode, GrantTypeRefreshToken},
		"code_challenge_methods_supported":               []string{PKCEMethodS256},
		"token_endpoint_auth_methods_supported":          []string{TokenAuthMethodNone},
		"scopes_supported":                               []string{"mcp"},
		"authorization_response_iss_parameter_supported": true,
	})
}

// ServePRM serves RFC 9728 OAuth 2.0 Protected Resource Metadata at
// both the root compatibility path and the RFC 9728 path-scoped location for
// the hosted MCP resource ({base}/mcp). The MCP endpoint's 401 carries a
// WWW-Authenticate resource_metadata pointer to the path-scoped document.
func (s *Server) ServePRM(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.ResourceURL(),
		"authorization_servers":    []string{s.baseURL},
		"bearer_methods_supported": []string{"header"},
	})
}

// PRMURL is the absolute URL of the protected-resource metadata document, the
// value every 401 response's resource_metadata challenge parameter points at.
func (s *Server) PRMURL() string {
	return s.baseURL + "/.well-known/oauth-protected-resource/mcp"
}

// writeJSON encodes v as the JSON response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeOAuthError writes an RFC 6749 §5.2 error response body.
func writeOAuthError(w http.ResponseWriter, status int, errCode, description string) {
	writeJSON(w, status, map[string]string{"error": errCode, "error_description": description})
}
