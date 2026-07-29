// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// --- ports ---

// knowledgeCreator is the write port for storing a new curated entry.
type knowledgeCreator interface {
	CreateEntry(ctx context.Context, e model.KnowledgeEntry) (model.KnowledgeEntry, error)
}

// knowledgeReadWriter is the read+write port the edit/mark-stale commands need:
// read the entry (to verify org ownership) then mutate it.
type knowledgeReadWriter interface {
	EntryByID(ctx context.Context, id uuid.UUID) (model.KnowledgeEntry, error)
	UpdateEntry(ctx context.Context, e model.KnowledgeEntry) (model.KnowledgeEntry, error)
	MarkStale(ctx context.Context, id uuid.UUID) error
}

// knowledgeLifecycle is the read+lifecycle port for the Phase 2 approval-queue
// and revalidation commands: read the entry (to verify org ownership) then
// activate it (approve/revalidate) or delete it (dismiss).
type knowledgeLifecycle interface {
	EntryByID(ctx context.Context, id uuid.UUID) (model.KnowledgeEntry, error)
	Activate(ctx context.Context, id uuid.UUID) error
	DeleteEntry(ctx context.Context, id uuid.UUID) error
}

// conversationPromoteReader reads a resolved conversation and its messages so
// PromoteConversationToKnowledge can distill a curated entry from it. Satisfied
// by *conversation.Store.
type conversationPromoteReader interface {
	ConversationByID(ctx context.Context, id uuid.UUID) (model.Conversation, error)
	MessagesByConversation(ctx context.Context, conversationID uuid.UUID) ([]model.Message, error)
}

// --- CreateKnowledgeEntry ---

// CreateKnowledgeEntryCommand authors one curated knowledge entry. OrgID and
// AuthorMemberID come from the caller identity (RBAC context / MCP token), never
// the request payload. ProjectID is optional (uuid.Nil = org-wide).
type CreateKnowledgeEntryCommand struct {
	OrgID          uuid.UUID
	ProjectID      uuid.UUID `exhaustruct:"optional"`
	AuthorMemberID uuid.UUID `exhaustruct:"optional"`
	Question       string
	Answer         string
	Summary        string   `exhaustruct:"optional"`
	Tags           []string `exhaustruct:"optional"`
	Entities       []string `exhaustruct:"optional"`
}

// CreateKnowledgeEntryResult carries the new entry's id.
type CreateKnowledgeEntryResult struct {
	EntryID uuid.UUID
}

// CreateKnowledgeEntryHandler handles CreateKnowledgeEntryCommand.
type CreateKnowledgeEntryHandler struct {
	store knowledgeCreator
}

// MustNewCreateKnowledgeEntryHandler builds a handler. It panics on a
// nil dependency.
func MustNewCreateKnowledgeEntryHandler(store knowledgeCreator) CreateKnowledgeEntryHandler {
	if store == nil {
		panic("CreateKnowledgeEntryHandler requires a non-nil store")
	}

	return CreateKnowledgeEntryHandler{store: store}
}

// Handle validates + normalizes the input via the domain constructor (same
// normalizers as conversation metadata) and persists the entry.
//
//nolint:dupl // parallel to SuggestKnowledgeEntryHandler.Handle but a distinct use case (publish vs propose); merging would couple the two flows.
func (h CreateKnowledgeEntryHandler) Handle(ctx context.Context, cmd CreateKnowledgeEntryCommand) (CreateKnowledgeEntryResult, error) {
	if cmd.OrgID == uuid.Nil {
		return CreateKnowledgeEntryResult{}, errs.InvalidError{Field: fieldOrgID, Reason: reasonNilUUID}
	}

	entry, err := model.NewKnowledgeEntry(
		uuid.New(),
		cmd.OrgID,
		cmd.ProjectID,
		cmd.AuthorMemberID,
		cmd.Question,
		cmd.Answer,
		cmd.Summary,
		cmd.Tags,
		cmd.Entities,
	)
	if err != nil {
		return CreateKnowledgeEntryResult{}, err
	}

	stored, err := h.store.CreateEntry(ctx, entry)
	if err != nil {
		return CreateKnowledgeEntryResult{}, translateErr(err, "knowledge entry")
	}

	return CreateKnowledgeEntryResult{EntryID: stored.ID}, nil
}

