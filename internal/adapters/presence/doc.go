// SPDX-License-Identifier: AGPL-3.0-or-later

// Package presence is the Postgres adapter for connection-presence persistence:
// the most recent online/offline state per member. It owns its own sqlc-generated
// query code (see sqlc.yaml, the presence block).
package presence
