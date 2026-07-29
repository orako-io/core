// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/service"
)

// ---- fakeConversationRepository ------------------------------------------

type fakeConversationRepository struct {
	conversations map[uuid.UUID]model.Conversation
	messages      map[uuid.UUID][]model.Message

	createErr     error
	byIDErr       error
	updateErr     error
	addMsgErr     error
	deleteConvErr error

	deletedConvs map[uuid.UUID]bool `exhaustruct:"optional"`

	// pendingAnswerBody, when non-empty, causes MessagesByConversation to
	// return a synthetic ANSWER-role message for any conversation ID. This
	// allows wait-poll tests to seed an inline answer without knowing the
	// conversation ID in advance.
	pendingAnswerBody string
}

func newFakeConvRepo() *fakeConversationRepository {
	return &fakeConversationRepository{
		conversations: make(map[uuid.UUID]model.Conversation),
		messages:      make(map[uuid.UUID][]model.Message),
	}
}

func (f *fakeConversationRepository) CreateConversation(_ context.Context, conv model.Conversation) error {
	if f.createErr != nil {
		return f.createErr
	}

	if _, exists := f.conversations[conv.ID]; exists {
		return adaptererr.ErrDuplicate
	}

	f.conversations[conv.ID] = conv

	return nil
}

func (f *fakeConversationRepository) ConversationByID(_ context.Context, id uuid.UUID) (model.Conversation, error) {
	if f.byIDErr != nil {
		return model.Conversation{}, f.byIDErr
	}

	c, ok := f.conversations[id]
	if !ok {
		return model.Conversation{}, adaptererr.ErrNotFound
	}

	return c, nil
}

func (f *fakeConversationRepository) ConversationBySlackThread(_ context.Context, _, _ string) (model.Conversation, error) {
	return model.Conversation{}, adaptererr.ErrNotFound
}

func (f *fakeConversationRepository) ConversationByTelegramMessage(_ context.Context, _, _ string) (model.Conversation, error) {
	return model.Conversation{}, adaptererr.ErrNotFound
}

func (f *fakeConversationRepository) DeleteConversation(_ context.Context, id uuid.UUID) error {
	if f.deleteConvErr != nil {
		return f.deleteConvErr
	}

	if _, ok := f.conversations[id]; !ok {
		return adaptererr.ErrNotFound
	}

	delete(f.conversations, id)

	if f.deletedConvs == nil {
		f.deletedConvs = make(map[uuid.UUID]bool)
	}

	f.deletedConvs[id] = true

	return nil
}

func (f *fakeConversationRepository) UpdateStatus(_ context.Context, id uuid.UUID, status model.ConversationStatus) error {
	if f.updateErr != nil {
		return f.updateErr
	}

	c, ok := f.conversations[id]
	if !ok {
		return adaptererr.ErrNotFound
	}

	c.Status = status
	f.conversations[id] = c

	return nil
}

func (f *fakeConversationRepository) UpdateMetadata(_ context.Context, id uuid.UUID, summary string, tags, entities []string) (model.Conversation, error) {
	if f.updateErr != nil {
		return model.Conversation{}, f.updateErr
	}

	c, ok := f.conversations[id]
	if !ok {
		return model.Conversation{}, adaptererr.ErrNotFound
	}

	c.Summary = summary
	c.Tags = tags
	c.Entities = entities
	f.conversations[id] = c

	return c, nil
}

func (f *fakeConversationRepository) AddMessage(_ context.Context, msg model.Message) error {
	if f.addMsgErr != nil {
		return f.addMsgErr
	}

	f.messages[msg.ConversationID] = append(f.messages[msg.ConversationID], msg)

	return nil
}

func (f *fakeConversationRepository) MessagesByConversation(_ context.Context, conversationID uuid.UUID) ([]model.Message, error) {
	if f.pendingAnswerBody != "" {
		return []model.Message{{
			ConversationID: conversationID,
			Role:           model.MessageRoleAnswer,
			Body:           f.pendingAnswerBody,
		}}, nil
	}

	return f.messages[conversationID], nil
}

