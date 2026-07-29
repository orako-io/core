// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
)

// NewRouter builds a watermill router with Orako's default middleware applied.
//
// The router has no handlers yet; callers register them with AddConsumerHandler
// before calling Run. Run blocks until its context is cancelled or Close is
// called, and returns nil on a clean shutdown.
func NewRouter(logger *slog.Logger) (*message.Router, error) {
	router, err := message.NewRouter(message.RouterConfig{}, watermill.NewSlogLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("creating event router: %w", err)
	}

	router.AddMiddleware(defaultMiddleware()...)

	return router, nil
}
