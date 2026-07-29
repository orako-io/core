// SPDX-License-Identifier: AGPL-3.0-or-later

// Package webui embeds the Orako web dashboard as a static file tree.
//
// The real Vite build output (a populated dist/ directory) slots in without
// any code change: copy the built assets into internal/infra/webui/dist/ and
// rebuild the binary. This placeholder keeps the //go:embed valid and lets the
// server mount the handler at startup until the dashboard ships.
//
// SPA routing: all unknown paths fall back to index.html so client-side routes
// survive a full page reload.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var ui embed.FS

// Handler returns an http.Handler that serves the embedded frontend.
//
// Routes:
//
//	GET /            → index.html (SPA entry point)
//	GET /assets/...  → embedded static asset
//	GET /<anything>  → index.html (SPA fallback) so client-side routes such as
//	                   /knowledge survive a hard load or reload.
func Handler() http.Handler {
	sub, err := fs.Sub(ui, "dist")
	if err != nil {
		// dist is always present because it is embedded at compile time.
		// This path is unreachable in practice.
		panic("webui: dist subdirectory not embedded: " + err.Error())
	}

	fileServer := http.FileServerFS(sub)

	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic("webui: index.html not embedded: " + err.Error())
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}

		// Serve a real embedded file when one exists (index.html, assets…);
		// otherwise fall back to index.html so client-side (history API) routes
		// resolve on a direct navigation or reload instead of 404-ing.
		if info, statErr := fs.Stat(sub, name); statErr == nil && !info.IsDir() {
			// Vite emits content-hashed asset filenames (…-<hash>.js), so those
			// are safe to cache forever. index.html carries no hash and points at
			// the current asset hashes, so it MUST revalidate — otherwise a
			// browser keeps a stale shell after a deploy and loads the old bundle
			// (the self-host "why is my update not showing" trap).
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}

			fileServer.ServeHTTP(w, r)

			return
		}

		// SPA fallback → index.html; never cache so a new deploy is picked up.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	})
}
