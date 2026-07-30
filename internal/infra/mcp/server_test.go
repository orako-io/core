// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/infra/api"
	"github.com/orako-io/core/internal/infra/api/oauth"
	"github.com/orako-io/core/internal/pkg/errs"
)

// --- api.MCPTokenAuthenticator fakes ---
//
// Mirror internal/infra/api/mcp_token_auth_test.go's fixture shapes
// (same unexported-interface method sets), but keyed by member/account so a
// single fixture can host both a project-member identity and an org-admin
// identity — the two RBAC postures the tool set gates on.

type fakeTokenStore struct {
	tokens map[string]oauth.Token
}

func (f *fakeTokenStore) GetToken(_ context.Context, rawToken string, kind oauth.TokenKind) (oauth.Token, error) {
	tok, ok := f.tokens[rawToken]
	if !ok || tok.Kind != kind {
		return oauth.Token{}, adaptererr.ErrNotFound
	}

	return tok, nil
}

func (f *fakeTokenStore) TouchToken(_ context.Context, _ uuid.UUID) error { return nil }

type fakeMembers struct {
	byID map[uuid.UUID]model.Member
}

func (f *fakeMembers) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	m, ok := f.byID[id]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

type fakeMemberships struct {
	byMember map[uuid.UUID][]repository.ProjectWithRole
}

func (f *fakeMemberships) ProjectsByMember(_ context.Context, memberID uuid.UUID) ([]repository.ProjectWithRole, error) {
	return f.byMember[memberID], nil
}

type fakeOrgAdmin struct {
	admins map[uuid.UUID]bool // by accountID
}

func (f *fakeOrgAdmin) RoleFor(_ context.Context, _, accountID uuid.UUID) (model.OrgRole, error) {
	if f.admins[accountID] {
		return model.OrgRoleAdmin, nil
	}

	return model.OrgRoleMember, nil
}

// --- Application-layer fakes ---
//
// One per narrow interface in tools.go. Each records its last input and
// returns a preset result, so tests can assert both the RBAC gate (was the
// handler reached at all) and the parity mapping (did the right fields
// arrive from the tool input / go back out in the tool output).

type fakeSearchHistory struct {
	got  query.SearchHistoryQuery
	hits []query.HistoryHit
}

func (f *fakeSearchHistory) Handle(_ context.Context, q query.SearchHistoryQuery) ([]query.HistoryHit, error) {
	f.got = q

	return f.hits, nil
}

type fakeVocabulary struct {
	got   query.VocabularyQuery
	vocab query.Vocabulary
}

func (f *fakeVocabulary) Handle(_ context.Context, q query.VocabularyQuery) (query.Vocabulary, error) {
	f.got = q

	return f.vocab, nil
}

type fakeListExperts struct{ got query.ListExpertsQuery }

func (f *fakeListExperts) Handle(_ context.Context, q query.ListExpertsQuery) ([]query.Expert, error) {
	f.got = q

	return []query.Expert{{MemberID: uuid.New(), DisplayName: "Ada", Domains: []string{"Backend"}, Online: true}}, nil
}

type fakeAsk struct {
	got    command.AskCommand
	result command.AskResult
}

func (f *fakeAsk) Handle(_ context.Context, cmd command.AskCommand) (command.AskResult, error) {
	f.got = cmd

	return f.result, nil
}

type fakeGetConversation struct{ got query.GetConversationQuery }

func (f *fakeGetConversation) Handle(_ context.Context, q query.GetConversationQuery) (query.ConversationView, error) {
	f.got = q

	return query.ConversationView{
		ID:     q.ConversationID,
		Status: model.ConversationStatusOpen,
		Messages: []query.MessageView{
			{ID: uuid.New(), AuthorMemberID: q.CallerMemberID, Role: model.MessageRoleQuestion, Body: "hi"},
		},
		Participants: []query.Participant{{
			MemberID: q.CallerMemberID, DisplayName: "Ada", Role: "asker",
		}},
	}, nil
}

type fakeUploadAttachment struct {
	got command.UploadAttachmentCommand
	id  uuid.UUID
}