// --- UpdateKnowledgeEntry ---

// UpdateKnowledgeEntryCommand rewrites the editable fields of a curated entry.
// OrgID (from the caller) must match the entry's org, or the entry is reported
// not-found (never leak cross-org existence). An empty Status leaves the status
// unchanged.
type UpdateKnowledgeEntryCommand struct {
	EntryID  uuid.UUID
	OrgID    uuid.UUID
	Question string
	Answer   string
	Summary  string   `exhaustruct:"optional"`
	Tags     []string `exhaustruct:"optional"`
	Entities []string `exhaustruct:"optional"`
	Status   string   `exhaustruct:"optional"`
}

// UpdateKnowledgeEntryResult is the (empty) result of UpdateKnowledgeEntry.
type UpdateKnowledgeEntryResult struct{}

// UpdateKnowledgeEntryHandler handles UpdateKnowledgeEntryCommand.
type UpdateKnowledgeEntryHandler struct {
	store knowledgeReadWriter
}

// MustNewUpdateKnowledgeEntryHandler builds a handler. It panics on a
// nil dependency.
func MustNewUpdateKnowledgeEntryHandler(store knowledgeReadWriter) UpdateKnowledgeEntryHandler {
	if store == nil {
		panic("UpdateKnowledgeEntryHandler requires a non-nil store")
	}

	return UpdateKnowledgeEntryHandler{store: store}
}

// Handle verifies org ownership, re-validates + re-normalizes the fields via the
// domain constructor, and persists the update.
func (h UpdateKnowledgeEntryHandler) Handle(ctx context.Context, cmd UpdateKnowledgeEntryCommand) (UpdateKnowledgeEntryResult, error) {
	current, err := h.loadOwned(ctx, cmd.EntryID, cmd.OrgID)
	if err != nil {
		return UpdateKnowledgeEntryResult{}, err
	}

	// Re-run the domain constructor to validate + normalize the new content
	// identically to create; it preserves org/project/author from the current
	// row (those are immutable on edit).
	rebuilt, err := model.NewKnowledgeEntry(
		current.ID,
		current.OrgID,
		current.ProjectID,
		current.AuthorMemberID,
		cmd.Question,
		cmd.Answer,
		cmd.Summary,
		cmd.Tags,
		cmd.Entities,
	)
	if err != nil {
		return UpdateKnowledgeEntryResult{}, err
	}

	status, err := resolveKnowledgeStatus(cmd.Status, current.Status)
	if err != nil {
		return UpdateKnowledgeEntryResult{}, err
	}

	rebuilt.Status = status

	if _, err := h.store.UpdateEntry(ctx, rebuilt); err != nil {
		return UpdateKnowledgeEntryResult{}, translateErr(err, "knowledge entry")
	}

	return UpdateKnowledgeEntryResult{}, nil
}

// --- MarkKnowledgeStale ---

// MarkKnowledgeStaleCommand flags a curated entry stale so it drops out of
// search. OrgID (from the caller) must match the entry's org.
type MarkKnowledgeStaleCommand struct {
	EntryID uuid.UUID
	OrgID   uuid.UUID
}

// MarkKnowledgeStaleResult is the (empty) result of MarkKnowledgeStale.
type MarkKnowledgeStaleResult struct{}

// MarkKnowledgeStaleHandler handles MarkKnowledgeStaleCommand.
type MarkKnowledgeStaleHandler struct {
	store knowledgeReadWriter
}

// MustNewMarkKnowledgeStaleHandler builds a handler. It panics on a
// nil dependency.
func MustNewMarkKnowledgeStaleHandler(store knowledgeReadWriter) MarkKnowledgeStaleHandler {
	if store == nil {
		panic("MarkKnowledgeStaleHandler requires a non-nil store")
	}

	return MarkKnowledgeStaleHandler{store: store}
}

