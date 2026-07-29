// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mcp is the server-hosted MCP-over-HTTP resource: the same agent
// tool set the CLI exposes over stdio (cli/internal/mcp), re-implemented here
// to call the core application layer directly — no Connect-RPC round-trip,
// no token pass-through. The resolved api.CallerIdentity (put in the
// request context by the auth middleware in server.go) scopes every call
// exactly like the Connect-RPC handlers in internal/infra/api/server.go;
// this package deliberately mirrors that file's narrow-interface-per-handler
// and RBAC pattern so the two driving adapters stay in lockstep.
package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/infra/api"
	"github.com/orako-io/core/internal/pkg/errs"
)

// Tool name constants — keep the string in one place across registration and tests.
const (
	toolSearchHistory       = "search_history"
	toolListExperts         = "list_experts"
	toolAsk                 = "ask"
	toolGetConversation     = "get_conversation"
	toolFollowUp            = "follow_up"
	toolResolveConversation = "resolve_conversation"
	toolListProjects        = "list_projects"
	toolListConversations   = "list_conversations"
	toolAddParticipant      = "add_participant"
	toolUploadAttachment    = "upload_attachment"
	toolAddKnowledge        = "add_knowledge"
	toolSuggestKnowledge    = "suggest_knowledge"
)

// Role name constants used in MCP tool arguments and responses — identical to
// cli/internal/mcp's roleName* constants.
const (
	roleNameDev         = "dev"
	roleNameSpecialist  = "specialist"
	roleNameLead        = "lead"
	roleNameAdmin       = "admin"
	roleNameUnspecified = "unspecified"
)

// --- Narrow application-layer ports ---
//
// One interface per handler this package depends on, mirroring
// internal/infra/api/server.go's pattern: fakes implement them in
// tests, and the production wiring (cmd/orako-server/main.go) passes the
// real app.Queries.* / app.Commands.* handler values, which already satisfy
// these interfaces structurally.

type searchHistorier interface {
	Handle(ctx context.Context, q query.SearchHistoryQuery) ([]query.HistoryHit, error)
}

// vocabularer reads the project's existing tag/entity vocabulary so the
// ask/resolve/follow_up tools can reinject it (agents reuse established labels
// instead of inventing synonyms).
type vocabularer interface {
	Handle(ctx context.Context, q query.VocabularyQuery) (query.Vocabulary, error)
}

type listExpertser interface {
	Handle(ctx context.Context, q query.ListExpertsQuery) ([]query.Expert, error)
}

type asker interface {
	Handle(ctx context.Context, cmd command.AskCommand) (command.AskResult, error)
}

type getConversationer interface {
	Handle(ctx context.Context, q query.GetConversationQuery) (query.ConversationView, error)
}

// uploader dispatches an attachment upload.
type uploader interface {
	Handle(ctx context.Context, cmd command.UploadAttachmentCommand) (command.UploadAttachmentResult, error)
}

type followUpper interface {
	Handle(ctx context.Context, cmd command.FollowUpCommand) (command.FollowUpResult, error)
}

type resolveConversationer interface {
	Handle(ctx context.Context, cmd command.ResolveConversationCommand) (command.ResolveConversationResult, error)
}

type projectsLister interface {
	Handle(ctx context.Context, q query.ListProjectsQuery) ([]query.ProjectSummary, error)
}

type conversationsLister interface {
	Handle(ctx context.Context, q query.ListConversationsQuery) ([]query.ConversationSummary, error)
}

type addParticipanter interface {
	Handle(ctx context.Context, cmd command.AddParticipantCommand) (command.AddParticipantResult, error)
}

// knowledgeAdder is the write port for the admin-gated add_knowledge tool: it
// authors one curated entry per call.
type knowledgeAdder interface {
	Handle(ctx context.Context, cmd command.CreateKnowledgeEntryCommand) (command.CreateKnowledgeEntryResult, error)
}

// knowledgeSuggester is the write port for the suggest_knowledge tool (any
// member): it proposes one agent-suggested entry (pending, not searchable) for
// human review.
type knowledgeSuggester interface {
	Handle(ctx context.Context, cmd command.SuggestKnowledgeEntryCommand) (command.SuggestKnowledgeEntryResult, error)
}

// --- Input / output types ---
//
// Field-for-field identical to cli/internal/mcp/tools.go's typed inputs and
// outputs (same JSON tags, same optional markers) so a client's tool call
// behaves the same regardless of which transport (stdio or this HTTP
// endpoint) it is aimed at.

// SearchHistoryInput is the typed input for the search_history tool.
type SearchHistoryInput struct {
	ProjectID string `json:"project_id,omitempty" exhaustruct:"optional"`
	Query     string `json:"query"`
	TopK      int32  `json:"top_k,omitempty" exhaustruct:"optional"`
}

// HistoryHitOutput is a single history search result: one conversation (any
// status) matched by the deterministic history search. The agent branches on
// `status` — join an open thread, reuse a resolved answer, or re-ask the
// original asker of a timed-out/dismissed question.
type HistoryHitOutput struct {
	ConversationID string `json:"conversation_id"`
	// Title is the display title (agent-written, falling back to a truncated
	// question when unset).
	Title string `json:"title" exhaustruct:"optional"`
	// Summary is the agent-curated one-line gist.
	Summary string `json:"summary" exhaustruct:"optional"`
	// Status is the conversation lifecycle state the agent routes on: "open",
	// "answered", "resolved", "timed_out", "dismissed".
	Status string `json:"status"`
	// Tags/Entities are the normalized facets the hit was (partly) matched on.
	Tags     []string `json:"tags" exhaustruct:"optional"`
	Entities []string `json:"entities" exhaustruct:"optional"`
	// AskerMemberID is who opened the conversation — the lead to re-ask via `ask`
	// when the hit is a timed-out/dismissed (unanswered) question.
	AskerMemberID string `json:"asker_member_id"`
	// AgeDays is how many whole days ago the conversation was opened — weigh it
	// for staleness before reusing a resolved hit.
	AgeDays int `json:"age_days"`
	// Project is the resolved project name the hit belongs to (a cross-project
	// search attributes each hit).
	Project string `json:"project" exhaustruct:"optional"`
	// Source is the hit's provenance: "conversation" (a real person answered) or
	// "curated" (a team-authored FAQ/runbook answer, attributed to the org). A
	// trust/attribution label only — reuse a curated hit exactly like a resolved
	// conversation; there is no thread to fetch, the answer is in `summary`.
	Source string `json:"source" exhaustruct:"optional"`
}