func (f *fakeUploadAttachment) Handle(_ context.Context, cmd command.UploadAttachmentCommand) (command.UploadAttachmentResult, error) {
	f.got = cmd

	return command.UploadAttachmentResult{AttachmentID: f.id}, nil
}

type fakeFollowUp struct{ got command.FollowUpCommand }

func (f *fakeFollowUp) Handle(_ context.Context, cmd command.FollowUpCommand) (command.FollowUpResult, error) {
	f.got = cmd

	return command.FollowUpResult{}, nil
}

type fakeAddParticipant struct{ got command.AddParticipantCommand }

func (f *fakeAddParticipant) Handle(_ context.Context, cmd command.AddParticipantCommand) (command.AddParticipantResult, error) {
	f.got = cmd

	return command.AddParticipantResult{}, nil
}

type fakeAddKnowledge struct {
	got   command.CreateKnowledgeEntryCommand
	calls int
}

func (f *fakeAddKnowledge) Handle(_ context.Context, cmd command.CreateKnowledgeEntryCommand) (command.CreateKnowledgeEntryResult, error) {
	f.got = cmd
	f.calls++

	return command.CreateKnowledgeEntryResult{EntryID: uuid.New()}, nil
}

type fakeSuggestKnowledge struct {
	got   command.SuggestKnowledgeEntryCommand
	calls int
}

func (f *fakeSuggestKnowledge) Handle(_ context.Context, cmd command.SuggestKnowledgeEntryCommand) (command.SuggestKnowledgeEntryResult, error) {
	f.got = cmd
	f.calls++

	return command.SuggestKnowledgeEntryResult{EntryID: uuid.New()}, nil
}

type fakeResolveConversation struct {
	got command.ResolveConversationCommand
}

func (f *fakeResolveConversation) Handle(_ context.Context, cmd command.ResolveConversationCommand) (command.ResolveConversationResult, error) {
	f.got = cmd

	return command.ResolveConversationResult{}, nil
}

type fakeListProjects struct {
	got       query.ListProjectsQuery
	projectID uuid.UUID // the roster the tool returns; must be in the caller's scope
}

func (f *fakeListProjects) Handle(_ context.Context, q query.ListProjectsQuery) ([]query.ProjectSummary, error) {
	f.got = q

	return []query.ProjectSummary{{ID: f.projectID, Name: "orako", Role: model.RoleDev}}, nil
}

type fakeListConversations struct{ got query.ListConversationsQuery }

func (f *fakeListConversations) Handle(_ context.Context, q query.ListConversationsQuery) ([]query.ConversationSummary, error) {
	f.got = q

	return nil, nil
}

// --- Test fixture ---

const testResourceURL = "https://orako.example.com/mcp"

const testPRMURL = "https://orako.example.com/.well-known/oauth-protected-resource"

// deps bundles every fake so tests can reach into individual call records.
type deps struct {
	searchHistory       *fakeSearchHistory
	historyVocabulary   *fakeVocabulary
	listExperts         *fakeListExperts
	ask                 *fakeAsk
	getConversation     *fakeGetConversation
	followUp            *fakeFollowUp
	uploadAttachment    *fakeUploadAttachment
	resolveConversation *fakeResolveConversation
	listProjects        *fakeListProjects
	listConversations   *fakeListConversations
	addParticipant      *fakeAddParticipant
	addKnowledge        *fakeAddKnowledge
	suggestKnowledge    *fakeSuggestKnowledge
}