// Handle verifies org ownership then flips the entry to stale.
func (h MarkKnowledgeStaleHandler) Handle(ctx context.Context, cmd MarkKnowledgeStaleCommand) (MarkKnowledgeStaleResult, error) {
	if _, err := h.loadOwned(ctx, cmd.EntryID, cmd.OrgID); err != nil {
		return MarkKnowledgeStaleResult{}, err
	}

	if err := h.store.MarkStale(ctx, cmd.EntryID); err != nil {
		return MarkKnowledgeStaleResult{}, translateErr(err, "knowledge entry")
	}

	return MarkKnowledgeStaleResult{}, nil
}

// loadOwned reads the entry and verifies it belongs to orgID, reporting a
// not-found (never a cross-org existence leak) on mismatch. Shared by the edit
// and mark-stale handlers, so both use the same read+write port.
func (h MarkKnowledgeStaleHandler) loadOwned(ctx context.Context, id, orgID uuid.UUID) (model.KnowledgeEntry, error) {
	return loadOwnedKnowledge(ctx, h.store, id, orgID)
}

// loadOwned reads the entry and verifies org ownership (see the shared helper).
func (h UpdateKnowledgeEntryHandler) loadOwned(ctx context.Context, id, orgID uuid.UUID) (model.KnowledgeEntry, error) {
	return loadOwnedKnowledge(ctx, h.store, id, orgID)
}

// loadOwnedKnowledge reads id and verifies it belongs to orgID. A cross-org (or
// missing) entry is reported as not-found so existence never leaks across orgs.
func loadOwnedKnowledge(ctx context.Context, store knowledgeReadWriter, id, orgID uuid.UUID) (model.KnowledgeEntry, error) {
	entry, err := store.EntryByID(ctx, id)
	if err != nil {
		return model.KnowledgeEntry{}, translateErr(err, "knowledge entry")
	}

	if entry.OrgID != orgID {
		return model.KnowledgeEntry{}, errs.NotFoundError{Resource: "knowledge entry"}
	}

	return entry, nil
}

// resolveKnowledgeStatus validates a requested status transition. An empty
// requested status keeps the current one; a non-empty value must be a recognized
// KnowledgeStatus.
func resolveKnowledgeStatus(requested string, current model.KnowledgeStatus) (model.KnowledgeStatus, error) {
	if requested == "" {
		return current, nil
	}

	status := model.KnowledgeStatus(requested)
	if !status.Valid() {
		return "", errs.InvalidError{Field: "status", Reason: "must be one of active, stale, pending"}
	}

	return status, nil
}

// --- SuggestKnowledgeEntry (agent-suggested, pending) ---

// SuggestKnowledgeEntryCommand proposes one agent-suggested knowledge entry from
// an agent's own work. It lands source='agent_suggested', status='pending' — NOT
// published, NOT searchable — until a human approves it (the trust gradient).
// OrgID and AuthorMemberID come from the caller identity, never the payload.
type SuggestKnowledgeEntryCommand struct {
	OrgID          uuid.UUID
	ProjectID      uuid.UUID `exhaustruct:"optional"`
	AuthorMemberID uuid.UUID `exhaustruct:"optional"`
	Question       string
	Answer         string
	Summary        string   `exhaustruct:"optional"`
	Tags           []string `exhaustruct:"optional"`
	Entities       []string `exhaustruct:"optional"`
}

// SuggestKnowledgeEntryResult carries the new pending entry's id.
type SuggestKnowledgeEntryResult struct {
	EntryID uuid.UUID
}

// SuggestKnowledgeEntryHandler handles SuggestKnowledgeEntryCommand.
type SuggestKnowledgeEntryHandler struct {
	store knowledgeCreator
}

// MustNewSuggestKnowledgeEntryHandler builds a handler. It panics on a
// nil dependency.
func MustNewSuggestKnowledgeEntryHandler(store knowledgeCreator) SuggestKnowledgeEntryHandler {
	if store == nil {
		panic("SuggestKnowledgeEntryHandler requires a non-nil store")
	}

	return SuggestKnowledgeEntryHandler{store: store}
}