// SearchHistoryOutput is the result returned by search_history.
type SearchHistoryOutput struct {
	Hits []HistoryHitOutput `json:"hits"`
	// KnownTags / KnownEntities are the project's existing curated vocabulary
	// (most-used first) — reuse them when you later ask/resolve/follow_up.
	KnownTags     []string `json:"known_tags,omitempty" exhaustruct:"optional"`
	KnownEntities []string `json:"known_entities,omitempty" exhaustruct:"optional"`
}

// ListExpertsInput is the typed input for the list_experts tool.
type ListExpertsInput struct {
	ProjectID string `json:"project_id,omitempty" exhaustruct:"optional"`
}

// ExpertOutput describes one routable member in the expert directory.
// No presence/online field is exposed to agents on purpose: routing is by
// expertise (domains), not availability — a expert can answer a question at
// any moment, so "online" was a misleading signal that made agents bail early.
type ExpertOutput struct {
	MemberID    string   `json:"member_id"`
	DisplayName string   `json:"display_name"`
	Domains     []string `json:"domains"`
	// DeliveryChannel is where Orako reaches this expert (discord/slack/
	// teams/telegram/dashboard). Format the question for this platform.
	DeliveryChannel string `json:"delivery_channel"`
	// MaxMessageChars is ADVISORY: the platform's per-bubble size (e.g.
	// Discord 2000). Nothing is ever truncated — a longer body simply arrives
	// as several sequential messages — but staying near one bubble reads best.
	// 0 means no practical limit (dashboard).
	MaxMessageChars int32 `json:"max_message_chars"`
}

// ListExpertsOutput is the result returned by list_experts.
type ListExpertsOutput struct {
	Experts []ExpertOutput `json:"experts"`
}

// AskInput is the typed input for the ask tool. Exactly
// one of ResponderMemberID (direct ask) or Domains (pool dispatch) is set.
type AskInput struct {
	ProjectID string `json:"project_id,omitempty" exhaustruct:"optional"`
	// ResponderMemberID targets ONE expert (direct ask).
	ResponderMemberID string `json:"member_id,omitempty" exhaustruct:"optional"`
	// Domains dispatches to every matching expert instead; all replies
	// land on the thread. One-of with member_id.
	Domains  []string `json:"domains,omitempty"      exhaustruct:"optional"`
	Question string   `json:"question"`
	// Title is a short imperative list title YOU write (e.g. "Pool pgx en dev") — the server never generates text.
	Title string `json:"title,omitempty"        exhaustruct:"optional"`
	// Summary is a REQUIRED one-line description of what this conversation is
	// about — YOU curate it; it powers history search. Reuse a knownTags-style
	// phrasing when one fits.
	Summary string `json:"summary" jsonschema:"REQUIRED one-line description of what this conversation is about; the server stores it verbatim to power history search"`
	// Tags is a REQUIRED non-empty list of lowercase topic/area labels (e.g.
	// \"billing\", \"discord\"). Reuse labels from knownTags when they fit
	// instead of inventing synonyms.
	Tags []string `json:"tags" jsonschema:"REQUIRED lowercase topic/area labels (e.g. billing, discord); reuse labels from knownTags when they fit instead of inventing synonyms"`
	// Entities are concrete systems/modules the question is about (e.g.
	// \"discord-gateway\", \"billing-webhook\"); optional. Reuse knownEntities.
	Entities   []string `json:"entities,omitempty"     exhaustruct:"optional" jsonschema:"concrete systems/modules mentioned (e.g. discord-gateway, billing-webhook); reuse labels from knownEntities when they fit"`
	Context    string   `json:"context"`
	Files      []string `json:"files,omitempty"        exhaustruct:"optional"`
	TicketURLs []string `json:"ticket_urls,omitempty"  exhaustruct:"optional"`
	Symbols    []string `json:"symbols,omitempty"      exhaustruct:"optional"`
	Wait       bool     `json:"wait,omitempty"         exhaustruct:"optional"`
	// AttachmentIDs are ids from upload_attachment to attach to the opening
	// question — a diagram, a screenshot, a log file the expert needs to see.
	AttachmentIDs []string `json:"attachment_ids,omitempty" exhaustruct:"optional"`
}

// AskOutput is the result returned by ask.
type AskOutput struct {
	ConversationID string `json:"conversation_id"`
	InlineAnswer   string `json:"inline_answer" exhaustruct:"optional"`
	Answered       bool   `json:"answered"`
	// PoolSize is how many experts received a pool dispatch (0 on a
	// direct ask) — report "question sent to N experts".
	PoolSize int32 `json:"pool_size,omitempty" exhaustruct:"optional"`
	// ProjectName is the resolved project the question was filed under — use it
	// to confirm where it went ("sent in cfea") instead of echoing an id.
	ProjectName string `json:"project_name,omitempty" exhaustruct:"optional"`
	// RecipientNames are the display name(s) reached: one for a direct ask,
	// the pool candidates for a domains dispatch. May be empty if unresolved.
	RecipientNames []string `json:"recipient_names,omitempty" exhaustruct:"optional"`
	// KnownTags / KnownEntities are the project's existing curated vocabulary
	// (most-used first). Reuse these labels on the next ask/resolve/follow_up
	// when they fit, instead of inventing synonyms.
	KnownTags     []string `json:"known_tags,omitempty" exhaustruct:"optional"`
	KnownEntities []string `json:"known_entities,omitempty" exhaustruct:"optional"`
}

// GetConversationInput is the typed input for the get_conversation tool.
type GetConversationInput struct {
	ConversationID string `json:"conversation_id"`
}

// MessageOutput is a single message in a conversation.
type MessageOutput struct {
	MessageID      string `json:"message_id"`
	AuthorMemberID string `json:"author_member_id"`
	Role           string `json:"role"`
	Body           string `json:"body"`
	// Source is how the message was authored: "human", "agent" (posted by a
	// coding agent), or "system". When the OTHER party's messages are "agent",
	// you are talking to another agent — offer to switch to sync mode.
	Source string `json:"source"`
	// AgentClient is which coding agent authored an "agent" message (e.g.
	// "claude-code"); empty otherwise.
	AgentClient string `json:"agent_client" exhaustruct:"optional"`
	// Attachments are the files/images on this message, each with a short-lived
	// signed download URL you can GET to fetch the bytes. Empty for text-only.
	Attachments []AttachmentOutput `json:"attachments,omitempty" exhaustruct:"optional"`
}

