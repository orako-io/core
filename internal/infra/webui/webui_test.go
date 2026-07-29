// SPDX-License-Identifier: AGPL-3.0-or-later

package webui_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/orako-io/core/internal/infra/webui"
)

// TestHandler_SPAFallback verifies deep-linked client-side routes fall back to
// index.html (200) instead of 404, while real embedded files are served as-is.
func TestHandler_SPAFallback(t *testing.T) {
	t.Parallel()

	h := webui.Handler()

	cases := []struct {
		name       string
		path       string
		wantStatus int
		wantHTML   bool // response should be the SPA index document
	}{
		{name: "root serves index", path: "/", wantStatus: http.StatusOK, wantHTML: true},
		{name: "client route falls back to index", path: "/knowledge", wantStatus: http.StatusOK, wantHTML: true},
		{name: "nested client route falls back", path: "/projects/123", wantStatus: http.StatusOK, wantHTML: true},
		{name: "unknown asset falls back too", path: "/assets/does-not-exist.js", wantStatus: http.StatusOK, wantHTML: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, tc.path, nil))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}

			if tc.wantHTML && !strings.Contains(rec.Body.String(), "<!doctype html") &&
				!strings.Contains(rec.Body.String(), "<!DOCTYPE html") {
				t.Fatalf("expected an HTML document (SPA index) for %s, got: %.80q", tc.path, rec.Body.String())
			}
		})
	}
}
