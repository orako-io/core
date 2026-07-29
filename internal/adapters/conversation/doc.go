// SPDX-License-Identifier: AGPL-3.0-or-later

// Package conversation is the Postgres adapter for the conversation aggregate:
// conversations and their messages, the candidate pool, cross-surface anchors
// (Slack/Discord threads), attachments, and the escalation sweep state. It owns
// its own sqlc-generated query code (see sqlc.yaml, the conversation block).
package conversation
