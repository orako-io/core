// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/orako-io/core/internal/infra/api"
)

// version is reported in the MCP implementation metadata. Kept as a constant
// rather than threaded through from a build tag: this endpoint's protocol
// surface (tool set, schemas, instructions), not the server binary version,
// is what a client cares about here.
const version = "1.0.0"

// identityExtraKey is the key CallerIdentity is stored under in
// auth.TokenInfo.Extra, so tool handlers can recover it via
// identityFromContext without a second lookup.
const identityExtraKey = "orako_caller_identity"

// tokenInfoTTL is a synthetic, forward-dated expiration handed to the go-sdk's
// auth.TokenInfo. It is NOT the mcp_at_ token's real TTL — mcpAuth.Authenticate
// (called on every single request, since the auth middleware wraps every
// /mcp call, not just session creation) already re-validates the real
// expiry/revocation state stored in Postgres. auth.TokenInfo.Expiration only
// exists to satisfy the SDK's own non-zero/not-yet-expired invariant in
// auth.RequireBearerToken; a short forward-dated value is deliberately
// conservative so a stale TokenInfo can never outlive a single request retry.
const tokenInfoTTL = 5 * time.Minute

// RejectingAuthenticator is the fallback api.Authenticator this
// endpoint's MCPTokenAuthenticator must be wired with (see
// cmd/orako-server/main.go). It refuses every bearer unconditionally: /mcp
// accepts ONLY mcp_at_ tokens issued by Orako's own thin OAuth Authorization
// Server. A raw Supabase JWT — perfectly valid for the dashboard Connect-RPC
// surface — must never authenticate here; that would be exactly the
// confused-deputy reuse the MCP Authorization spec forbids. See
// internal/infra/api/mcp_token_auth_test.go's
// TestMCPTokenRawJWTRejectedWithRejectingFallback, which is the reference
// behavior this type provides in production.
type RejectingAuthenticator struct{}

// Authenticate always fails: there is no fallback identity source at /mcp.
func (RejectingAuthenticator) Authenticate(_ context.Context, _ string) (api.CallerIdentity, error) {
	return api.CallerIdentity{}, errRejected
}

// AuthenticateAccount always fails for the same reason: /mcp has no
// account-only fallback either — only mcp_at_ tokens are ever accepted here.
func (RejectingAuthenticator) AuthenticateAccount(_ context.Context, _ string) (api.CallerIdentity, error) {
	return api.CallerIdentity{}, errRejected
}

var errRejected = errors.New("this endpoint accepts only Orako MCP OAuth (mcp_at_) tokens; dashboard sessions are not valid here")

// Server is the server-hosted MCP-over-HTTP resource: streamable-HTTP
// transport, OAuth-gated by an mcp_at_-only api.Authenticator (see
// RejectingAuthenticator), tools running in-process against the core
// application layer.
type Server struct {
	mcpAuth api.Authenticator
	prmURL  string
	logger  *slog.Logger
	// mcpServer is built by NewServer after the literal below, once every
	// tool dependency is known.
	mcpServer *sdkmcp.Server `exhaustruct:"optional"`
}

// NewServer builds the MCP-over-HTTP resource server. mcpAuth must be a
// api.MCPTokenAuthenticator wired with RejectingAuthenticator as its
// fallback (see cmd/orako-server/main.go) — this constructor does not enforce
// that shape, but Handler's security posture depends on it. prmURL is the
// absolute URL of the OAuth Protected Resource Metadata document
// (oauth.Server.PRMURL()), returned in the WWW-Authenticate challenge on every
// 401 so a client discovers the Authorization Server.
func NewServer(
	searchHistory searchHistorier,
	historyVocabulary vocabularer,
	listExperts listExpertser,
	ask asker,
	getConversation getConversationer,
	followUp followUpper,
	uploadAttachment uploader,
	resolveConversation resolveConversationer,
	listProjects projectsLister,
	listConversations conversationsLister,
	addParticipant addParticipanter,
	addKnowledge knowledgeAdder,
	suggestKnowledge knowledgeSuggester,
	maxAttachmentBytes int64,
	mcpAuth api.Authenticator,
	prmURL string,
	logger *slog.Logger,
) *Server {
	s := &Server{
		mcpAuth: mcpAuth,
		prmURL:  prmURL,
		logger:  logger,
	}

	s.mcpServer = buildMCPServer(toolDeps{
		searchHistory:       searchHistory,
		historyVocabulary:   historyVocabulary,
		listExperts:         listExperts,
		ask:                 ask,
		getConversation:     getConversation,
		followUp:            followUp,
		uploadAttachment:    uploadAttachment,
		resolveConversation: resolveConversation,
		listProjects:        listProjects,
		listConversations:   listConversations,
		addParticipant:      addParticipant,
		addKnowledge:        addKnowledge,
		suggestKnowledge:    suggestKnowledge,
		maxAttachmentBytes:  maxAttachmentBytes,
	})

	return s
}

// toolDeps groups every application-layer handler the tool set depends on,
// keeping buildMCPServer's signature manageable.
type toolDeps struct {
	searchHistory       searchHistorier
	historyVocabulary   vocabularer
	listExperts         listExpertser
	ask                 asker
	getConversation     getConversationer
	followUp            followUpper
	uploadAttachment    uploader
	resolveConversation resolveConversationer
	listProjects        projectsLister
	listConversations   conversationsLister
	addParticipant      addParticipanter
	addKnowledge        knowledgeAdder
	suggestKnowledge    knowledgeSuggester
	maxAttachmentBytes  int64
}