// AttachmentOutput is one file/image on a message, with a signed download URL.
type AttachmentOutput struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	MimeType    string `json:"mime_type"`
	SizeBytes   int64  `json:"size_bytes"`
	DownloadURL string `json:"download_url"`
}

// GetConversationOutput is the result returned by get_conversation.
type GetConversationOutput struct {
	ConversationID string          `json:"conversation_id"`
	Status         string          `json:"status"`
	Messages       []MessageOutput `json:"messages"`
}

// UploadAttachmentInput is the typed input for the upload_attachment tool.
type UploadAttachmentInput struct {
	ConversationID string `json:"conversation_id"`
	Filename       string `json:"filename"`
	MimeType       string `json:"mime_type"`
	// BytesBase64 is the file content, standard base64-encoded.
	BytesBase64 string `json:"bytes_base64"`
}

// UploadAttachmentOutput returns the new attachment id to reference in a
// following ask/follow_up via attachment_ids.
type UploadAttachmentOutput struct {
	AttachmentID string `json:"attachment_id"`
}

// FollowUpInput is the typed input for the follow_up tool.
type FollowUpInput struct {
	ConversationID string `json:"conversation_id"`
	Body           string `json:"body"`
	// AuthoredByHuman marks the message as the USER's own words that you are only
	// relaying (e.g. they dictated the reply, or in a /sync flow the human wrote
	// it) — set true so it shows as the person, not "via agent". Default false =
	// you (the agent) composed it, shown as "Name · <your client>".
	AuthoredByHuman bool `json:"authored_by_human,omitempty" exhaustruct:"optional"`
	// AttachmentIDs are ids from upload_attachment to attach to this reply
	// (files/images). Empty for a text-only reply.
	AttachmentIDs []string `json:"attachment_ids,omitempty" exhaustruct:"optional"`
	// Summary / Tags / Entities OPTIONALLY evolve the conversation's history
	// metadata as it progresses — a supplied field overwrites that facet, an
	// omitted one is left untouched. Reuse knownTags / knownEntities when they fit.
	Summary  string   `json:"summary,omitempty"  exhaustruct:"optional" jsonschema:"optional updated one-line summary of the conversation; overwrites the stored summary when supplied"`
	Tags     []string `json:"tags,omitempty"     exhaustruct:"optional" jsonschema:"optional updated lowercase topic/area labels; overwrites the stored tags when supplied; reuse labels from knownTags"`
	Entities []string `json:"entities,omitempty" exhaustruct:"optional" jsonschema:"optional updated concrete systems/modules; overwrites the stored entities when supplied; reuse labels from knownEntities"`
}

// FollowUpOutput is the result returned by follow_up.
type FollowUpOutput struct {
	OK bool `json:"ok"`
	// KnownTags / KnownEntities are the project's existing curated vocabulary
	// (most-used first) — reuse them when you evolve this thread's metadata.
	KnownTags     []string `json:"known_tags,omitempty" exhaustruct:"optional"`
	KnownEntities []string `json:"known_entities,omitempty" exhaustruct:"optional"`
}

// ResolveConversationInput is the typed input for the resolve_conversation tool.
type ResolveConversationInput struct {
	ConversationID string `json:"conversation_id"`
	Resolution     string `json:"resolution"`
	// Summary is the REQUIRED FINAL one-line description of what this
	// conversation was about — persisted onto the conversation for history search.
	Summary string `json:"summary" jsonschema:"REQUIRED final one-line description of what this conversation was about; persisted for history search"`
	// Tags is a REQUIRED non-empty list of lowercase topic/area labels; they are
	// persisted as the conversation's final facets.
	// Reuse labels from knownTags when they fit.
	Tags []string `json:"tags" jsonschema:"REQUIRED lowercase topic/area labels (e.g. billing, discord); reuse labels from knownTags when they fit instead of inventing synonyms"`
	// Entities are concrete systems/modules the conversation is about (e.g.
	// \"discord-gateway\"); optional. Reuse knownEntities.
	Entities []string `json:"entities,omitempty" exhaustruct:"optional" jsonschema:"concrete systems/modules mentioned (e.g. discord-gateway, billing-webhook); reuse labels from knownEntities when they fit"`
	// Contested is the agent's divergence verdict, applied only when ContestedSet is true.
	Contested bool `json:"contested" exhaustruct:"optional"`
	// ContestedSet gates Contested: false leaves the server's own default.
	ContestedSet bool `json:"contested_set" exhaustruct:"optional"`
}

// ResolveConversationOutput is the result returned by resolve_conversation.
type ResolveConversationOutput struct {
	// KnownTags / KnownEntities are the project's existing curated vocabulary
	// (most-used first) — reuse them on your next ask/resolve/follow_up.
	KnownTags     []string `json:"known_tags,omitempty" exhaustruct:"optional"`
	KnownEntities []string `json:"known_entities,omitempty" exhaustruct:"optional"`
}

// ListProjectsInput is the typed input for the list_projects tool (no fields — caller identified by token).
type ListProjectsInput struct{}

// ProjectSummaryOutput is one project entry returned by list_projects.
type ProjectSummaryOutput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// ListProjectsOutput is the result returned by list_projects.
type ListProjectsOutput struct {
	Projects []ProjectSummaryOutput `json:"projects"`
}

// ListConversationsInput is the typed input for the list_conversations tool.
type ListConversationsInput struct {
	ProjectID string `json:"project_id,omitempty" exhaustruct:"optional"`
	// Status is an optional filter: "open", "answered", "resolved", "timed_out".
	// Empty string means all statuses.
	Status string `json:"status,omitempty" exhaustruct:"optional"`
}

// ConversationSummaryOutput is one conversation entry returned by list_conversations.
type ConversationSummaryOutput struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	Question          string `json:"question"`
	ResponderMemberID string `json:"responder_member_id"`
}

// ListConversationsOutput is the result returned by list_conversations.
type ListConversationsOutput struct {
	Conversations []ConversationSummaryOutput `json:"conversations"`
}

