// SPDX-License-Identifier: AGPL-3.0-or-later

// Package integration is the Postgres adapter for messaging-integration
// persistence: per-org and per-project provider connections (Slack/Discord/Teams
// credentials, encrypted at rest) and the delivered-message ledger. Distinct from
// adapters/provider, which is the live delivery registry. It owns its own
// sqlc-generated query code (see sqlc.yaml, the integration block).
package integration