// Handle validates + normalizes the input via the agent-suggested constructor
// (source=agent_suggested, status=pending) and persists the pending entry.
//
//nolint:dupl // parallel to CreateKnowledgeEntryHandler.Handle but a distinct use case (propose vs publish); merging would couple the two flows.
func (h SuggestKnowledgeEntryHandler) Handle(ctx context.Context, cmd SuggestKnowledgeEntryCommand) (SuggestKnowledgeEntryResult, error) {
	if cmd.OrgID == uuid.Nil {
		return SuggestKnowledgeEntryResult{}, errs.InvalidError{Field: fieldOrgID, Reason: reasonNilUUID}
	}

	entry, err := model.NewSuggestedKnowledgeEntry(
		uuid.New(),
		cmd.OrgID,
		cmd.ProjectID,
		cmd.AuthorMemberID,
		cmd.Question,
		cmd.Answer,
		cmd.Summary,
		cmd.Tags,
		cmd.Entities,
	)
	if err != nil {
		return SuggestKnowledgeEntryResult{}, err
	}

	stored, err := h.store.CreateEntry(ctx, entry)
	if err != nil {
		return SuggestKnowledgeEntryResult{}, translateErr(err, "knowledge entry")
	}

	return SuggestKnowledgeEntryResult{EntryID: stored.ID}, nil
}

// --- ApproveKnowledgeEntry ---

// ApproveKnowledgeEntryCommand approves a pending agent-suggested entry: it flips
// status to active (now human-vetted, so it appears in search). Source is kept
// as 'agent_suggested' for provenance. OrgID (from the caller) must match the
// entry's org.
type ApproveKnowledgeEntryCommand struct {
	EntryID uuid.UUID
	OrgID   uuid.UUID
}

// ApproveKnowledgeEntryResult is the (empty) result of ApproveKnowledgeEntry.
type ApproveKnowledgeEntryResult struct{}

// ApproveKnowledgeEntryHandler handles ApproveKnowledgeEntryCommand.
type ApproveKnowledgeEntryHandler struct {
	store knowledgeLifecycle
}

// MustNewApproveKnowledgeEntryHandler builds a handler. It panics on a
// nil dependency.
func MustNewApproveKnowledgeEntryHandler(store knowledgeLifecycle) ApproveKnowledgeEntryHandler {
	if store == nil {
		panic("ApproveKnowledgeEntryHandler requires a non-nil store")
	}

	return ApproveKnowledgeEntryHandler{store: store}
}

// Handle verifies org ownership then activates the entry.
func (h ApproveKnowledgeEntryHandler) Handle(ctx context.Context, cmd ApproveKnowledgeEntryCommand) (ApproveKnowledgeEntryResult, error) {
	if _, err := loadOwnedForLifecycle(ctx, h.store, cmd.EntryID, cmd.OrgID); err != nil {
		return ApproveKnowledgeEntryResult{}, err
	}

	if err := h.store.Activate(ctx, cmd.EntryID); err != nil {
		return ApproveKnowledgeEntryResult{}, translateErr(err, "knowledge entry")
	}

	return ApproveKnowledgeEntryResult{}, nil
}

// --- DismissKnowledgeEntry ---

// DismissKnowledgeEntryCommand rejects a pending agent-suggested entry by
// deleting the row (no 'dismissed' status is kept). OrgID (from the caller) must
// match the entry's org, and only a pending entry may be dismissed this way (an
// active entry is retired via MarkKnowledgeStale, not deleted).
type DismissKnowledgeEntryCommand struct {
	EntryID uuid.UUID
	OrgID   uuid.UUID
}

// DismissKnowledgeEntryResult is the (empty) result of DismissKnowledgeEntry.
type DismissKnowledgeEntryResult struct{}

// DismissKnowledgeEntryHandler handles DismissKnowledgeEntryCommand.
type DismissKnowledgeEntryHandler struct {
	store knowledgeLifecycle
}

// MustNewDismissKnowledgeEntryHandler builds a handler. It panics on a
// nil dependency.
func MustNewDismissKnowledgeEntryHandler(store knowledgeLifecycle) DismissKnowledgeEntryHandler {
	if store == nil {
		panic("DismissKnowledgeEntryHandler requires a non-nil store")
	}

	return DismissKnowledgeEntryHandler{store: store}
}