// AddKnowledgeItem is one curated entry in an add_knowledge call. Question and
// answer are required; summary/tags/entities are the same enrichment an agent
// provides when resolving a conversation (normalized server-side).
type AddKnowledgeItem struct {
	Question string   `json:"question"`
	Answer   string   `json:"answer"`
	Summary  string   `json:"summary,omitempty"  exhaustruct:"optional"`
	Tags     []string `json:"tags,omitempty"     exhaustruct:"optional"`
	Entities []string `json:"entities,omitempty" exhaustruct:"optional"`
}

// AddKnowledgeInput is the typed input for the admin-gated add_knowledge tool.
// The common single-entry case uses the top-level question/answer/… fields; a
// batch (FAQ import) passes entries[] instead. When entries is non-empty it
// wins and the top-level fields are ignored. The org is taken from the caller
// identity, never the payload; every entry is created org-wide.
type AddKnowledgeInput struct {
	// Single-entry convenience fields (ignored when entries is non-empty).
	Question string   `json:"question,omitempty" exhaustruct:"optional"`
	Answer   string   `json:"answer,omitempty"   exhaustruct:"optional"`
	Summary  string   `json:"summary,omitempty"  exhaustruct:"optional"`
	Tags     []string `json:"tags,omitempty"     exhaustruct:"optional"`
	Entities []string `json:"entities,omitempty" exhaustruct:"optional"`
	// Entries is the batch form: multiple curated entries in one call.
	Entries []AddKnowledgeItem `json:"entries,omitempty" exhaustruct:"optional"`
}

// AddKnowledgeOutput is the result returned by add_knowledge: the ids of the
// created entries and how many were stored.
type AddKnowledgeOutput struct {
	EntryIDs []string `json:"entry_ids"`
	Count    int      `json:"count"`
}

// SuggestKnowledgeInput is the typed input for the suggest_knowledge tool
// (available to ANY member). Question and answer are required; summary/tags/
// entities are the same enrichment resolve_conversation takes. The org + author
// come from the caller identity, never the payload.
type SuggestKnowledgeInput struct {
	Question string   `json:"question"`
	Answer   string   `json:"answer"`
	Summary  string   `json:"summary,omitempty"  exhaustruct:"optional"`
	Tags     []string `json:"tags,omitempty"     exhaustruct:"optional"`
	Entities []string `json:"entities,omitempty" exhaustruct:"optional"`
}

// SuggestKnowledgeOutput is the result returned by suggest_knowledge: the new
// pending entry's id and its status (always "pending" — a human must approve it
// before it is searchable).
type SuggestKnowledgeOutput struct {
	EntryID string `json:"entry_id"`
	Status  string `json:"status"`
}

// AddParticipantInput is the typed input for the add_participant tool.
type AddParticipantInput struct {
	ConversationID string `json:"conversation_id"`
	// ResponderMemberID is the member to add to the conversation.
	ResponderMemberID string `json:"member_id"`
}

// AddParticipantOutput is the result returned by add_participant.
type AddParticipantOutput struct {
	OK bool `json:"ok"`
	// AlreadyParticipant is true when the member was already on the conversation.
	AlreadyParticipant bool `json:"already_participant"`
}

// --- RBAC helpers ---
//
// Mirror internal/infra/api/server.go's requireProjectMember /
// isAdminProcedure gates exactly: this package never routes through the
// Connect-RPC interceptor, so every RPC-shaped RBAC check that interceptor
// used to apply is re-applied here, per tool, against the CallerIdentity
// resolved by the auth middleware in server.go.

var errNoIdentity = errors.New("no caller identity in context")

// callerIdentity resolves the CallerIdentity the auth middleware attached to
// ctx. Every handler below calls this first; a missing identity means the
// auth middleware was bypassed, which must never happen in production wiring.
func callerIdentity(ctx context.Context) (api.CallerIdentity, error) {
	identity, ok := identityFromContext(ctx)
	if !ok {
		return api.CallerIdentity{}, errNoIdentity
	}

	return identity, nil
}

// requireProjectMember resolves the caller identity and checks it belongs to
// projectID — the same check transport.requireProjectMember applies to
// SearchHistory, ListExperts, Ask, and ListConversations.
func requireProjectMember(ctx context.Context, projectID uuid.UUID) (api.CallerIdentity, error) {
	identity, err := callerIdentity(ctx)
	if err != nil {
		return api.CallerIdentity{}, err
	}

	if !slices.Contains(identity.ProjectIDs, projectID) {
		return api.CallerIdentity{}, errors.New("caller is not a member of the requested project")
	}

	return identity, nil
}

// resolveProject resolves the project a tool acts on. An explicit project_id is
// parsed and checked for scope membership; an empty project_id resolves to the
// token's primary project (the first in scope). When writeGuard is set — for a
// project-creating write like ask — an omitted project_id is
// rejected if the connection scopes more than one project, so the write cannot
// silently land in the wrong one.
func resolveProject(ctx context.Context, raw string, writeGuard bool) (uuid.UUID, api.CallerIdentity, error) {
	if strings.TrimSpace(raw) == "" {
		identity, err := callerIdentity(ctx)
		if err != nil {
			return uuid.Nil, api.CallerIdentity{}, err
		}

		if writeGuard && len(identity.ProjectIDs) > 1 {
			return uuid.Nil, api.CallerIdentity{}, fmt.Errorf(
				"project_id is required: this connection is scoped to %d projects — call list_projects and pass one",
				len(identity.ProjectIDs),
			)
		}

		if identity.ProjectID == uuid.Nil {
			return uuid.Nil, api.CallerIdentity{}, errors.New("no project available for this connection")
		}

		return identity.ProjectID, identity, nil
	}

	projectID, err := parseUUID(raw, "project_id")
	if err != nil {
		return uuid.Nil, api.CallerIdentity{}, err
	}

	identity, err := requireProjectMember(ctx, projectID)
	if err != nil {
		return uuid.Nil, api.CallerIdentity{}, err
	}

	return projectID, identity, nil
}

// parseUUID parses s as a UUID, naming field in the error for an actionable message.
func parseUUID(s, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s must be a valid UUID", field)
	}

	return id, nil
}

// parseUUIDs parses a slice of UUID strings, naming field on any failure.
func parseUUIDs(ss []string, field string) ([]uuid.UUID, error) {
	if len(ss) == 0 {
		return nil, nil
	}

	out := make([]uuid.UUID, len(ss))

	for i, s := range ss {
		id, err := parseUUID(s, field)
		if err != nil {
			return nil, err
		}

		out[i] = id
	}

	return out, nil
}