// ---- fakeProjectRepository -----------------------------------------------

type fakeProjectRepository struct {
	projects    map[uuid.UUID]model.Project
	memberships []repository.ProjectMembership
	deleted     map[uuid.UUID]bool

	createErr      error
	addMemberErr   error
	setDomainsErr  error
	renameErr      error
	setArchivedErr error
	deleteErr      error
}

func newFakeProjectRepo() *fakeProjectRepository {
	return &fakeProjectRepository{
		projects: make(map[uuid.UUID]model.Project),
		deleted:  make(map[uuid.UUID]bool),
	}
}

func (f *fakeProjectRepository) Create(_ context.Context, project model.Project) error {
	if f.createErr != nil {
		return f.createErr
	}

	if _, exists := f.projects[project.ID]; exists {
		return adaptererr.ErrDuplicate
	}

	f.projects[project.ID] = project

	return nil
}

func (f *fakeProjectRepository) ByID(_ context.Context, id uuid.UUID) (model.Project, error) {
	p, ok := f.projects[id]
	if !ok {
		return model.Project{}, adaptererr.ErrNotFound
	}

	return p, nil
}

func (f *fakeProjectRepository) AddMember(_ context.Context, m repository.ProjectMembership) error {
	if f.addMemberErr != nil {
		return f.addMemberErr
	}

	f.memberships = append(f.memberships, m)

	return nil
}

func (f *fakeProjectRepository) SetMemberDomains(_ context.Context, projectID, memberID uuid.UUID, domains []string) error {
	if f.setDomainsErr != nil {
		return f.setDomainsErr
	}

	for i, m := range f.memberships {
		if m.ProjectID == projectID && m.MemberID == memberID {
			f.memberships[i].Domains = domains
			return nil
		}
	}

	return adaptererr.ErrNotFound
}

func (f *fakeProjectRepository) SetDomainsForMemberInOrg(
	_ context.Context,
	_ uuid.UUID,
	memberID uuid.UUID,
	domains []string,
) error {
	if f.setDomainsErr != nil {
		return f.setDomainsErr
	}

	for i, m := range f.memberships {
		if m.MemberID == memberID {
			f.memberships[i].Domains = domains
		}
	}

	return nil
}

func (f *fakeProjectRepository) MembersByProject(_ context.Context, projectID uuid.UUID) ([]repository.ProjectMembership, error) {
	var out []repository.ProjectMembership

	for _, m := range f.memberships {
		if m.ProjectID == projectID {
			out = append(out, m)
		}
	}

	return out, nil
}

func (f *fakeProjectRepository) Rename(_ context.Context, id uuid.UUID, name string) error {
	if f.renameErr != nil {
		return f.renameErr
	}

	p, ok := f.projects[id]
	if !ok {
		return adaptererr.ErrNotFound
	}

	p.Name = name
	f.projects[id] = p

	return nil
}

func (f *fakeProjectRepository) SetArchived(_ context.Context, id uuid.UUID, archived bool) error {
	if f.setArchivedErr != nil {
		return f.setArchivedErr
	}

	p, ok := f.projects[id]
	if !ok {
		return adaptererr.ErrNotFound
	}

	if archived {
		now := time.Now().UTC()
		p.ArchivedAt = &now
	} else {
		p.ArchivedAt = nil
	}

	f.projects[id] = p

	return nil
}

func (f *fakeProjectRepository) Delete(_ context.Context, id uuid.UUID) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}

	if _, ok := f.projects[id]; !ok {
		return adaptererr.ErrNotFound
	}

	delete(f.projects, id)
	f.deleted[id] = true

	return nil
}

func (f *fakeProjectRepository) RoleOf(_ context.Context, projectID, memberID uuid.UUID) (model.Role, error) {
	for _, m := range f.memberships {
		if m.ProjectID == projectID && m.MemberID == memberID {
			return m.Role, nil
		}
	}

	return model.RoleUnspecified, adaptererr.ErrNotFound
}

// ---- fakeMemberRepository ------------------------------------------------

