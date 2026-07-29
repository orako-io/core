// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

// Participant role labels.
const (
	participantRoleAsker     = "asker"
	participantRoleResponder = "specialist"
	participantRoleAdded     = "added"
	// participantRoleCandidate marks a still-active pool candidate: contacted
	// (they received the question DM) but not yet the assigned responder.
	participantRoleCandidate = "candidate"
)

// participantsBatchReader resolves the explicitly-added participants and the
// still-active pool candidates for a set of conversations, one round-trip each.
// *conversation.Store satisfies it.
type participantsBatchReader interface {
	ParticipantsByConversations(ctx context.Context, conversationIDs []uuid.UUID) (map[uuid.UUID][]model.ConversationParticipant, error)
	ActiveCandidatesByConversations(ctx context.Context, conversationIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error)
}

// memberNamesReader resolves display names for a set of members in one
// round-trip. *conversation.Store satisfies it.
type memberNamesReader interface {
	MemberNamesByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
}

// participantSource is the per-conversation input to resolveParticipants: the
// implicit members (asker, assigned responder) plus the conversation id used
// to look up explicitly-added ones.
type participantSource struct {
	ConversationID    uuid.UUID
	AskerMemberID     uuid.UUID
	ResponderMemberID uuid.UUID
}

// resolveParticipants builds the ordered, deduplicated participant list for
// each conversation: asker first, assigned responder second, then
// explicitly-added members, all with display names resolved in two batched
// queries (participants, names). A missing name degrades to a short id — the
// list must render even if a member row vanished.
func resolveParticipants(
	ctx context.Context,
	parts participantsBatchReader,
	names memberNamesReader,
	sources []participantSource,
) (map[uuid.UUID][]Participant, error) {
	if len(sources) == 0 {
		return map[uuid.UUID][]Participant{}, nil
	}

	convIDs := make([]uuid.UUID, 0, len(sources))
	for _, s := range sources {
		convIDs = append(convIDs, s.ConversationID)
	}

	added, err := parts.ParticipantsByConversations(ctx, convIDs)
	if err != nil {
		return nil, err
	}

	// Still-active pool candidates: contacted, no answer of their own yet — they belong
	// on the participant list (role "candidate") so an unclaimed pool ask shows
	// WHO was reached, not just the asker.
	candidates, err := parts.ActiveCandidatesByConversations(ctx, convIDs)
	if err != nil {
		return nil, err
	}

	nameByID, err := names.MemberNamesByIDs(ctx, collectNameIDs(sources, added, candidates))
	if err != nil {
		return nil, err
	}

	displayName := func(id uuid.UUID) string {
		if n := nameByID[id]; n != "" {
			return n
		}

		return id.String()[:8]
	}

	out := make(map[uuid.UUID][]Participant, len(sources))

	for _, s := range sources {
		seen := map[uuid.UUID]bool{uuid.Nil: true}

		var list []Participant

		appendOne := func(id uuid.UUID, role string) {
			if seen[id] {
				return
			}

			seen[id] = true

			list = append(list, Participant{MemberID: id, DisplayName: displayName(id), Role: role})
		}

		appendOne(s.AskerMemberID, participantRoleAsker)
		appendOne(s.ResponderMemberID, participantRoleResponder)

		for _, p := range added[s.ConversationID] {
			appendOne(p.MemberID, participantRoleAdded)
		}

		for _, id := range candidates[s.ConversationID] {
			appendOne(id, participantRoleCandidate)
		}

		out[s.ConversationID] = list
	}

	return out, nil
}

// collectNameIDs gathers every member id the participant lists will render
// (asker, responder, added, candidates), deduplicated, for one batched
// name lookup.
func collectNameIDs(
	sources []participantSource,
	added map[uuid.UUID][]model.ConversationParticipant,
	candidates map[uuid.UUID][]uuid.UUID,
) []uuid.UUID {
	nameIDs := make(map[uuid.UUID]bool)

	for _, s := range sources {
		if s.AskerMemberID != uuid.Nil {
			nameIDs[s.AskerMemberID] = true
		}

		if s.ResponderMemberID != uuid.Nil {
			nameIDs[s.ResponderMemberID] = true
		}

		for _, p := range added[s.ConversationID] {
			nameIDs[p.MemberID] = true
		}

		for _, id := range candidates[s.ConversationID] {
			nameIDs[id] = true
		}
	}

	ids := make([]uuid.UUID, 0, len(nameIDs))
	for id := range nameIDs {
		ids = append(ids, id)
	}

	return ids
}