// Handle verifies org ownership and that the entry is still pending, then deletes it.
func (h DismissKnowledgeEntryHandler) Handle(ctx context.Context, cmd DismissKnowledgeEntryCommand) (DismissKnowledgeEntryResult, error) {
	entry, err := loadOwnedForLifecycle(ctx, h.store, cmd.EntryID, cmd.OrgID)
	if err != nil {
		return DismissKnowledgeEntryResult{}, err
	}

	// Dismiss is the reject-a-suggestion path; refuse to delete a published entry
	// through it (that would be a silent data loss — use mark-stale instead).
	if entry.Status != model.KnowledgeStatusPending {
		return DismissKnowledgeEntryResult{}, errs.InvalidError{
			Field:  "status",
			Reason: "only a pending entry can be dismissed; retire a published entry with mark-stale",
		}
	}

	if err := h.store.DeleteEntry(ctx, cmd.EntryID); err != nil {
		return DismissKnowledgeEntryResult{}, translateErr(err, "knowledge entry")
	}

	return DismissKnowledgeEntryResult{}, nil
}

// --- RevalidateKnowledgeEntry ---

// RevalidateKnowledgeEntryCommand re-confirms a stale entry as current: it flips
// status back to active and bumps updated_at ("last reviewed"). OrgID (from the
// caller) must match the entry's org.
type RevalidateKnowledgeEntryCommand struct {
	EntryID uuid.UUID
	OrgID   uuid.UUID
}

// RevalidateKnowledgeEntryResult is the (empty) result of RevalidateKnowledgeEntry.
type RevalidateKnowledgeEntryResult struct{}

// RevalidateKnowledgeEntryHandler handles RevalidateKnowledgeEntryCommand.
type RevalidateKnowledgeEntryHandler struct {
	store knowledgeLifecycle
}

// MustNewRevalidateKnowledgeEntryHandler builds a handler. It panics
// on a nil dependency.
func MustNewRevalidateKnowledgeEntryHandler(store knowledgeLifecycle) RevalidateKnowledgeEntryHandler {
	if store == nil {
		panic("RevalidateKnowledgeEntryHandler requires a non-nil store")
	}

	return RevalidateKnowledgeEntryHandler{store: store}
}

// Handle verifies org ownership then re-activates the entry.
func (h RevalidateKnowledgeEntryHandler) Handle(ctx context.Context, cmd RevalidateKnowledgeEntryCommand) (RevalidateKnowledgeEntryResult, error) {
	if _, err := loadOwnedForLifecycle(ctx, h.store, cmd.EntryID, cmd.OrgID); err != nil {
		return RevalidateKnowledgeEntryResult{}, err
	}

	if err := h.store.Activate(ctx, cmd.EntryID); err != nil {
		return RevalidateKnowledgeEntryResult{}, translateErr(err, "knowledge entry")
	}

	return RevalidateKnowledgeEntryResult{}, nil
}

// loadOwnedForLifecycle reads id and verifies it belongs to orgID (cross-org or
// missing is reported as not-found — no existence leak). Shared by the
// approve/dismiss/revalidate handlers.
func loadOwnedForLifecycle(ctx context.Context, store knowledgeLifecycle, id, orgID uuid.UUID) (model.KnowledgeEntry, error) {
	entry, err := store.EntryByID(ctx, id)
	if err != nil {
		return model.KnowledgeEntry{}, translateErr(err, "knowledge entry")
	}

	if entry.OrgID != orgID {
		return model.KnowledgeEntry{}, errs.NotFoundError{Resource: "knowledge entry"}
	}

	return entry, nil
}

// --- PromoteConversationToKnowledge ---

// PromoteConversationToKnowledgeCommand distills a RESOLVED conversation into a
// curated knowledge entry (source=curated, status=active), authored by the
// promoter. The question, answer (composed from the first responder's answer
// messages), and the conversation's curated summary/tags/entities carry over.
// OrgID + PromoterMemberID come from the caller identity; the conversation must
// belong to a project in the caller's org (verified by the transport before this
// runs — the command trusts OrgID).
type PromoteConversationToKnowledgeCommand struct {
	ConversationID   uuid.UUID
	OrgID            uuid.UUID
	PromoterMemberID uuid.UUID `exhaustruct:"optional"`
}