// testFixture wires a Server plus its httptest.Server, two mcp_at_ raw
// tokens (one project-member, one org-admin — same project) and the fakes
// backing every tool.
type testFixture struct {
	srv          *httptest.Server
	deps         deps
	memberToken  string // non-admin project member
	adminToken   string // org admin, same project
	projectID    uuid.UUID
	otherProject uuid.UUID // a project the tokens are NOT members of
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()

	memberID := uuid.New()
	adminMemberID := uuid.New()
	accountID := uuid.New()
	adminAccountID := uuid.New()
	projectID := uuid.New()
	orgID := uuid.New()

	memberToken := "mcp_at_" + uuid.NewString()
	adminToken := "mcp_at_" + uuid.NewString()

	tokens := &fakeTokenStore{
		tokens: map[string]oauth.Token{
			memberToken: {
				ID: uuid.New(), MemberID: memberID, ClientID: "test",
				Resource: testResourceURL, Kind: oauth.TokenKindAccess,
				GrantID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour),
			},
			adminToken: {
				ID: uuid.New(), MemberID: adminMemberID, ClientID: "test",
				Resource: testResourceURL, Kind: oauth.TokenKindAccess,
				GrantID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour),
			},
		},
	}

	members := &fakeMembers{byID: map[uuid.UUID]model.Member{
		memberID:      {ID: memberID, AccountID: accountID, Status: model.MemberStatusActive},
		adminMemberID: {ID: adminMemberID, AccountID: adminAccountID, Status: model.MemberStatusActive},
	}}

	memberships := &fakeMemberships{byMember: map[uuid.UUID][]repository.ProjectWithRole{
		memberID:      {{ID: projectID, OrgID: orgID}},
		adminMemberID: {{ID: projectID, OrgID: orgID}},
	}}

	orgAdmin := &fakeOrgAdmin{admins: map[uuid.UUID]bool{adminAccountID: true}}

	mcpAuth := api.NewMCPTokenAuthenticator(
		tokens, members, memberships, orgAdmin, testResourceURL, RejectingAuthenticator{},
		slog.New(slog.DiscardHandler),
	)

	d := deps{
		searchHistory:       &fakeSearchHistory{},
		historyVocabulary:   &fakeVocabulary{},
		listExperts:         &fakeListExperts{},
		ask:                 &fakeAsk{},
		getConversation:     &fakeGetConversation{},
		followUp:            &fakeFollowUp{},
		resolveConversation: &fakeResolveConversation{},
		listProjects:        &fakeListProjects{projectID: projectID},
		listConversations:   &fakeListConversations{},
		addParticipant:      &fakeAddParticipant{},
		uploadAttachment:    &fakeUploadAttachment{id: uuid.New()},
		addKnowledge:        &fakeAddKnowledge{},
		suggestKnowledge:    &fakeSuggestKnowledge{},
	}

	srv := NewServer(
		d.searchHistory, d.historyVocabulary, d.listExperts, d.ask, d.getConversation, d.followUp,
		d.uploadAttachment, d.resolveConversation,
		d.listProjects, d.listConversations, d.addParticipant, d.addKnowledge, d.suggestKnowledge,
		25<<20, mcpAuth, testPRMURL,
		slog.New(slog.DiscardHandler),
	)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &testFixture{
		srv: ts, deps: d, memberToken: memberToken, adminToken: adminToken,
		projectID: projectID, otherProject: uuid.New(),
	}
}

// bearerRoundTripper injects a fixed Authorization header on every request —
// standing in for a real MCP client's stored token.
type bearerRoundTripper struct{ token string }

func (rt bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if rt.token != "" {
		req.Header.Set("Authorization", "Bearer "+rt.token)
	}

	return http.DefaultTransport.RoundTrip(req)
}

// connectMCP dials the fixture's httptest.Server as a real MCP client
// authenticated with token (empty = unauthenticated), and returns the live
// session plus a closer.
func connectMCP(t *testing.T, tf *testFixture, token string) *sdkmcp.ClientSession {
	t.Helper()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)

	transport := &sdkmcp.StreamableClientTransport{
		Endpoint:   tf.srv.URL,
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token}},
	}

	cs, err := client.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

// --- Tests ---

// TestUnauthenticatedRequestRejected proves task 1's core acceptance
// criterion: no Authorization header at all gets 401 with a WWW-Authenticate
// challenge pointing at the PRM URL, which is what makes an OAuth-capable
// client kick off discovery (phase 2's PRM/AS).
func TestUnauthenticatedRequestRejected(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, tf.srv.URL,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	challenge := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(challenge, "Bearer") || !strings.Contains(challenge, testPRMURL) {
		t.Fatalf("WWW-Authenticate = %q, want a Bearer challenge naming resource_metadata=%q", challenge, testPRMURL)
	}
}

