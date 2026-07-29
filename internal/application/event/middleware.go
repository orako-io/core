// SPDX-License-Identifier: AGPL-3.0-or-later

// Package event wires the watermill router and middleware that deliver Orako
// events to their in-process consumers.
package event

import (
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
)

// defaultMiddleware is the middleware stack applied to every router handler, in
// order. Recoverer turns a handler panic into an error (so one bad message
// cannot crash the process), and CorrelationID propagates a correlation id
// across the message's lifetime for traceability.
func defaultMiddleware() []message.HandlerMiddleware {
	return []message.HandlerMiddleware{
		middleware.Recoverer,
		middleware.CorrelationID,
	}
}
