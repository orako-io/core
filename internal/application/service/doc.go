// SPDX-License-Identifier: AGPL-3.0-or-later

// Package service holds the application-layer service ports — the capabilities
// the use cases (command and query) call out through, and the message-delivery
// domain that surrounds the Provider port. These are NOT aggregate persistence:
// the aggregate repositories live in domain/repository. Concrete implementations
// live under internal/adapters.
package service
