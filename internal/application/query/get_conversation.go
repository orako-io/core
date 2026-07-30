// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// GetConversationQuery is the input for fetching a conversation and its
// messages. This is the park-and-resume read (PRD §3.3).
type GetConversationQuery struct {
	// ConversationID identifies the conversation to fetch.
	ConversationID uuid.UUID
	// CallerMemberID is the authenticated caller. Access is granted only when the
	// caller is the asker, the assigned responder, or an org admin OF the
	// conversation's project.
	CallerMemberID uuid.UUID `exhaustruct:"optional"`
	// IsOrgAdmin grants access to any conversation whose project is in
	// CallerProjectIDs — NOT to conversations in other tenants' projects.
	IsOrgAdmin bool `exhaustruct:"optional"`
	// CallerProjectIDs is the set of projects the caller belongs to. The admin
	// bypass is honoured only for a conversation whose project is in this set,
	// closing the cross-tenant read where an admin of ANY org could fetch any
	// conversation by UUID (H1). Every caller (Connect + MCP) must pass it.
	CallerProjectIDs []uuid.UUID `exhaustruct:"optional"`
}

// candidateChecker admits pool candidates to an unclaimed conversation.
// *conversation.CandidateStore satisfies it.
type candidateChecker interface {
	IsActiveCandidate(ctx context.Context, conversationID, memberID uuid.UUID) (bool, error)
}

// attachmentReader lists a conversation's linked attachments as read models.
// *conversation.AttachmentStore satisfies it.
type attachmentReader interface {
	AttachmentsByConversation(ctx context.Context, conversationID uuid.UUID) ([]Attachment, error)
}

// signedURLMaker mints a short-lived download URL for a blob key.
// service.BlobStore satisfies it (Noop when storage is disabled).
type signedURLMaker interface {
	SignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	Enabled() bool
}

// attachmentURLTTL bounds a get_conversation download URL: long enough for the
// agent/dashboard to fetch, short enough that a leaked link expires quickly.
const attachmentURLTTL = 15 * time.Minute

// GetConversationHandler handles GetConversationQuery.
type GetConversationHandler struct {
	convReader   ConversationReader
	candidates   candidateChecker
	participants participantsBatchReader
	names        memberNamesReader
	attachments  attachmentReader
	blobs        signedURLMaker
}

// MustNewGetConversationHandler builds a handler. It panics on nil
// required dependencies. attachments and blobs may be nil (attachments simply
// never appear on the returned messages).
func MustNewGetConversationHandler(convReader ConversationReader, candidates candidateChecker, participants participantsBatchReader, names memberNamesReader, attachments attachmentReader, blobs signedURLMaker) GetConversationHandler {
	if convReader == nil || candidates == nil || participants == nil || names == nil {
		panic("GetConversationHandler requires non-nil convReader, candidates, participants, and names")
	}

	return GetConversationHandler{convReader: convReader, candidates: candidates, participants: participants, names: names, attachments: attachments, blobs: blobs}
}

