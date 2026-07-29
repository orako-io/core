// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"

	"connectrpc.com/connect"

	"github.com/orako-io/core/gen/orako/v1/orakov1connect"
)

// newTestLogger returns a no-op logger suitable for unit tests.
func newTestLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// startTestServer mounts the OrakoService (with the given handler options) on
// a test HTTP server and returns the server and a generated Connect client.
// Callers must close the returned server.
func startTestServer(svc *Server, opts ...connect.HandlerOption) (*httptest.Server, orakov1connect.OrakoServiceClient) {
	mux := http.NewServeMux()

	path, handler := svc.Handler(opts...)
	mux.Handle(path, handler)

	srv := httptest.NewServer(mux)
	client := orakov1connect.NewOrakoServiceClient(srv.Client(), srv.URL)

	return srv, client
}

// startTestServerWithAuth mounts the OrakoService wired with the auth
// interceptor. Clients must set an Authorization header on every request.
func startTestServerWithAuth(svc *Server) (*httptest.Server, orakov1connect.OrakoServiceClient) {
	return startTestServer(svc, connect.WithInterceptors(NewAuthInterceptor(DevAuthenticator{}, nil)))
}

// devToken returns the raw token string (without "Bearer ") for the given
// identifiers and role.
func devToken(memberID, projectID, role string) string {
	return "Bearer " + memberID + ":" + projectID + ":" + role
}

// setAuth attaches a dev-stub Authorization header to req (which must be a
// *connect.Request[T]). Connect request headers are set before the call.
// Usage: setAuth(req, "dev") before client.SomeRPC(ctx, req).
func setAuth(h interface{ Header() http.Header }, role string) {
	h.Header().Set("Authorization", devToken(testMemberID.String(), testProjectID.String(), role))
}

// setAuthCustom is like setAuth but accepts explicit member/project IDs.
func setAuthCustom(h interface{ Header() http.Header }, memberID, projectID, role string) {
	h.Header().Set("Authorization", devToken(memberID, projectID, role))
}

// setAuthOrgAdmin attaches a dev-stub token whose 4th segment marks the caller as
// an org admin, so it passes the org-admin gate regardless of the project role.
func setAuthOrgAdmin(h interface{ Header() http.Header }, role string) {
	h.Header().Set("Authorization", devToken(testMemberID.String(), testProjectID.String(), role)+":orgadmin:"+testOrgID.String())
}
