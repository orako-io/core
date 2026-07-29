// SPDX-License-Identifier: AGPL-3.0-or-later

// Package query declares the read-side handlers, their read-ports, and DTOs.
// Domain models never leak out of a handler, and handlers never import adapter
// or infrastructure packages directly.
package query

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/repository"
)

// ConversationReader is the read-side port for conversations and their messages.
type ConversationReader interface {
	// ReadConversation returns the core conversation read model. Returns
	// adaptererr.ErrNotFound when absent.
	ReadConversation(ctx context.Context, id uuid.UUID) (ConversationRecord, error)
	// ReadMessages returns all messages in chronological order as views (without
	// attachments, which the handler resolves and signs separately).
	ReadMessages(ctx context.Context, conversationID uuid.UUID) ([]MessageView, error)
}

// ProjectReader is the read-side port for querying project memberships.
type ProjectReader interface {
	// MembersByProject lists memberships, ordered by join time ascending.
	MembersByProject(ctx context.Context, projectID uuid.UUID) ([]repository.ProjectMembership, error)
}

// MemberReader is the read-side port for querying individual members.
type MemberReader interface {
	// ReadMember returns the member as a caller-facing view. Returns
	// adaptererr.ErrNotFound when absent.
	ReadMember(ctx context.Context, id uuid.UUID) (MemberView, error)
	ReadMembers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]MemberView, error)
}

// PresenceReader is the read-side port for a member's online hint.
type PresenceReader interface {
	// ReadOnline reports whether the member is online now (a weak poll-on-demand
	// hint). Returns adaptererr.ErrNotFound when no presence record exists yet;
	// callers treat any error as "offline".
	ReadOnline(ctx context.Context, memberID uuid.UUID) (bool, error)
	ReadOnlineByMembers(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]bool, error)
}

// ProjectsByMemberReader is the read-side port for listing a member's projects.
type ProjectsByMemberReader interface {
	// ProjectsByMember returns all projects where memberID holds any role,
	// ordered by project creation time ascending.
	ProjectsByMember(ctx context.Context, memberID uuid.UUID) ([]repository.ProjectWithRole, error)
}

// ConversationsByProjectReader is the read-side port for listing conversations
// across a multi-project scope.
type ConversationsByProjectReader interface {
	// ConversationsByProjectIDs returns conversations newest first, each paired
	// with its project's resolved name. An empty projectIDs falls back to
	// every non-archived project in orgID (the org-wide read); a non-empty
	// slice restricts to exactly those projects. Archived projects are always
	// excluded. status is optional (empty = all). includeAll (the org-admin
	// path) returns every conversation in scope; otherwise results are scoped
	// to those callerMemberID asked or is the assigned responder for.
	ConversationsByProjectIDs(ctx context.Context, orgID uuid.UUID, projectIDs []uuid.UUID, status string, callerMemberID uuid.UUID, includeAll bool) ([]ConversationWithProject, error)
}

// ProjectByIDReader is the read-side port for resolving a single project's
// display name. Satisfied by *identity.ProjectStore.
type ProjectByIDReader interface {
	// ReadProjectName returns the project's name. Returns adaptererr.ErrNotFound
	// when absent.
	ReadProjectName(ctx context.Context, id uuid.UUID) (string, error)
}

// ProjectsDetailedReader is the read-side port for the Projects tab
// (ListProjectsDetailed). Satisfied by *identity.ProjectStore.
type ProjectsDetailedReader interface {
	// ProjectsDetailedByOrg returns every project in orgID with its archived
	// status and aggregate stats, ordered by creation time ascending.
	// includeArchived, when false, excludes archived projects entirely.
	ProjectsDetailedByOrg(ctx context.Context, orgID uuid.UUID, includeArchived bool) ([]ProjectDetailRow, error)
}

// InboxReader is the read-side port for a responder's pending questions.
type InboxReader interface {
	// InboxByMember returns the open conversations addressed to targetID,
	// newest first, as read models.
	InboxByMember(ctx context.Context, targetID uuid.UUID) ([]ConversationRecord, error)
	// OpenConversationsByOrg returns every open conversation across the org's
	// projects, newest first (the org-admin inbox bypass), as read models.
	OpenConversationsByOrg(ctx context.Context, orgID uuid.UUID) ([]ConversationRecord, error)
}

// ConfiguredChannelsReader is the read-side port for the external provider
// kinds configured for a project.
type ConfiguredChannelsReader interface {
	// ConfiguredKinds returns the configured provider kinds (empty when none).
	ConfiguredKinds(ctx context.Context, projectID uuid.UUID) ([]string, error)
}