// --- Handlers ---

// vocabularyLimit caps the reinjected known-tags/known-entities lists on tool
// responses — enough to steer reuse without flooding the agent's context.
const vocabularyLimit = 50

// knownVocabulary best-effort reads the project scope's existing curated
// tag/entity vocabulary for reinjection into a tool response. A nil reader,
// empty scope, or any error yields empty lists — the reuse hint must never fail
// the tool it decorates.
func knownVocabulary(ctx context.Context, vocab vocabularer, projectIDs []uuid.UUID) ([]string, []string) {
	if vocab == nil || len(projectIDs) == 0 {
		return nil, nil
	}

	v, err := vocab.Handle(ctx, query.VocabularyQuery{ProjectIDs: projectIDs, Limit: vocabularyLimit})
	if err != nil {
		return nil, nil
	}

	return v.Tags, v.Entities
}

func searchHistoryHandler(h searchHistorier, vocab vocabularer) sdkmcp.ToolHandlerFor[SearchHistoryInput, SearchHistoryOutput] {
	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in SearchHistoryInput) (*sdkmcp.CallToolResult, SearchHistoryOutput, error) {
		projectID, identity, err := resolveProject(ctx, in.ProjectID, false)
		if err != nil {
			return nil, SearchHistoryOutput{}, err
		}

		// IncludeCurated unions the org's ACTIVE curated knowledge entries into the
		// results, ranked alongside organic conversations by the same score — the
		// agent sees both under one shape (source labels which is which but is a
		// UI hint, not a branch). OrgID carries the org-wide curated scope
		// (project_id NULL entries), which project_id alone cannot reach.
		hits, err := h.Handle(ctx, query.SearchHistoryQuery{
			OrgID:          identity.OrgID,
			ProjectIDs:     []uuid.UUID{projectID},
			Query:          in.Query,
			TopK:           int(in.TopK),
			IncludeCurated: true,
		})
		if err != nil {
			return nil, SearchHistoryOutput{}, actionableError(err, toolSearchHistory)
		}

		out := make([]HistoryHitOutput, 0, len(hits))

		for _, hit := range hits {
			out = append(out, HistoryHitOutput{
				ConversationID: hit.ConversationID.String(),
				Title:          hit.Title,
				Summary:        hit.Summary,
				Status:         string(hit.Status),
				Tags:           hit.Tags,
				Entities:       hit.Entities,
				AskerMemberID:  hit.AskerMemberID.String(),
				AgeDays:        hit.AgeDays,
				Project:        hit.ProjectName,
				Source:         hitSource(hit.Source),
			})
		}

		knownTags, knownEntities := knownVocabulary(ctx, vocab, []uuid.UUID{projectID})

		return nil, SearchHistoryOutput{Hits: out, KnownTags: knownTags, KnownEntities: knownEntities}, nil
	}
}

func listExpertsHandler(h listExpertser) sdkmcp.ToolHandlerFor[ListExpertsInput, ListExpertsOutput] {
	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ListExpertsInput) (*sdkmcp.CallToolResult, ListExpertsOutput, error) {
		projectID, _, err := resolveProject(ctx, in.ProjectID, false)
		if err != nil {
			return nil, ListExpertsOutput{}, err
		}

		experts, err := h.Handle(ctx, query.ListExpertsQuery{ProjectID: projectID})
		if err != nil {
			return nil, ListExpertsOutput{}, actionableError(err, toolListExperts)
		}

		out := make([]ExpertOutput, 0, len(experts))

		for _, sp := range experts {
			channel := sp.DeliveryChannel
			if channel == "" {
				channel = model.DeliveryChannelDashboard
			}

			out = append(out, ExpertOutput{
				MemberID:        sp.MemberID.String(),
				DisplayName:     sp.DisplayName,
				Domains:         sp.Domains,
				DeliveryChannel: string(channel),
				MaxMessageChars: maxMessageChars(channel),
			})
		}

		return nil, ListExpertsOutput{Experts: out}, nil
	}
}

func askHandler(h asker, vocab vocabularer) sdkmcp.ToolHandlerFor[AskInput, AskOutput] {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest, in AskInput) (*sdkmcp.CallToolResult, AskOutput, error) {
		// Validate the one-of locally for a crisp error before any app-layer call.
		if (in.ResponderMemberID == "") == (len(in.Domains) == 0) {
			return nil, AskOutput{}, errors.New(
				"provide exactly one of member_id (direct ask) or domains (pool dispatch — every matching expert is contacted and all replies land on the thread)",
			)
		}

		projectID, identity, err := resolveProject(ctx, in.ProjectID, true)
		if err != nil {
			return nil, AskOutput{}, err
		}

		expertID := uuid.Nil

		if in.ResponderMemberID != "" {
			expertID, err = parseUUID(in.ResponderMemberID, "member_id")
			if err != nil {
				return nil, AskOutput{}, err
			}
		}

		attachmentIDs, err := parseUUIDs(in.AttachmentIDs, "attachment_ids")
		if err != nil {
			return nil, AskOutput{}, err
		}

		result, err := h.Handle(ctx, command.AskCommand{
			ProjectID:         projectID,
			AskerMemberID:     identity.MemberID,
			ResponderMemberID: expertID,
			Domains:           in.Domains,
			Question:          in.Question,
			Title:             in.Title,
			Context:           composeAskContext(in),
			Summary:           in.Summary,
			Tags:              in.Tags,
			Entities:          in.Entities,
			Wait:              in.Wait,
			AgentClient:       mcpClientName(req),
			AttachmentIDs:     attachmentIDs,
		})
		if err != nil {
			return nil, AskOutput{}, actionableError(err, toolAsk)
		}

		knownTags, knownEntities := knownVocabulary(ctx, vocab, []uuid.UUID{projectID})

		return nil, AskOutput{
			ConversationID: result.ConversationID.String(),
			InlineAnswer:   result.InlineAnswer,
			Answered:       result.Answered,
			PoolSize:       int32(result.PoolSize), //nolint:gosec // pool size is bounded by project membership.
			ProjectName:    result.ProjectName,
			RecipientNames: result.RecipientNames,
			KnownTags:      knownTags,
			KnownEntities:  knownEntities,
		}, nil
	}
}