// Handle fetches the conversation and all its messages, mapping them to DTOs.
// Open conversations stay participant-scoped. Resolved conversations are
// project knowledge and may be read by any caller whose token includes the
// project, matching search_history's project-wide reuse contract.
func (h GetConversationHandler) Handle(ctx context.Context, q GetConversationQuery) (ConversationView, error) {
	conv, err := h.convReader.ReadConversation(ctx, q.ConversationID)
	if err != nil {
		return ConversationView{}, errs.NotFoundError{Resource: "conversation"}
	}

	// Visibility: owner (asker) or the assigned responder, with an org-admin
	// bypass. Explicitly-added participants may also read the thread. A nil caller
	// never matches (a nil member_id must not grant access).
	// An unclaimed pool conversation is additionally visible to its active
	// candidates — they must read the thread before deciding to answer.
	// Once resolved, the thread is reusable project history: any caller whose
	// token includes the project may read it, exactly as search_history directs.
	// The admin bypass is scoped to the caller's own projects: an admin of a
	// different tenant, who is not a member of this conversation's project, gets
	// no bypass (H1 — cross-tenant read).
	adminForProject := q.IsOrgAdmin && slices.Contains(q.CallerProjectIDs, conv.ProjectID)

	resolvedForProject := conv.Status == model.ConversationStatusResolved &&
		slices.Contains(q.CallerProjectIDs, conv.ProjectID)
	if !visibleTo(conv, q.CallerMemberID, adminForProject) &&
		!resolvedForProject &&
		!h.addedParticipantMayView(ctx, conv.ID, q.CallerMemberID) &&
		!h.candidateMayView(ctx, conv, q.CallerMemberID) {
		return ConversationView{}, errs.ForbiddenError{Action: "view conversation"}
	}

	views, err := h.convReader.ReadMessages(ctx, q.ConversationID)
	if err != nil {
		return ConversationView{}, errs.InternalError{Err: err}
	}

	byMessage, err := h.attachmentsByMessage(ctx, conv.ID)
	if err != nil {
		return ConversationView{}, errs.InternalError{Err: err}
	}

	for i := range views {
		views[i].Attachments = byMessage[views[i].ID]
	}

	participants, err := resolveParticipants(ctx, h.participants, h.names, []participantSource{{
		ConversationID:    conv.ID,
		AskerMemberID:     conv.AskerMemberID,
		ResponderMemberID: conv.ResponderMemberID,
	}})
	if err != nil {
		return ConversationView{}, errs.InternalError{Err: err}
	}

	return ConversationView{
		ID:                conv.ID,
		ProjectID:         conv.ProjectID,
		AskerMemberID:     conv.AskerMemberID,
		ResponderMemberID: conv.ResponderMemberID,
		Status:            conv.Status,
		Question:          conv.Question,
		Title:             conv.Title,
		Messages:          views,
		CreatedAt:         conv.CreatedAt,
		UpdatedAt:         conv.UpdatedAt,
		Participants:      participants[conv.ID],
	}, nil
}

// addedParticipantMayView admits a member explicitly attached through
// add_participant. Errors deny access.
func (h GetConversationHandler) addedParticipantMayView(
	ctx context.Context,
	conversationID, caller uuid.UUID,
) bool {
	if caller == uuid.Nil {
		return false
	}

	byConversation, err := h.participants.ParticipantsByConversations(ctx, []uuid.UUID{conversationID})
	if err != nil {
		return false
	}

	return slices.ContainsFunc(byConversation[conversationID], func(participant model.ConversationParticipant) bool {
		return participant.MemberID == caller
	})
}

// candidateMayView admits an active candidate while the pool conversation is
// still unclaimed. Errors deny (visibility fails closed).
func (h GetConversationHandler) candidateMayView(ctx context.Context, conv ConversationRecord, caller uuid.UUID) bool {
	if conv.ResponderMemberID != uuid.Nil || caller == uuid.Nil {
		return false
	}

	active, err := h.candidates.IsActiveCandidate(ctx, conv.ID, caller)

	return err == nil && active
}

// visibleTo reports whether caller may view conv: an org admin always may;
// otherwise the caller must be a non-nil member who is the asker or the assigned
// responder. A nil caller is never granted access (guards the nil == nil trap
// when a conversation has no assigned responder).
func visibleTo(conv ConversationRecord, caller uuid.UUID, isOrgAdmin bool) bool {
	if isOrgAdmin {
		return true
	}

	if caller == uuid.Nil {
		return false
	}

	return caller == conv.AskerMemberID || caller == conv.ResponderMemberID
}

// attachmentsByMessage resolves a conversation's linked attachments and groups
// them by message id, minting a signed download URL for each. Best-effort: when
// storage is disabled or a URL cannot be signed, the attachment is omitted
// rather than failing the whole read. A nil attachments reader (attachments
// feature unwired) yields an empty map.
func (h GetConversationHandler) attachmentsByMessage(ctx context.Context, conversationID uuid.UUID) (map[uuid.UUID][]AttachmentView, error) {
	out := map[uuid.UUID][]AttachmentView{}

	if h.attachments == nil || h.blobs == nil || !h.blobs.Enabled() {
		return out, nil
	}

	atts, err := h.attachments.AttachmentsByConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	for _, a := range atts {
		url, err := h.blobs.SignedGetURL(ctx, a.StorageKey, attachmentURLTTL)
		if err != nil {
			slog.WarnContext(ctx, "get_conversation: signing attachment URL",
				slog.String("attachment_id", a.ID.String()), slog.Any("error", err))

			continue
		}

		out[a.MessageID] = append(out[a.MessageID], AttachmentView{
			ID:          a.ID,
			Filename:    a.Filename,
			MimeType:    a.MimeType,
			SizeBytes:   a.SizeBytes,
			DownloadURL: url,
		})
	}

	return out, nil
}
