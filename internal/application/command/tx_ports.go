// SPDX-License-Identifier: AGPL-3.0-or-later

// tx_ports.go collects the narrow write ports that command handlers compose
// under a single service.Transactor. Each groups the writes of one use case
// that must commit or roll back together; the transaction lives in the handler,
// not in a dedicated cross-store adapter (the former *AtomicStore types, now
// dissolved).

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/service"
)

// conversationOpener is the narrow atomic write port for opening a new
// conversation: the conversation row and first message commit together or not
// at all.
type conversationOpener interface {
	// candidates is the pool computed at dispatch (empty for direct asks);
	// it commits atomically with the conversation so an unclaimed
	// conversation can never exist without its pool.
	OpenConversation(ctx context.Context, conv model.Conversation, msg model.Message, candidates []uuid.UUID) error
}

// memberProvisioner provisions a member for AddMember. The handler composes the
// two calls in one transaction (via service.Transactor) so the find-or-create and
// the project-membership insert commit together — replacing the former
// MemberAtomicStore.
type memberProvisioner interface {
	// FindOrCreateByEmail resolves the member for email, creating it when absent
	// and reactivating a soft-removed match. freshInvite is true when the member
	// was newly created or reactivated (the invitation lifecycle must run).
	FindOrCreateByEmail(ctx context.Context, email string, newMember model.Member) (memberID uuid.UUID, freshInvite bool, err error)
	// AddToProject adds the member to the project idempotently. added is false
	// when the membership already existed.
	AddToProject(ctx context.Context, membership repository.ProjectMembership) (added bool, err error)
}

// providerLookup resolves the provider that delivers to a given responder,
// routing by delivery channel. Resolving before any state change lets handlers
// fail fast without orphaning a conversation.
type providerLookup interface {
	// ForMember returns the delivery provider: dashboard-channel members always
	// resolve; external channels return an error wrapping
	// service.ErrNoProvider when the project has none configured.
	ForMember(ctx context.Context, projectID, memberID uuid.UUID) (service.Provider, error)
}

// AskResult is the result of AskHandler.Handle.
type AskResult struct {
	ConversationID uuid.UUID
	// InlineAnswer is the responder's answer when it arrived within the
	// synchronous wait window; empty when Answered is false.
	InlineAnswer string `exhaustruct:"optional"`
	Answered     bool   `exhaustruct:"optional"`
	// PoolSize is how many candidates were notified (0 for a direct ask).
	PoolSize int `exhaustruct:"optional"`
	// ProjectName is the resolved name of the conversation's project, echoed so
	// the agent can name where the question went. Best-effort: empty when the
	// lookup fails — never a reason to fail the ask.
	ProjectName string `exhaustruct:"optional"`
	// RecipientNames are the display name(s) the question reached: one for a
	// direct ask, the pool candidates for a domains dispatch. Best-effort: an
	// id that fails to resolve is omitted.
	RecipientNames []string `exhaustruct:"optional"`
}