type fakeMemberRepository struct {
	members map[uuid.UUID]model.Member
	byEmail map[string]model.Member

	createErr   error
	byIDErr     error
	byEmailErr  error
	updateErr   error
	offboardErr error
}

func newFakeMemberRepo() *fakeMemberRepository {
	return &fakeMemberRepository{
		members: make(map[uuid.UUID]model.Member),
		byEmail: make(map[string]model.Member),
	}
}

func (f *fakeMemberRepository) Create(_ context.Context, member model.Member) error {
	if f.createErr != nil {
		return f.createErr
	}

	if _, exists := f.members[member.ID]; exists {
		return adaptererr.ErrDuplicate
	}

	f.members[member.ID] = member
	f.byEmail[member.Email] = member

	return nil
}

func (f *fakeMemberRepository) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	if f.byIDErr != nil {
		return model.Member{}, f.byIDErr
	}

	m, ok := f.members[id]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

func (f *fakeMemberRepository) ByEmail(_ context.Context, email string) (model.Member, error) {
	if f.byEmailErr != nil {
		return model.Member{}, f.byEmailErr
	}

	m, ok := f.byEmail[email]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

func (f *fakeMemberRepository) Update(_ context.Context, member model.Member) error {
	if f.updateErr != nil {
		return f.updateErr
	}

	if _, ok := f.members[member.ID]; !ok {
		return adaptererr.ErrNotFound
	}

	f.members[member.ID] = member
	f.byEmail[member.Email] = member

	return nil
}

// OffboardFromOrg mirrors the terminal path of the store for command tests.
func (f *fakeMemberRepository) OffboardFromOrg(_ context.Context, member model.Member, _ uuid.UUID) error {
	if f.offboardErr != nil {
		return f.offboardErr
	}

	if _, ok := f.members[member.ID]; !ok {
		return adaptererr.ErrNotFound
	}

	member.SlackUserID = ""
	member.TelegramChatID = ""
	member.TeamsUserID = ""
	member.DiscordUserID = ""
	member.BindingError = ""
	member.DeliveryChannel = model.DeliveryChannelDashboard

	f.members[member.ID] = member
	f.byEmail[member.Email] = member

	return nil
}

// ---- fakePresenceRepository ----------------------------------------------

type fakePresenceRepository struct {
	records   map[uuid.UUID]model.Presence
	upsertErr error
}

func newFakePresenceRepo() *fakePresenceRepository {
	return &fakePresenceRepository{
		records: make(map[uuid.UUID]model.Presence),
	}
}

func (f *fakePresenceRepository) Upsert(_ context.Context, p model.Presence) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}

	f.records[p.MemberID] = p

	return nil
}

func (f *fakePresenceRepository) ByMember(_ context.Context, memberID uuid.UUID) (model.Presence, error) {
	p, ok := f.records[memberID]
	if !ok {
		return model.Presence{}, adaptererr.ErrNotFound
	}

	return p, nil
}

// ---- fakeEventBus --------------------------------------------------------

type fakeEventBus struct {
	published  []*orakov1.Envelope
	publishErr error
}

func (f *fakeEventBus) Publish(_ context.Context, env *orakov1.Envelope) (*orakov1.Envelope, error) {
	if f.publishErr != nil {
		return nil, f.publishErr
	}

	f.published = append(f.published, env)

	return env, nil
}

func (f *fakeEventBus) lastOfType(t orakov1.EventType) (*orakov1.Envelope, bool) {
	for _, env := range slices.Backward(f.published) {
		if env.Type == t {
			return env, true
		}
	}

	return nil, false
}

func (f *fakeEventBus) countOfType(t orakov1.EventType) int {
	n := 0

	for _, env := range f.published {
		if env.Type == t {
			n++
		}
	}

	return n
}

// ---- fakeProvider --------------------------------------------------------

// noopProvider is a noop Provider for command unit tests.
type noopProvider struct {
	deliverErr error
}