// composeAskContext folds structured references into free-text context.
func composeAskContext(in AskInput) string {
	if len(in.Files) == 0 && len(in.TicketURLs) == 0 && len(in.Symbols) == 0 {
		return in.Context
	}

	var b strings.Builder

	b.WriteString(in.Context)

	if len(in.Files) > 0 {
		fmt.Fprintf(&b, "\n\nFiles:\n- %s", strings.Join(in.Files, "\n- "))
	}

	if len(in.TicketURLs) > 0 {
		fmt.Fprintf(&b, "\n\nTickets:\n- %s", strings.Join(in.TicketURLs, "\n- "))
	}

	if len(in.Symbols) > 0 {
		fmt.Fprintf(&b, "\n\nSymbols:\n- %s", strings.Join(in.Symbols, "\n- "))
	}

	return b.String()
}

func getConversationHandler(h getConversationer) sdkmcp.ToolHandlerFor[GetConversationInput, GetConversationOutput] {
	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in GetConversationInput) (*sdkmcp.CallToolResult, GetConversationOutput, error) {
		convID, err := parseUUID(in.ConversationID, "conversation_id")
		if err != nil {
			return nil, GetConversationOutput{}, err
		}

		identity, err := callerIdentity(ctx)
		if err != nil {
			return nil, GetConversationOutput{}, err
		}

		view, err := h.Handle(ctx, query.GetConversationQuery{
			ConversationID:   convID,
			CallerMemberID:   identity.MemberID,
			IsOrgAdmin:       identity.IsOrgAdmin,
			CallerProjectIDs: identity.ProjectIDs,
		})
		if err != nil {
			return nil, GetConversationOutput{}, actionableError(err, toolGetConversation)
		}

		msgs := make([]MessageOutput, 0, len(view.Messages))

		for _, m := range view.Messages {
			atts := make([]AttachmentOutput, 0, len(m.Attachments))
			for _, a := range m.Attachments {
				atts = append(atts, AttachmentOutput{
					ID:          a.ID.String(),
					Filename:    a.Filename,
					MimeType:    a.MimeType,
					SizeBytes:   a.SizeBytes,
					DownloadURL: a.DownloadURL,
				})
			}

			msgs = append(msgs, MessageOutput{
				MessageID:      m.ID.String(),
				AuthorMemberID: m.AuthorMemberID.String(),
				Role:           messageRoleToString(m.Role),
				Body:           m.Body,
				Source:         string(m.Source),
				AgentClient:    m.AgentClient,
				Attachments:    atts,
			})
		}

		return nil, GetConversationOutput{
			ConversationID: view.ID.String(),
			Status:         string(view.Status),
			Messages:       msgs,
		}, nil
	}
}

func followUpHandler(h followUpper, vocab vocabularer) sdkmcp.ToolHandlerFor[FollowUpInput, FollowUpOutput] {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest, in FollowUpInput) (*sdkmcp.CallToolResult, FollowUpOutput, error) {
		convID, err := parseUUID(in.ConversationID, "conversation_id")
		if err != nil {
			return nil, FollowUpOutput{}, err
		}

		identity, err := callerIdentity(ctx)
		if err != nil {
			return nil, FollowUpOutput{}, err
		}

		source := model.MessageSourceAgent
		if in.AuthoredByHuman {
			source = model.MessageSourceHuman
		}

		attachmentIDs, err := parseUUIDs(in.AttachmentIDs, "attachment_ids")
		if err != nil {
			return nil, FollowUpOutput{}, err
		}

		if _, err = h.Handle(ctx, command.FollowUpCommand{
			ConversationID: convID,
			AuthorMemberID: identity.MemberID,
			Message:        in.Body,
			Source:         source,
			AgentClient:    mcpClientName(req),
			AttachmentIDs:  attachmentIDs,
			Summary:        in.Summary,
			Tags:           in.Tags,
			Entities:       in.Entities,
		}); err != nil {
			return nil, FollowUpOutput{}, actionableError(err, toolFollowUp)
		}

		knownTags, knownEntities := knownVocabulary(ctx, vocab, identity.ProjectIDs)

		return nil, FollowUpOutput{OK: true, KnownTags: knownTags, KnownEntities: knownEntities}, nil
	}
}

func uploadAttachmentHandler(h uploader, maxBytes int64) sdkmcp.ToolHandlerFor[UploadAttachmentInput, UploadAttachmentOutput] {
	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in UploadAttachmentInput) (*sdkmcp.CallToolResult, UploadAttachmentOutput, error) {
		convID, err := parseUUID(in.ConversationID, "conversation_id")
		if err != nil {
			return nil, UploadAttachmentOutput{}, err
		}

		if strings.TrimSpace(in.Filename) == "" {
			return nil, UploadAttachmentOutput{}, errors.New("filename is required")
		}

		content, err := base64.StdEncoding.DecodeString(in.BytesBase64)
		if err != nil {
			return nil, UploadAttachmentOutput{}, errors.New("bytes_base64 must be standard base64-encoded content")
		}

		if maxBytes > 0 && int64(len(content)) > maxBytes {
			return nil, UploadAttachmentOutput{}, fmt.Errorf("attachment is %d bytes; the server limit is %d", len(content), maxBytes)
		}

		identity, err := callerIdentity(ctx)
		if err != nil {
			return nil, UploadAttachmentOutput{}, err
		}

		mime := strings.TrimSpace(in.MimeType)
		if mime == "" {
			mime = "application/octet-stream"
		}

		res, err := h.Handle(ctx, command.UploadAttachmentCommand{
			ConversationID:   convID,
			UploaderMemberID: identity.MemberID,
			Filename:         in.Filename,
			MimeType:         mime,
			Content:          content,
		})
		if err != nil {
			return nil, UploadAttachmentOutput{}, actionableError(err, toolUploadAttachment)
		}

		return nil, UploadAttachmentOutput{AttachmentID: res.AttachmentID.String()}, nil
	}
}

