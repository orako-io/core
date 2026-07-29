// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"slices"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/service"
)

// ---- fakeConversationReader ----------------------------------------------

type fakeConversationReader struct {
	conversations map[uuid.UUID]model.Conversation
	messages      map[uuid.UUID][]model.Message
}

func newFakeConversationReader() *fakeConversationReader {
	return &fakeConversationReader{
		conversations: make(map[uuid.UUID]model.Conversation),
		messages:      make(map[uuid.UUID][]model.Message),
	}
}

func (f *fakeConversationReader) ReadConversation(_ context.Context, id uuid.UUID) (ConversationRecord, error) {
	c, ok := f.conversations[id]
	if !ok {
		return ConversationRecord{}, adaptererr.ErrNotFound
	}

	return ConversationRecord{
		ID:                c.ID,
		ProjectID:         c.ProjectID,
		AskerMemberID:     c.AskerMemberID,
		ResponderMemberID: c.ResponderMemberID,
		Status:            c.Status,
		Question:          c.Question,
		Title:             c.DisplayTitle(),
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
	}, nil
}

func (f *fakeConversationReader) ReadMessages(_ context.Context, conversationID uuid.UUID) ([]MessageView, error) {
	msgs := f.messages[conversationID]
	views := make([]MessageView, len(msgs))

	for i, m := range msgs {
		views[i] = MessageView{
			ID:             m.ID,
			AuthorMemberID: m.AuthorMemberID,
			Role:           m.Role,
			Body:           m.Body,
			Source:         m.Source,
			AgentClient:    m.AgentClient,
			CreatedAt:      m.CreatedAt,
		}
	}

	return views, nil
}

// ---- fakeCandidateReader ---------------------------------------------------

// fakeCandidateReader serves both candidate ports the query handlers use:
// candidateChecker (GetConversation) and openPoolReader (ListInbox).
type fakeCandidateReader struct {
	// active marks a member an active candidate of a conversation.
	active map[uuid.UUID]map[uuid.UUID]bool
	// pool is what OpenPoolFor returns for any member.
	pool []model.Conversation
	err  error
}

func newFakeCandidateReader() *fakeCandidateReader {
	return &fakeCandidateReader{active: make(map[uuid.UUID]map[uuid.UUID]bool)}
}

func (f *fakeCandidateReader) IsActiveCandidate(_ context.Context, conversationID, memberID uuid.UUID) (bool, error) {
	return f.active[conversationID][memberID], f.err
}

func (f *fakeCandidateReader) OpenPoolFor(_ context.Context, _ uuid.UUID) ([]ConversationRecord, error) {
	return convsToRecords(f.pool), f.err
}

// convsToRecords maps test conversation aggregates to their read models,
// mirroring the adapter's conversationsToRecords.
func convsToRecords(convs []model.Conversation) []ConversationRecord {
	records := make([]ConversationRecord, len(convs))
	for i, c := range convs {
		records[i] = ConversationRecord{
			ID:                c.ID,
			ProjectID:         c.ProjectID,
			AskerMemberID:     c.AskerMemberID,
			ResponderMemberID: c.ResponderMemberID,
			Status:            c.Status,
			Question:          c.Question,
			Title:             c.DisplayTitle(),
			CreatedAt:         c.CreatedAt,
			UpdatedAt:         c.UpdatedAt,
		}
	}

	return records
}

// ---- fakeProjectReader ---------------------------------------------------

type fakeProjectReader struct {
	memberships []repository.ProjectMembership
}

func (f *fakeProjectReader) MembersByProject(_ context.Context, projectID uuid.UUID) ([]repository.ProjectMembership, error) {
	var out []repository.ProjectMembership

	for _, m := range f.memberships {
		if m.ProjectID == projectID {
			out = append(out, m)
		}
	}

	return out, nil
}

// ---- fakeMemberReader ----------------------------------------------------

type fakeMemberReader struct {
	members map[uuid.UUID]model.Member
}

func newFakeMemberReader() *fakeMemberReader {
	return &fakeMemberReader{members: make(map[uuid.UUID]model.Member)}
}

func (f *fakeMemberReader) ReadMember(_ context.Context, id uuid.UUID) (MemberView, error) {
	m, ok := f.members[id]
	if !ok {
		return MemberView{}, adaptererr.ErrNotFound
	}

	return NewMemberView(m), nil
}

func (f *fakeMemberReader) ReadMembers(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]MemberView, error) {
	out := make(map[uuid.UUID]MemberView, len(ids))
	for _, id := range ids {
		if member, ok := f.members[id]; ok {
			out[id] = NewMemberView(member)
		}
	}

	return out, nil
}

// ---- fakePresenceReader --------------------------------------------------

type fakePresenceReader struct {
	records map[uuid.UUID]model.Presence
}

