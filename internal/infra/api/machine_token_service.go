// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"google.golang.org/protobuf/types/known/timestamppb"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/infra/api/oauth"
)

// machineTokenCreator mints a machine token. *command.CreateMachineTokenHandler
// satisfies it.
type machineTokenCreator interface {
	Handle(ctx context.Context, cmd command.CreateMachineTokenCommand) (command.CreateMachineTokenResult, error)
}

// machineTokenViewLister reads the caller's minted machine tokens.
// *query.ListMachineTokensHandler satisfies it.
type machineTokenViewLister interface {
	Handle(ctx context.Context, q query.ListMachineTokensQuery) ([]query.MachineTokenView, error)
}

// machineTokenRevokerHandler revokes one of the caller's machine tokens.
// *command.RevokeMachineTokenHandler satisfies it.
type machineTokenRevokerHandler interface {
	Handle(ctx context.Context, cmd command.RevokeMachineTokenCommand) error
}

// MachineTokenGateway adapts *oauth.Store to the narrow ports
// command.CreateMachineTokenHandler and query.ListMachineTokensHandler need,
// translating between oauth.Store's transport-layer Token/MachineToken types
// and the application layer's own DTOs — application/{command,query} never
// import this transport package directly, only through this adapter.
// resource is the canonical MCP resource URL ({base}/mcp) every minted
// machine token is bound to, exactly like an OAuth-flow access token (see
// MCPTokenAuthenticator's confused-deputy guard).
type MachineTokenGateway struct {
	store    *oauth.Store
	resource string
	now      func() time.Time
}

// NewMachineTokenGateway builds a MachineTokenGateway backed by store,
// minting tokens for resource — the canonical MCP resource URL
// ({base}/mcp). Exported for cmd/orako-server's composition root.
func NewMachineTokenGateway(store *oauth.Store, resource string) *MachineTokenGateway {
	return &MachineTokenGateway{store: store, resource: resource, now: time.Now}
}

// MintMachineToken satisfies command's machineTokenMinter port.
func (g *MachineTokenGateway) MintMachineToken(ctx context.Context, orgID, memberID uuid.UUID, projectIDs []uuid.UUID, label string) (command.MintedMachineToken, error) {
	secret, tok, err := oauth.MintMachineToken(ctx, g.store, g.now(), orgID, memberID, g.resource, projectIDs, label)
	if err != nil {
		return command.MintedMachineToken{}, err
	}

	return command.MintedMachineToken{
		ID:        tok.ID,
		Secret:    secret,
		CreatedAt: tok.CreatedAt,
		ExpiresAt: tok.ExpiresAt,
	}, nil
}

// ListMachineTokens satisfies query's machineTokenLister port.
func (g *MachineTokenGateway) ListMachineTokens(ctx context.Context, orgID uuid.UUID) ([]query.MachineTokenView, error) {
	rows, err := g.store.ListMachineTokens(ctx, orgID)
	if err != nil {
		return nil, err
	}

	views := make([]query.MachineTokenView, len(rows))
	for i, r := range rows {
		views[i] = query.MachineTokenView{
			ID:         r.ID,
			Label:      r.Label,
			ProjectIDs: r.ProjectIDs,
			CreatedAt:  r.CreatedAt,
			ExpiresAt:  r.ExpiresAt,
			LastUsedAt: r.LastUsedAt,
			RevokedAt:  r.RevokedAt,
		}
	}

	return views, nil
}

// RevokeMachineToken revokes a token within its owning organization.
func (g *MachineTokenGateway) RevokeMachineToken(ctx context.Context, orgID, tokenID uuid.UUID) error {
	return g.store.RevokeMachineToken(ctx, orgID, tokenID)
}

// CreateMachineToken mints a durable, non-interactive access token for a
// headless agent (org-admin gated in the command handler, and by the
// interceptor's admin-procedure list). The raw secret is returned exactly
// once, here.
func (s *Server) CreateMachineToken(
	ctx context.Context,
	req *connect.Request[orakov1.CreateMachineTokenRequest],
) (*connect.Response[orakov1.CreateMachineTokenResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	projectIDs, err := parseProjectIDs(req.Msg.GetProjectIds())
	if err != nil {
		return nil, err
	}

	result, err := s.createMachineToken.Handle(ctx, command.CreateMachineTokenCommand{
		MemberID:   caller.MemberID,
		OrgID:      caller.OrgID,
		IsOrgAdmin: caller.IsOrgAdmin,
		Label:      req.Msg.GetLabel(),
		ProjectIDs: projectIDs,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.CreateMachineTokenResponse{
		Token:  pbMachineToken(result.ID, result.Label, result.ProjectIDs, result.CreatedAt, result.ExpiresAt, nil, nil),
		Secret: result.Secret,
	}), nil
}

// ListMachineTokens returns the caller's minted machine tokens: metadata
// only, never the secret or its hash (org-admin gated).
func (s *Server) ListMachineTokens(
	ctx context.Context,
	_ *connect.Request[orakov1.ListMachineTokensRequest],
) (*connect.Response[orakov1.ListMachineTokensResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	views, err := s.listMachineTokens.Handle(ctx, query.ListMachineTokensQuery{
		OrgID:      caller.OrgID,
		IsOrgAdmin: caller.IsOrgAdmin,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	tokens := make([]*orakov1.MachineToken, len(views))
	for i, v := range views {
		tokens[i] = pbMachineToken(v.ID, v.Label, v.ProjectIDs, v.CreatedAt, v.ExpiresAt, v.LastUsedAt, v.RevokedAt)
	}

	return connect.NewResponse(&orakov1.ListMachineTokensResponse{Tokens: tokens}), nil
}

// RevokeMachineToken immediately invalidates one of the caller's machine
// tokens (org-admin gated). Idempotent.
func (s *Server) RevokeMachineToken(
	ctx context.Context,
	req *connect.Request[orakov1.RevokeMachineTokenRequest],
) (*connect.Response[orakov1.RevokeMachineTokenResponse], error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller identity in context"))
	}

	id, err := parseUUID(req.Msg.GetId(), "id")
	if err != nil {
		return nil, err
	}

	if err := s.revokeMachineToken.Handle(ctx, command.RevokeMachineTokenCommand{
		OrgID:      caller.OrgID,
		IsOrgAdmin: caller.IsOrgAdmin,
		TokenID:    id,
	}); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&orakov1.RevokeMachineTokenResponse{}), nil
}

// pbMachineToken maps a machine token's metadata to its wire view (never the
// secret or its hash).
func pbMachineToken(id uuid.UUID, label string, projectIDs []uuid.UUID, createdAt, expiresAt time.Time, lastUsedAt, revokedAt *time.Time) *orakov1.MachineToken {
	pb := &orakov1.MachineToken{
		Id:         id.String(),
		Label:      label,
		ProjectIds: make([]string, len(projectIDs)),
		CreatedAt:  timestamppb.New(createdAt),
		ExpiresAt:  timestamppb.New(expiresAt),
	}

	for i, pid := range projectIDs {
		pb.ProjectIds[i] = pid.String()
	}

	if lastUsedAt != nil {
		pb.LastUsedAt = timestamppb.New(*lastUsedAt)
	}

	if revokedAt != nil {
		pb.RevokedAt = timestamppb.New(*revokedAt)
	}

	return pb
}