func resolveConversationHandler(h resolveConversationer, vocab vocabularer) sdkmcp.ToolHandlerFor[ResolveConversationInput, ResolveConversationOutput] {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest, in ResolveConversationInput) (*sdkmcp.CallToolResult, ResolveConversationOutput, error) {
		convID, err := parseUUID(in.ConversationID, "conversation_id")
		if err != nil {
			return nil, ResolveConversationOutput{}, err
		}

		identity, err := callerIdentity(ctx)
		if err != nil {
			return nil, ResolveConversationOutput{}, err
		}

		if _, err := h.Handle(ctx, command.ResolveConversationCommand{
			ConversationID: convID,
			CloserMemberID: identity.MemberID,
			Resolution:     in.Resolution,
			Summary:        in.Summary,
			Tags:           in.Tags,
			Entities:       in.Entities,
			Contested:      in.Contested,
			ContestedSet:   in.ContestedSet,
			Source:         model.MessageSourceAgent,
			AgentClient:    mcpClientName(req),
		}); err != nil {
			return nil, ResolveConversationOutput{}, actionableError(err, toolResolveConversation)
		}

		knownTags, knownEntities := knownVocabulary(ctx, vocab, identity.ProjectIDs)

		return nil, ResolveConversationOutput{
			KnownTags:     knownTags,
			KnownEntities: knownEntities,
		}, nil
	}
}

func listProjectsHandler(h projectsLister) sdkmcp.ToolHandlerFor[ListProjectsInput, ListProjectsOutput] {
	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ ListProjectsInput) (*sdkmcp.CallToolResult, ListProjectsOutput, error) {
		identity, err := callerIdentity(ctx)
		if err != nil {
			return nil, ListProjectsOutput{}, err
		}

		summaries, err := h.Handle(ctx, query.ListProjectsQuery{CallerMemberID: identity.MemberID})
		if err != nil {
			return nil, ListProjectsOutput{}, actionableError(err, toolListProjects)
		}

		out := make([]ProjectSummaryOutput, 0, len(summaries))

		for _, p := range summaries {
			// Scope the discovery surface: the agent only sees the projects its
			// token can reach (identity.ProjectIDs), matching what
			// requireProjectMember will let it act on.
			if !slices.Contains(identity.ProjectIDs, p.ID) {
				continue
			}

			out = append(out, ProjectSummaryOutput{
				ID:   p.ID.String(),
				Name: p.Name,
				Role: roleToString(p.Role),
			})
		}

		return nil, ListProjectsOutput{Projects: out}, nil
	}
}

func listConversationsHandler(h conversationsLister) sdkmcp.ToolHandlerFor[ListConversationsInput, ListConversationsOutput] {
	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ListConversationsInput) (*sdkmcp.CallToolResult, ListConversationsOutput, error) {
		projectID, identity, err := resolveProject(ctx, in.ProjectID, false)
		if err != nil {
			return nil, ListConversationsOutput{}, err
		}

		summaries, err := h.Handle(ctx, query.ListConversationsQuery{
			ProjectIDs:     []uuid.UUID{projectID},
			Status:         in.Status,
			CallerMemberID: identity.MemberID,
			IsOrgAdmin:     identity.IsOrgAdmin,
		})
		if err != nil {
			return nil, ListConversationsOutput{}, actionableError(err, toolListConversations)
		}

		out := make([]ConversationSummaryOutput, 0, len(summaries))

		for _, c := range summaries {
			out = append(out, ConversationSummaryOutput{
				ID:                c.ID.String(),
				Status:            string(c.Status),
				Question:          c.Question,
				ResponderMemberID: c.ResponderMemberID.String(),
			})
		}

		return nil, ListConversationsOutput{Conversations: out}, nil
	}
}

func addParticipantHandler(h addParticipanter) sdkmcp.ToolHandlerFor[AddParticipantInput, AddParticipantOutput] {
	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in AddParticipantInput) (*sdkmcp.CallToolResult, AddParticipantOutput, error) {
		convID, err := parseUUID(in.ConversationID, "conversation_id")
		if err != nil {
			return nil, AddParticipantOutput{}, err
		}

		newMemberID, err := parseUUID(in.ResponderMemberID, "member_id")
		if err != nil {
			return nil, AddParticipantOutput{}, err
		}

		identity, err := callerIdentity(ctx)
		if err != nil {
			return nil, AddParticipantOutput{}, err
		}

		result, err := h.Handle(ctx, command.AddParticipantCommand{
			ConversationID:  convID,
			NewMemberID:     newMemberID,
			AddedByMemberID: identity.MemberID,
		})
		if err != nil {
			return nil, AddParticipantOutput{}, actionableError(err, toolAddParticipant)
		}

		return nil, AddParticipantOutput{OK: true, AlreadyParticipant: result.AlreadyParticipant}, nil
	}
}

// maxKnowledgeBatch caps how many entries one add_knowledge call may create, so
// a single FAQ import cannot blow up the request body or the index in one shot.
const maxKnowledgeBatch = 50

// hitSource maps the read-side HistorySource to the wire value, defaulting an
// empty source to "conversation" (organic history) so the field is never blank.
func hitSource(source string) string {
	if source == "" {
		return query.HistorySourceConversation
	}

	return source
}

// errNotOrgAdmin is returned when a non-admin calls an org-admin-gated MCP tool.
var errNotOrgAdmin = errors.New("add_knowledge requires org admin: ask an org admin to author curated knowledge")

func addKnowledgeHandler(h knowledgeAdder) sdkmcp.ToolHandlerFor[AddKnowledgeInput, AddKnowledgeOutput] {
	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in AddKnowledgeInput) (*sdkmcp.CallToolResult, AddKnowledgeOutput, error) {
		identity, err := callerIdentity(ctx)
		if err != nil {
			return nil, AddKnowledgeOutput{}, err
		}

		// Admin gate: curated knowledge is authored by org admins (the same
		// IsOrgAdmin signal the Connect-RPC admin procedures use), never a plain
		// project member. Org comes from the identity, never the payload.
		if !identity.IsOrgAdmin {
			return nil, AddKnowledgeOutput{}, errNotOrgAdmin
		}

		items := knowledgeItems(in)
		if len(items) == 0 {
			return nil, AddKnowledgeOutput{}, errors.New("provide question and answer (or a non-empty entries[] batch)")
		}

		if len(items) > maxKnowledgeBatch {
			return nil, AddKnowledgeOutput{}, fmt.Errorf("too many entries: %d (max %d per call)", len(items), maxKnowledgeBatch)
		}

		ids := make([]string, 0, len(items))

		for _, item := range items {
			result, cerr := h.Handle(ctx, command.CreateKnowledgeEntryCommand{
				OrgID:          identity.OrgID,
				AuthorMemberID: identity.MemberID,
				Question:       item.Question,
				Answer:         item.Answer,
				Summary:        item.Summary,
				Tags:           item.Tags,
				Entities:       item.Entities,
			})
			if cerr != nil {
				return nil, AddKnowledgeOutput{}, actionableError(cerr, toolAddKnowledge)
			}

			ids = append(ids, result.EntryID.String())
		}

		return nil, AddKnowledgeOutput{EntryIDs: ids, Count: len(ids)}, nil
	}
}