func (p *noopProvider) Deliver(_ context.Context, _ service.OutboundMessage) (service.MessageRef, error) {
	if p.deliverErr != nil {
		return service.MessageRef{}, p.deliverErr
	}

	// Non-zero so a caller that records this ref (e.g. the direct-ask ledger
	// write) has something meaningful to assert on.
	return service.MessageRef{ChannelID: "dm-1", MessageID: "msg-1"}, nil
}

func (p *noopProvider) ParseInbound(_ context.Context, _ []byte) (service.InboundMessage, error) {
	return service.InboundMessage{}, errors.New("noopProvider: ParseInbound not implemented")
}

// ---- alwaysDashboardMembers ------------------------------------------------

// alwaysDashboardMembers is a memberBindingReader stand-in that resolves any
// id to a dashboard-channel, active member, for tests that don't care about
// channel routing or member status.
type alwaysDashboardMembers struct{}

func (alwaysDashboardMembers) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	return model.Member{ID: id, DeliveryChannel: model.DeliveryChannelDashboard, Status: model.MemberStatusActive}, nil
}

// fakeMemberBindingReader is a configurable memberBindingReader for tests
// that DO care about channel routing.
type fakeMemberBindingReader struct {
	members map[uuid.UUID]model.Member
}

func newFakeMemberBindingReader() *fakeMemberBindingReader {
	return &fakeMemberBindingReader{members: make(map[uuid.UUID]model.Member)}
}

func (f *fakeMemberBindingReader) add(m model.Member) { f.members[m.ID] = m }

func (f *fakeMemberBindingReader) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	m, ok := f.members[id]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

// ---- fakeProviderConfigStore ---------------------------------------------

// fakeOrgCredStore records org-level credential UpsertProvider calls.
type fakeOrgCredStore struct {
	calls     []orgCredCall
	upsertErr error  `exhaustruct:"optional"`
	loaded    []byte `exhaustruct:"optional"` // existing creds LoadProvider returns
	loadErr   error  `exhaustruct:"optional"` // when set, LoadProvider returns it
}

// LoadProvider returns the seeded existing credentials, or ErrNotFound (a fresh
// connection) when none is seeded.
func (f *fakeOrgCredStore) LoadProvider(_ context.Context, _ uuid.UUID, _ string) ([]byte, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}

	if len(f.loaded) == 0 {
		return nil, adaptererr.ErrNotFound
	}

	return f.loaded, nil
}

type orgCredCall struct {
	orgID       uuid.UUID
	kind        string
	credentials []byte
}

func (f *fakeOrgCredStore) UpsertProvider(_ context.Context, orgID uuid.UUID, kind string, credentials []byte) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}

	f.calls = append(f.calls, orgCredCall{orgID, kind, credentials})

	return nil
}

// fakeProviderConfigStore records per-project alert-channel upserts.
type fakeProviderConfigStore struct {
	alertCalls []upsertCall `exhaustruct:"optional"`
	alertErr   error        `exhaustruct:"optional"`
}

type upsertCall struct {
	projectID       uuid.UUID
	kind            string
	alertChannelIDs []string
}

func (f *fakeProviderConfigStore) UpsertAlertChannels(_ context.Context, projectID uuid.UUID, kind string, alertChannelIDs []string) error {
	if f.alertErr != nil {
		return f.alertErr
	}

	f.alertCalls = append(f.alertCalls, upsertCall{projectID: projectID, kind: kind, alertChannelIDs: alertChannelIDs})

	return nil
}

// ---- fakeProviderRefresher -----------------------------------------------

// fakeProviderRefresher records RegisterFromMap calls for assertions.
type fakeProviderRefresher struct {
	calls       []registerCall
	registerErr error
}

type registerCall struct {
	projectID uuid.UUID
	kind      string
	creds     map[string]string
}

func (f *fakeProviderRefresher) RegisterFromMap(projectID uuid.UUID, kind string, creds map[string]string) error {
	if f.registerErr != nil {
		return f.registerErr
	}

	f.calls = append(f.calls, registerCall{projectID, kind, creds})

	return nil
}

// ---- fakeConversationOpener -----------------------------------------------