// boolPtr returns a pointer to b — used to set *bool ToolAnnotations fields.
func boolPtr(b bool) *bool {
	return &b
}

// buildMCPServer constructs the sdkmcp.Server with every tool registered plus
// the embedded instructions and prompts. It is safe to reuse the returned
// value across every session: the go-sdk docs for
// mcp.NewStreamableHTTPHandler explicitly allow getServer to return the same
// *Server for every request, and every tool handler here reads the caller
// identity fresh from ctx on each call rather than from any per-session
// captured state.
func buildMCPServer(deps toolDeps) *sdkmcp.Server {
	srv := sdkmcp.NewServer(
		&sdkmcp.Implementation{
			Name:    "orako",
			Version: version,
		},
		&sdkmcp.ServerOptions{
			Instructions: serverInstructions,
		},
	)

	registerCoreTools(srv, deps)
	registerMoreTools(srv, deps)
	registerPrompts(srv)

	return srv
}

// registerCoreTools adds the agent-facing tool set — the product's core loop
// (search, ask, park-and-resume, close, feedback). Mirrors
// cli/internal/mcp/server.go's registerCoreTools.
func registerCoreTools(srv *sdkmcp.Server, deps toolDeps) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolSearchHistory,
		Description: descSearchHistory,
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(true),
		},
	}, searchHistoryHandler(deps.searchHistory, deps.historyVocabulary))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolListExperts,
		Description: descListExperts,
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(true),
		},
	}, listExpertsHandler(deps.listExperts))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolAsk,
		Description: descAsk,
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, askHandler(deps.ask, deps.historyVocabulary))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolGetConversation,
		Description: descGetConversation,
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(true),
		},
	}, getConversationHandler(deps.getConversation))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolFollowUp,
		Description: descFollowUp,
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, followUpHandler(deps.followUp, deps.historyVocabulary))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolUploadAttachment,
		Description: descUploadAttachment,
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, uploadAttachmentHandler(deps.uploadAttachment, deps.maxAttachmentBytes))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolAddParticipant,
		Description: descAddParticipant,
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, addParticipantHandler(deps.addParticipant))
}

// registerMoreTools adds the remaining tools — split from registerCoreTools
// purely to keep each function
// under the length lint.
func registerMoreTools(srv *sdkmcp.Server, deps toolDeps) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolResolveConversation,
		Description: descResolveConversation,
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, resolveConversationHandler(deps.resolveConversation, deps.historyVocabulary))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolListProjects,
		Description: descListProjects,
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(true),
		},
	}, listProjectsHandler(deps.listProjects))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolListConversations,
		Description: descListConversations,
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(true),
		},
	}, listConversationsHandler(deps.listConversations))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolAddKnowledge,
		Description: descAddKnowledge,
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, addKnowledgeHandler(deps.addKnowledge))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        toolSuggestKnowledge,
		Description: descSuggestKnowledge,
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, suggestKnowledgeHandler(deps.suggestKnowledge))
}

// Handler returns the /mcp endpoint: streamable-HTTP MCP transport wrapped in
// an OAuth resource-server gate. An unauthenticated or wrongly-authenticated
// request never reaches the MCP transport at all — auth.RequireBearerToken
// rejects it with 401 and a WWW-Authenticate: Bearer resource_metadata="..."
// header pointing at prmURL, before the streamable handler sees the request.
func (s *Server) Handler() http.Handler {
	streamable := sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return s.mcpServer },
		&sdkmcp.StreamableHTTPOptions{
			Stateless:                    true,
			Logger:                       s.logger,
			PropagateRequestCancellation: true,
		},
	)

	return sdkauth.RequireBearerToken(s.verifyToken, &sdkauth.RequireBearerTokenOptions{
		ResourceMetadataURL: s.prmURL,
	})(streamable)
}

// verifyToken adapts api.Authenticator (the "Bearer <token>" header
// shape every other authenticator in this codebase uses) to the go-sdk's
// auth.TokenVerifier (raw token, no "Bearer " prefix). Rejection here is what
// makes the mcpAuth authenticator's rejecting-fallback posture (see
// RejectingAuthenticator) reach the wire as a 401.
func (s *Server) verifyToken(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
	identity, err := s.mcpAuth.Authenticate(ctx, "Bearer "+token)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", sdkauth.ErrInvalidToken, err.Error())
	}

	return &sdkauth.TokenInfo{
		UserID:     identity.MemberID.String(),
		Expiration: time.Now().Add(tokenInfoTTL),
		Extra:      map[string]any{identityExtraKey: identity},
	}, nil
}

// identityFromContext recovers the CallerIdentity verifyToken attached to the
// current request's auth.TokenInfo. ok is false only if the auth middleware
// was bypassed (must never happen when Handler is used as constructed).
func identityFromContext(ctx context.Context) (api.CallerIdentity, bool) {
	info := sdkauth.TokenInfoFromContext(ctx)
	if info == nil {
		return api.CallerIdentity{}, false //nolint:exhaustruct // ok=false zero-value fallback, mirrors transport's own CallerIdentity{} error-path returns
	}

	identity, ok := info.Extra[identityExtraKey].(api.CallerIdentity)

	return identity, ok
}