// PromoteConversationToKnowledgeResult carries the new curated entry's id.
type PromoteConversationToKnowledgeResult struct {
	EntryID uuid.UUID
}

// PromoteConversationToKnowledgeHandler handles
// PromoteConversationToKnowledgeCommand.
type PromoteConversationToKnowledgeHandler struct {
	conversations conversationPromoteReader
	store         knowledgeCreator
}

// MustNewPromoteConversationToKnowledgeHandler builds a handler. It panics on a nil dependency.
func MustNewPromoteConversationToKnowledgeHandler(conversations conversationPromoteReader, store knowledgeCreator) PromoteConversationToKnowledgeHandler {
	if conversations == nil || store == nil {
		panic("PromoteConversationToKnowledgeHandler requires non-nil conversations reader and store")
	}

	return PromoteConversationToKnowledgeHandler{conversations: conversations, store: store}
}

// Handle reads the resolved conversation, composes its answer, and creates a
// curated entry. It rejects a conversation that is not resolved (only a settled
// answer is worth curating).
func (h PromoteConversationToKnowledgeHandler) Handle(ctx context.Context, cmd PromoteConversationToKnowledgeCommand) (PromoteConversationToKnowledgeResult, error) {
	if cmd.OrgID == uuid.Nil {
		return PromoteConversationToKnowledgeResult{}, errs.InvalidError{Field: fieldOrgID, Reason: reasonNilUUID}
	}

	conv, err := h.conversations.ConversationByID(ctx, cmd.ConversationID)
	if err != nil {
		return PromoteConversationToKnowledgeResult{}, translateErr(err, "conversation")
	}

	if conv.Status != model.ConversationStatusResolved {
		return PromoteConversationToKnowledgeResult{}, errs.InvalidError{
			Field:  "conversation",
			Reason: "only a resolved conversation can be promoted to knowledge",
		}
	}

	msgs, err := h.conversations.MessagesByConversation(ctx, cmd.ConversationID)
	if err != nil {
		return PromoteConversationToKnowledgeResult{}, translateErr(err, "conversation")
	}

	answer := composeConversationAnswer(msgs)
	if answer == "" {
		return PromoteConversationToKnowledgeResult{}, errs.InvalidError{
			Field:  "conversation",
			Reason: "conversation has no answer to promote",
		}
	}

	entry, err := model.NewKnowledgeEntry(
		uuid.New(),
		cmd.OrgID,
		conv.ProjectID,
		cmd.PromoterMemberID,
		conv.Question,
		answer,
		conv.Summary,
		conv.Tags,
		conv.Entities,
	)
	if err != nil {
		return PromoteConversationToKnowledgeResult{}, err
	}

	stored, err := h.store.CreateEntry(ctx, entry)
	if err != nil {
		return PromoteConversationToKnowledgeResult{}, translateErr(err, "knowledge entry")
	}

	return PromoteConversationToKnowledgeResult{EntryID: stored.ID}, nil
}

// composeConversationAnswer joins the FIRST responder's answer messages (role =
// answer) into the curated answer body, in chronological order. It anchors on
// the author of the earliest answer message so second opinions and later
// responders' asides do not bleed into the promoted answer. Returns "" when no
// answer message exists.
func composeConversationAnswer(msgs []model.Message) string {
	firstResponder := uuid.Nil

	for _, m := range msgs {
		if m.Role == model.MessageRoleAnswer {
			firstResponder = m.AuthorMemberID

			break
		}
	}

	if firstResponder == uuid.Nil {
		return ""
	}

	var parts []string

	for _, m := range msgs {
		if m.Role == model.MessageRoleAnswer && m.AuthorMemberID == firstResponder {
			if body := strings.TrimSpace(m.Body); body != "" {
				parts = append(parts, body)
			}
		}
	}

	return strings.Join(parts, "\n\n")
}