// knowledgeItems normalizes an add_knowledge input into a list of items: the
// batch entries[] when present, otherwise the single top-level entry (when it
// carries any content).
func knowledgeItems(in AddKnowledgeInput) []AddKnowledgeItem {
	if len(in.Entries) > 0 {
		return in.Entries
	}

	if strings.TrimSpace(in.Question) == "" && strings.TrimSpace(in.Answer) == "" {
		return nil
	}

	return []AddKnowledgeItem{{
		Question: in.Question,
		Answer:   in.Answer,
		Summary:  in.Summary,
		Tags:     in.Tags,
		Entities: in.Entities,
	}}
}

// suggestKnowledgeHandler backs the suggest_knowledge tool. Unlike
// add_knowledge it is NOT admin-gated — any authenticated member's agent may
// PROPOSE knowledge from its work; the proposal lands pending and is NOT
// searchable until a human approves it (the trust gradient). Org + author come
// from the caller identity, never the payload.
func suggestKnowledgeHandler(h knowledgeSuggester) sdkmcp.ToolHandlerFor[SuggestKnowledgeInput, SuggestKnowledgeOutput] {
	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in SuggestKnowledgeInput) (*sdkmcp.CallToolResult, SuggestKnowledgeOutput, error) {
		identity, err := callerIdentity(ctx)
		if err != nil {
			return nil, SuggestKnowledgeOutput{}, err
		}

		if strings.TrimSpace(in.Question) == "" || strings.TrimSpace(in.Answer) == "" {
			return nil, SuggestKnowledgeOutput{}, errors.New("provide both question and answer")
		}

		result, err := h.Handle(ctx, command.SuggestKnowledgeEntryCommand{
			OrgID:          identity.OrgID,
			AuthorMemberID: identity.MemberID,
			Question:       in.Question,
			Answer:         in.Answer,
			Summary:        in.Summary,
			Tags:           in.Tags,
			Entities:       in.Entities,
		})
		if err != nil {
			return nil, SuggestKnowledgeOutput{}, actionableError(err, toolSuggestKnowledge)
		}

		return nil, SuggestKnowledgeOutput{
			EntryID: result.EntryID.String(),
			Status:  string(model.KnowledgeStatusPending),
		}, nil
	}
}

// mcpClientName returns the MCP client's declared name.
func mcpClientName(req *sdkmcp.CallToolRequest) string {
	if req == nil {
		return ""
	}

	clientInfo := req.ClientInfo()
	if clientInfo == nil {
		return ""
	}

	return clientInfo.Name
}

const formatOverheadReserve int32 = 200

// maxMessageChars returns the advisory single-message character budget.
func maxMessageChars(c model.DeliveryChannel) int32 {
	var rawCap int32

	switch c {
	case model.DeliveryChannelDiscord:
		rawCap = 2000
	case model.DeliveryChannelSlack:
		rawCap = 4000
	case model.DeliveryChannelTelegram:
		rawCap = 4096
	case model.DeliveryChannelTeams:
		rawCap = 28000
	case model.DeliveryChannelDashboard:
		return 0
	default:
		return 0
	}

	return rawCap - formatOverheadReserve
}

// roleToString converts a domain model.Role to a human-readable string.
func roleToString(r model.Role) string {
	switch r {
	case model.RoleUnspecified:
		return roleNameUnspecified
	case model.RoleDev:
		return roleNameDev
	case model.RoleSpecialist:
		return roleNameSpecialist
	case model.RoleLead:
		return roleNameLead
	case model.RoleAdmin:
		return roleNameAdmin
	}

	return roleNameUnspecified
}

// messageRoleToString converts a domain model.MessageRole to a human-readable string.
func messageRoleToString(r model.MessageRole) string {
	switch r {
	case model.MessageRoleUnspecified:
		return "unknown"
	case model.MessageRoleQuestion:
		return "question" //nolint:goconst // JSON wire tag values (question/answer) share this spelling but a struct tag cannot reference a constant
	case model.MessageRoleAnswer:
		return "answer" //nolint:goconst // JSON wire tag values (question/answer) share this spelling but a struct tag cannot reference a constant
	case model.MessageRoleFollowUp:
		return "follow_up"
	case model.MessageRoleSystem:
		return "system"
	case model.MessageRoleSecondOpinion:
		return "second_opinion"
	}

	return "unknown"
}

// --- Errors ---

// actionableError converts an app-layer error into a guidance-bearing message
// so the calling agent knows what to do next, mirroring
// cli/internal/mcp/errors.go's actionableError but working directly off the
// typed errs.* sentinels the application layer returns (this package never
// sees a connect.Error — there is no Connect-RPC hop).
func actionableError(err error, tool string) error {
	if err == nil {
		return nil
	}

	var nf errs.NotFoundError
	if errors.As(err, &nf) {
		return fmt.Errorf("%w; %s", err, notFoundHint(tool))
	}

	var forb errs.ForbiddenError
	if errors.As(err, &forb) {
		return fmt.Errorf("%w; ask a project lead or org admin before retrying", err)
	}

	var internal errs.InternalError
	if errors.As(err, &internal) {
		return errors.New(internal.Detail())
	}

	// errs.InvalidError, errs.DuplicateError, and errs.ConflictError already
	// carry an actionable caller-safe message in Error().
	return err
}

// notFoundHint returns a tool-specific suggestion when the app layer returns
// errs.NotFoundError, guiding the agent toward the right list/search tool.
func notFoundHint(tool string) string {
	switch tool {
	case toolGetConversation, toolFollowUp, toolResolveConversation:
		return "use list_conversations to find active conversation IDs"

	case toolAsk:
		return "use list_experts to find valid member IDs, or list_projects to find valid project IDs"

	default:
		return "use list_projects to find valid project IDs"
	}
}
