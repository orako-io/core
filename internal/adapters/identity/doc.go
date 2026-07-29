// SPDX-License-Identifier: AGPL-3.0-or-later

// Package identity is the Postgres adapter for the identity aggregates that the
// rest of the schema hangs off: accounts, members, organizations, projects, and
// their memberships. It owns its own sqlc-generated query code (see sqlc.yaml,
// the identity block).
package identity