func newFakePresenceReader() *fakePresenceReader {
	return &fakePresenceReader{records: make(map[uuid.UUID]model.Presence)}
}

func (f *fakePresenceReader) ReadOnline(_ context.Context, memberID uuid.UUID) (bool, error) {
	p, ok := f.records[memberID]
	if !ok {
		return false, adaptererr.ErrNotFound
	}

	return p.OnlineNow(), nil
}

func (f *fakePresenceReader) ReadOnlineByMembers(_ context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool, len(memberIDs))
	for _, id := range memberIDs {
		if presence, ok := f.records[id]; ok {
			out[id] = presence.OnlineNow()
		}
	}

	return out, nil
}

// ---- fakeProjectsByMemberReader ------------------------------------------

type fakeProjectsByMemberReader struct {
	projects []repository.ProjectWithRole
}

func (f *fakeProjectsByMemberReader) ProjectsByMember(_ context.Context, _ uuid.UUID) ([]repository.ProjectWithRole, error) {
	return f.projects, nil
}

// ---- fakeConversationsByProjectReader ------------------------------------

type fakeConversationsByProjectReader struct {
	conversations []model.Conversation
	// projectNames optionally names each project, for tests asserting the
	// resolved project name is carried through. Unset projects map to "".
	projectNames map[uuid.UUID]string
}

func (f *fakeConversationsByProjectReader) ConversationsByProjectIDs(
	_ context.Context,
	_ uuid.UUID,
	projectIDs []uuid.UUID,
	status string,
	callerMemberID uuid.UUID,
	includeAll bool,
) ([]ConversationWithProject, error) {
	var out []ConversationWithProject

	for _, c := range f.conversations {
		// Empty projectIDs is the org-wide fallback: every project in scope.
		if len(projectIDs) > 0 && !slices.Contains(projectIDs, c.ProjectID) {
			continue
		}

		if status != "" && string(c.Status) != status {
			continue
		}

		// Mirror the store's visibility predicate: unless the caller is an org
		// admin (includeAll), only their asked/assigned conversations are visible.
		if !includeAll && callerMemberID != c.AskerMemberID && callerMemberID != c.ResponderMemberID {
			continue
		}

		out = append(out, ConversationWithProject{
			ConversationRecord: convsToRecords([]model.Conversation{c})[0],
			ProjectName:        f.projectNames[c.ProjectID],
		})
	}

	return out, nil
}

// ---- fakeProjectsDetailedReader ---------------------------------------------

type fakeProjectsDetailedReader struct {
	rows []ProjectDetailRow
}

func (f *fakeProjectsDetailedReader) ProjectsDetailedByOrg(_ context.Context, _ uuid.UUID, includeArchived bool) ([]ProjectDetailRow, error) {
	if includeArchived {
		return f.rows, nil
	}

	out := make([]ProjectDetailRow, 0, len(f.rows))

	for _, r := range f.rows {
		if !r.Archived {
			out = append(out, r)
		}
	}

	return out, nil
}

// ---- fakeAlertRoutingByProject -----------------------------------------

type fakeAlertRoutingByProject struct {
	byProject map[uuid.UUID][]service.ProviderAlertChannel
}

func newFakeAlertRoutingByProject() *fakeAlertRoutingByProject {
	return &fakeAlertRoutingByProject{byProject: make(map[uuid.UUID][]service.ProviderAlertChannel)}
}

func (f *fakeAlertRoutingByProject) ConfiguredProvidersWithAlertChannel(
	_ context.Context,
	projectID uuid.UUID,
) ([]service.ProviderAlertChannel, error) {
	return f.byProject[projectID], nil
}

func (f *fakeAlertRoutingByProject) ConfiguredProvidersWithAlertChannels(
	_ context.Context,
	projectIDs []uuid.UUID,
) (map[uuid.UUID][]service.ProviderAlertChannel, error) {
	out := make(map[uuid.UUID][]service.ProviderAlertChannel, len(projectIDs))
	for _, id := range projectIDs {
		out[id] = f.byProject[id]
	}

	return out, nil
}

// fakeParticipantsNames is a no-op fake for the participants/names batch
// readers: no explicitly-added participants, no resolvable names.
type fakeParticipantsNames struct{}

func (f *fakeParticipantsNames) ParticipantsByConversations(_ context.Context, _ []uuid.UUID) (map[uuid.UUID][]model.ConversationParticipant, error) {
	return map[uuid.UUID][]model.ConversationParticipant{}, nil
}

func (f *fakeParticipantsNames) MemberNamesByIDs(_ context.Context, _ []uuid.UUID) (map[uuid.UUID]string, error) {
	return map[uuid.UUID]string{}, nil
}

func (f *fakeParticipantsNames) ActiveCandidatesByConversations(_ context.Context, _ []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	return map[uuid.UUID][]uuid.UUID{}, nil
}