// fakeConversationOpener is an all-or-nothing fake for the conversationOpener
// port. When openErr is nil the conv, msg, and candidates are stored; when
// openErr is set nothing is written (simulating TX rollback).
type fakeConversationOpener struct {
	conversations map[uuid.UUID]model.Conversation
	messages      map[uuid.UUID][]model.Message
	candidates    map[uuid.UUID][]uuid.UUID
	openErr       error
}

func newFakeConversationOpener() *fakeConversationOpener {
	return &fakeConversationOpener{
		conversations: make(map[uuid.UUID]model.Conversation),
		messages:      make(map[uuid.UUID][]model.Message),
		candidates:    make(map[uuid.UUID][]uuid.UUID),
	}
}

func (f *fakeConversationOpener) OpenConversation(_ context.Context, conv model.Conversation, msg model.Message, candidates []uuid.UUID) error {
	if f.openErr != nil {
		return f.openErr // atomic fail — nothing written
	}

	f.conversations[conv.ID] = conv
	f.messages[conv.ID] = append(f.messages[conv.ID], msg)
	f.candidates[conv.ID] = candidates

	return nil
}

// ---- fakeCandidatePool ------------------------------------------------------

// fakeCandidatePool implements candidatePoolResolver: it returns the canned
// member set for any domains query.
type fakeCandidatePool struct {
	members []uuid.UUID
	err     error
}

func (f *fakeCandidatePool) MembersByDomains(_ context.Context, _ uuid.UUID, _ []string, _ uuid.UUID) ([]uuid.UUID, error) {
	return f.members, f.err
}

// ---- fakeLabeler -------------------------------------------------------------

// fakeLabeler implements specialistLabeler with an in-memory CAS: the first
// label stamp on a conversation wins, later ones lose. Like the real store, a
// win also stamps the conversation row in the linked repo (same UPDATE). The
// label is descriptive attribution only — never an exclusivity gate.
type fakeLabeler struct {
	active map[uuid.UUID]map[uuid.UUID]bool // conversationID → memberID → active candidate
	repo   *fakeConversationRepository
	// denyClaim forces the CAS to lose, simulating a concurrent stamp that
	// landed between the caller's read and their UPDATE.
	denyClaim bool
	// candidacyErr fails IsActiveCandidate (the membership read).
	candidacyErr error
	// claimErr fails Claim (the CAS write).
	claimErr error
}

func newFakeLabeler(repo *fakeConversationRepository) *fakeLabeler {
	return &fakeLabeler{
		active: make(map[uuid.UUID]map[uuid.UUID]bool),
		repo:   repo,
	}
}

func (f *fakeLabeler) markActive(conversationID, memberID uuid.UUID) {
	if f.active[conversationID] == nil {
		f.active[conversationID] = make(map[uuid.UUID]bool)
	}

	f.active[conversationID][memberID] = true
}

func (f *fakeLabeler) IsActiveCandidate(_ context.Context, conversationID, memberID uuid.UUID) (bool, error) {
	return f.active[conversationID][memberID], f.candidacyErr
}

func (f *fakeLabeler) RecordFirstResponder(_ context.Context, conversationID, memberID uuid.UUID) (bool, error) {
	if f.claimErr != nil {
		return false, f.claimErr
	}

	conv, ok := f.repo.conversations[conversationID]
	if f.denyClaim || !ok || conv.ResponderMemberID != uuid.Nil {
		return false, nil
	}

	conv.ResponderMemberID = memberID
	f.repo.conversations[conversationID] = conv

	return true, nil
}

// ---- fakeMemberWriter -----------------------------------------------------

// fakeMemberWriter is an all-or-nothing fake for the memberWriter port.
// When writeErr is nil both the member find-or-create and the project
// membership are applied; when writeErr is set neither is written.
type fakeMemberWriter struct {
	members      map[uuid.UUID]model.Member
	byEmail      map[string]model.Member
	memberships  []repository.ProjectMembership
	writeErr     error
	duplicateAdd bool // if true, returns alreadyMember=true without error
}