// TestRawJWTRejectedWithRejectingFallback proves the confused-deputy guard
// end to end over HTTP: a bearer that is not an mcp_at_ secret (a raw
// Supabase-shaped JWT, standing in for a valid dashboard session token) must
// never authenticate at /mcp, because MCPTokenAuthenticator here is wired
// with RejectingAuthenticator, not the dev/jwt chain.
func TestRawJWTRejectedWithRejectingFallback(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, tf.srv.URL,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJ1c2VyXzEifQ.sig")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a raw JWT (not an mcp_at_ token)", resp.StatusCode)
	}
}

// TestValidTokenDiscoversInstructionsAndTools proves a valid mcp_at_ token
// negotiates the current protocol through server/discover and receives the
// real workflow guidance and tool catalog.
func TestValidTokenDiscoversInstructionsAndTools(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)

	cs := connectMCP(t, tf, tf.memberToken)

	result := cs.InitializeResult()
	if result == nil {
		t.Fatal("InitializeResult is nil")
	}

	if result.ProtocolVersion != "2026-07-28" {
		t.Errorf("protocol version = %q, want 2026-07-28", result.ProtocolVersion)
	}

	if !strings.Contains(result.Instructions, "search_history FIRST") {
		t.Errorf("instructions do not mention the search-first rule: %q", result.Instructions)
	}

	if !strings.Contains(result.Instructions, "add_participant") {
		t.Errorf("instructions do not describe the status-based branching (add_participant): %q", result.Instructions)
	}

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}

	for _, want := range []string{
		toolSearchHistory, toolAsk, toolGetConversation, toolFollowUp,
		toolResolveConversation, toolListExperts, toolListProjects,
	} {
		if !names[want] {
			t.Errorf("tool %q not registered; got %v", want, names)
		}
	}
}

func TestGetConversationReturnsReadableAuthor(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)
	cs := connectMCP(t, tf, tf.memberToken)

	var out GetConversationOutput
	callTool(t, cs, toolGetConversation, map[string]any{
		"conversation_id": uuid.New().String(),
	}, &out)

	if len(out.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(out.Messages))
	}

	want := "Ada (" + tf.deps.getConversation.got.CallerMemberID.String() + ")"
	if out.Messages[0].Author != want {
		t.Fatalf("author = %q, want %q", out.Messages[0].Author, want)
	}
}

// TestLegacyInitializeRemainsSupported proves stateless transport still
// accepts the previous initialize handshake used by older MCP clients.
func TestLegacyInitializeRemainsSupported(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"legacy-client","version":"0.0.0"}}}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, tf.srv.URL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tf.memberToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading initialize response: %v", err)
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		for line := range strings.SplitSeq(string(payload), "\n") {
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				payload = []byte(data)

				break
			}
		}
	}

	var rpcResponse struct {
		Result struct {
			Instructions    string `json:"instructions"`
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &rpcResponse); err != nil {
		t.Fatalf("decoding initialize response: %v", err)
	}

	if rpcResponse.Result.ProtocolVersion != "2025-11-25" {
		t.Errorf("protocol version = %q, want 2025-11-25", rpcResponse.Result.ProtocolVersion)
	}

	if !strings.Contains(rpcResponse.Result.Instructions, "search_history FIRST") {
		t.Errorf("legacy instructions do not mention the search-first rule: %q", rpcResponse.Result.Instructions)
	}
}

// callTool calls name over cs with args and decodes StructuredContent into out.
func callTool(t *testing.T, cs *sdkmcp.ClientSession, name string, args map[string]any, out any) *sdkmcp.CallToolResult {
	t.Helper()

	result, err := cs.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}

	if out != nil && !result.IsError {
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured content: %v", err)
		}

		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("unmarshal structured content into %T: %v", out, err)
		}
	}

	return result
}

