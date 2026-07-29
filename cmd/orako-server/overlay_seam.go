// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "github.com/go-chi/chi/v5"

type overlayRoutes interface {
	RegisterRoutes(chi.Router)
}