func newFakeMemberWriter() *fakeMemberWriter {
	return &fakeMemberWriter{
		members: make(map[uuid.UUID]model.Member),
		byEmail: make(map[string]model.Member),
	}
}

func (f *fakeMemberWriter) FindOrCreateByEmail(_ context.Context, email string, newMember model.Member) (uuid.UUID, bool, error) {
	if f.writeErr != nil {
		return uuid.Nil, false, f.writeErr
	}

	if existing, ok := f.byEmail[email]; ok {
		return existing.ID, false, nil // existing member → no fresh invite
	}

	// Create new member → freshInvite, mirroring the real store.
	f.members[newMember.ID] = newMember
	f.byEmail[email] = newMember

	return newMember.ID, true, nil
}

func (f *fakeMemberWriter) AddToProject(_ context.Context, membership repository.ProjectMembership) (bool, error) {
	if f.duplicateAdd {
		return false, nil // membership already existed — idempotent no-op
	}

	f.memberships = append(f.memberships, membership)

	return true, nil
}

// ---- fakeOrganizationCreator ---------------------------------------------

// fakeOrganizationCreator is an all-or-nothing fake for the organizationCreator
// port. When createErr is nil all five rows are "stored" in memory; when
// createErr is set, nothing is written (simulating TX rollback).
// orgProvisionCapture records the six org-creation writes so a test asserts on a
// single source of truth; the four narrow fakes below share one via a pointer.
type orgProvisionCapture struct {
	org     *model.Organization
	project *model.Project
	member  *model.Member

	createErr error // returned by the first write, aborting the whole unit of work
}

type fakeOrgWriter struct{ c *orgProvisionCapture }

func (f fakeOrgWriter) Create(_ context.Context, org model.Organization) error {
	if f.c.createErr != nil {
		return f.c.createErr
	}

	f.c.org = &org

	return nil
}

func (f fakeOrgWriter) AddMember(_ context.Context, _ model.OrgMembership) error { return nil }

type fakeGlobalProjectWriter struct{ c *orgProvisionCapture }

func (f fakeGlobalProjectWriter) CreateInOrg(_ context.Context, p model.Project) error {
	f.c.project = &p

	return nil
}

func (f fakeGlobalProjectWriter) AddMember(_ context.Context, _ repository.ProjectMembership) error {
	return nil
}

type fakeCreatorMemberWriter struct{ c *orgProvisionCapture }

func (f fakeCreatorMemberWriter) CreateWithAccount(_ context.Context, m model.Member) error {
	f.c.member = &m

	return nil
}

// fakeOrgCreatedHook records the org-created seam firing (SaaS billing trial in
// production).
type fakeOrgCreatedHook struct {
	called bool
	orgID  uuid.UUID
}

func (f *fakeOrgCreatedHook) OnOrgCreated(_ context.Context, orgID, _ uuid.UUID) error {
	f.called = true
	f.orgID = orgID

	return nil
}

// fakeCreatorAccountReader is a configurable fake for the creatorAccountReader port.
type fakeCreatorAccountReader struct {
	account model.Account
	err     error
}

func (f *fakeCreatorAccountReader) ByID(_ context.Context, _ uuid.UUID) (model.Account, error) {
	if f.err != nil {
		return model.Account{}, f.err
	}

	return f.account, nil
}

// ---- fakeProviderLookup ---------------------------------------------------

// fakeProviderLookup is a configurable fake for the providerLookup port.
// When noProvider is true ForProject returns service.ErrNoProvider;
// otherwise it returns the injected provider (defaulting to a noopProvider).
type fakeProviderLookup struct {
	provider   service.Provider
	noProvider bool
	lookupErr  error
}

func newFakeProviderLookup(prov service.Provider) *fakeProviderLookup {
	return &fakeProviderLookup{provider: prov}
}

func (f *fakeProviderLookup) ForMember(_ context.Context, _, _ uuid.UUID) (service.Provider, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}

	if f.noProvider {
		return nil, service.ErrNoProvider
	}

	return f.provider, nil
}