// TestSearchHistoryParityAndRBAC proves tool parity (the HTTP call reaches
// the app layer with the right project_id/query and returns its hits
// unmodified) and project-scoped RBAC (a caller cannot search a project they
// are not a member of).
func TestSearchHistoryParityAndRBAC(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)
	tf.deps.searchHistory.hits = []query.HistoryHit{
		{
			ConversationID: uuid.New(),
			Title:          "how do I deploy?",
			Summary:        "run make deploy",
			Status:         model.ConversationStatusResolved,
			AskerMemberID:  uuid.New(),
		},
	}

	cs := connectMCP(t, tf, tf.memberToken)

	var out SearchHistoryOutput

	callTool(t, cs, toolSearchHistory, map[string]any{
		"project_id": tf.projectID.String(),
		"query":      "how do I deploy?",
	}, &out)

	if len(tf.deps.searchHistory.got.ProjectIDs) != 1 || tf.deps.searchHistory.got.ProjectIDs[0] != tf.projectID {
		t.Errorf("app layer received project_ids = %v, want [%s]", tf.deps.searchHistory.got.ProjectIDs, tf.projectID)
	}

	if tf.deps.searchHistory.got.Query != "how do I deploy?" {
		t.Errorf("app layer received query = %q", tf.deps.searchHistory.got.Query)
	}

	if len(out.Hits) != 1 || out.Hits[0].Summary != "run make deploy" || out.Hits[0].Status != "resolved" {
		t.Errorf("tool output did not carry the app layer's hit through unmodified: %+v", out)
	}

	// RBAC: the same token, a project it is not a member of.
	result := callTool(t, cs, toolSearchHistory, map[string]any{
		"project_id": tf.otherProject.String(),
		"query":      "anything",
	}, nil)

	if !result.IsError {
		t.Fatal("search_history against a foreign project should fail RBAC, got success")
	}
}

// TestSearchHistoryDefaultsToPrimaryProject proves an omitted project_id
// resolves to the token's primary project — the scope-1 ergonomics where the
// agent never has to pass (or discover) a project_id.
func TestSearchHistoryDefaultsToPrimaryProject(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)
	cs := connectMCP(t, tf, tf.memberToken)

	var out SearchHistoryOutput

	// No project_id: the fixture's memberToken is scoped to a single project,
	// so the server resolves it to that primary.
	callTool(t, cs, toolSearchHistory, map[string]any{"query": "pgx pool"}, &out)

	if len(tf.deps.searchHistory.got.ProjectIDs) != 1 || tf.deps.searchHistory.got.ProjectIDs[0] != tf.projectID {
		t.Errorf("omitted project_id resolved to %v, want the primary [%s]",
			tf.deps.searchHistory.got.ProjectIDs, tf.projectID)
	}
}

// TestPhase2_TagsAndConversationID proves the capture-side wiring: resolve_conversation
// carries agent-supplied tags to the app layer, and a search hit surfaces its tags and
// conversation_id (so the agent can follow a contested hit to the source thread).
func TestPhase2_TagsAndConversationID(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)
	convID := uuid.New()
	tf.deps.searchHistory.hits = []query.HistoryHit{
		{
			ConversationID: convID,
			Title:          "webhook?",
			Summary:        "set the secret",
			Status:         model.ConversationStatusResolved,
			Tags:           []string{"deploy", "webhook"},
			AskerMemberID:  uuid.New(),
		},
	}

	cs := connectMCP(t, tf, tf.memberToken)

	// search surfaces tags + conversation_id unmodified.
	var out SearchHistoryOutput

	callTool(t, cs, toolSearchHistory, map[string]any{
		"project_id": tf.projectID.String(),
		"query":      "webhook",
	}, &out)

	if len(out.Hits) != 1 {
		t.Fatalf("want 1 hit, got %d", len(out.Hits))
	}

	if out.Hits[0].ConversationID != convID.String() {
		t.Errorf("hit conversation_id = %q, want %q", out.Hits[0].ConversationID, convID)
	}

	if len(out.Hits[0].Tags) != 2 || out.Hits[0].Tags[0] != "deploy" {
		t.Errorf("hit tags = %v, want [deploy webhook]", out.Hits[0].Tags)
	}

	// resolve_conversation carries tags to the app layer. summary + tags are
	// REQUIRED by the tool schema (P2b); contested/contested_set stay optional.
	res := callTool(t, cs, toolResolveConversation, map[string]any{
		"conversation_id": uuid.New().String(),
		"resolution":      "done",
		"summary":         "webhook secret configured",
		"tags":            []string{"billing", "webhook"},
		"contested":       false,
		"contested_set":   false,
	}, nil)
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("resolve_conversation returned error: %s", raw)
	}

	got := tf.deps.resolveConversation.got.Tags
	if len(got) != 2 || got[0] != "billing" || got[1] != "webhook" {
		t.Errorf("app layer received tags = %v, want [billing webhook]", got)
	}
}

// TestListProjectsUsesCallerIdentity proves an identity-scoped (not
// project_id-scoped) tool is called with the resolved CallerIdentity's
// MemberID, never a client-supplied value — there is no such field in the
// tool's input schema in the first place.
func TestListProjectsUsesCallerIdentity(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)

	cs := connectMCP(t, tf, tf.memberToken)

	var out ListProjectsOutput

	callTool(t, cs, toolListProjects, map[string]any{}, &out)

	if tf.deps.listProjects.got.CallerMemberID == uuid.Nil {
		t.Error("list_projects was not called with a resolved caller MemberID")
	}

	if len(out.Projects) != 1 || out.Projects[0].Name != "orako" {
		t.Errorf("unexpected list_projects output: %+v", out)
	}
}

// TestAskOneOfValidation proves the one-of validation (exactly one
// of member_id or domains) is enforced before any app-layer call —
// mirroring the CLI tool's local validation.
func TestAskOneOfValidation(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)
	cs := connectMCP(t, tf, tf.memberToken)

	result := callTool(t, cs, toolAsk, map[string]any{
		"project_id": tf.projectID.String(),
		"question":   "why?",
		"context":    "because",
		"summary":    "why does it fail",
		"tags":       []string{"debugging"},
		// neither member_id nor domains set
	}, nil)

	if !result.IsError {
		t.Fatal("ask without member_id or domains should fail validation")
	}
}

func TestAskRecordsCurrentClientIdentity(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)
	cs := connectMCP(t, tf, tf.memberToken)

	callTool(t, cs, toolAsk, map[string]any{
		"project_id": tf.projectID.String(),
		"member_id":  uuid.New().String(),
		"question":   "why?",
		"context":    "because",
		"summary":    "why does it fail",
		"tags":       []string{"debugging"},
	}, nil)

	if tf.deps.ask.got.AgentClient != "test-client" {
		t.Errorf("agent client = %q, want test-client", tf.deps.ask.got.AgentClient)
	}
}

// errsSentinelSurfacesGuidance is a compile-time reminder that
// actionableError must be exercised against the errs sentinels the
// application layer actually returns; TestActionableErrorGuidance below
// covers it directly at the unit level (no MCP roundtrip needed).
var _ = errs.NotFoundError{}

func TestActionableErrorGuidance(t *testing.T) {
	t.Parallel()

	err := actionableError(errs.NotFoundError{Resource: "conversation"}, toolGetConversation)
	if !strings.Contains(err.Error(), "list_conversations") {
		t.Errorf("not-found guidance missing list_conversations hint: %v", err)
	}

	err = actionableError(errs.ForbiddenError{Action: "view conversation"}, toolGetConversation)
	if !strings.Contains(err.Error(), "add you") {
		t.Errorf("forbidden guidance missing participant hint: %v", err)
	}

	err = actionableError(errs.InternalError{Err: errors.New("postgres password leaked")}, toolGetConversation)
	if strings.Contains(err.Error(), "postgres") {
		t.Errorf("internal error leaked its cause: %v", err)
	}
	if err.Error() != "an unexpected error occurred" {
		t.Errorf("internal error = %q, want caller-safe detail", err)
	}

	if actionableError(nil, toolGetConversation) != nil {
		t.Error("actionableError(nil) must return nil")
	}
}
