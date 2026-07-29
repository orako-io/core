// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/errs"
)

const fieldKind = "kind"

const providerTestText = "✅ Orako test — if you can read this, delivery works. You can safely ignore this message."

// projectProviderResolver resolves the live provider for a project + kind. A
// thin adapter over *provider.Registry.ForProjectKind is wired at composition,
// so the command layer never imports the adapter package.
type projectProviderResolver interface {
	ForProjectKind(ctx context.Context, projectID uuid.UUID, kind string) (service.Provider, error)
}

// SendProviderTestCommand runs a real delivery test for one provider.
type SendProviderTestCommand struct {
	ProjectID uuid.UUID
	MemberID  uuid.UUID
	Kind      string
}

// ProviderTestResult is one target's real delivery outcome.
type ProviderTestResult struct {
	Label string
	OK    bool
	Error string `exhaustruct:"optional"`
}

// SendProviderTestHandler handles SendProviderTestCommand.
type SendProviderTestHandler struct {
	providers projectProviderResolver
	channels  service.ProviderAlertChannelsReader
}

// MustNewSendProviderTestHandler builds a handler.
func MustNewSendProviderTestHandler(providers projectProviderResolver, channels service.ProviderAlertChannelsReader) SendProviderTestHandler {
	if providers == nil || channels == nil {
		panic("SendProviderTestHandler requires non-nil dependencies")
	}

	return SendProviderTestHandler{providers: providers, channels: channels}
}

// Handle DMs the caller and posts to each configured alert channel, returning a
// real per-target result. A delivery error never fails the request — that error
// is exactly the signal an admin needs (e.g. "the bot is not in that server").
func (h SendProviderTestHandler) Handle(ctx context.Context, cmd SendProviderTestCommand) ([]ProviderTestResult, error) {
	prov, err := h.providers.ForProjectKind(ctx, cmd.ProjectID, cmd.Kind)
	if err != nil {
		return nil, errs.InvalidError{Field: fieldKind, Reason: "no " + cmd.Kind + " provider is configured for this project — save its credentials first"}
	}

	results := make([]ProviderTestResult, 0, 2)

	// DM the caller: exercises the primary path — the bot shares a server with
	// them, they allow DMs, and their own ID is bound to their member.
	_, dmErr := prov.Deliver(ctx, service.OutboundMessage{
		ProjectID:         cmd.ProjectID,
		ConversationID:    uuid.New(), // throwaway: a test DM has no real conversation
		ResponderMemberID: cmd.MemberID,
		Kind:              service.MessageKindQuestion,
		Question:          providerTestText,
	})
	results = append(results, ProviderTestResult{Label: "Direct message to you", OK: dmErr == nil, Error: testErrString(dmErr)})

	// Post to each configured alert channel: the bot must be in that channel's
	// server with access. A wrong channel or an un-invited bot fails here.
	poster, canPost := prov.(service.ChannelPoster)
	for _, ch := range h.alertChannelsFor(ctx, cmd.ProjectID, cmd.Kind) {
		if !canPost {
			results = append(results, ProviderTestResult{Label: "Alert channel " + ch, OK: false, Error: "this provider cannot post to channels"})

			continue
		}

		_, perr := poster.PostChannel(ctx, ch, providerTestText)
		results = append(results, ProviderTestResult{Label: "Alert channel " + ch, OK: perr == nil, Error: testErrString(perr)})
	}

	return results, nil
}

func (h SendProviderTestHandler) alertChannelsFor(ctx context.Context, projectID uuid.UUID, kind string) []string {
	configured, err := h.channels.ConfiguredProvidersWithAlertChannel(ctx, projectID)
	if err != nil {
		return nil
	}

	for _, c := range configured {
		if c.Kind == kind {
			return c.AlertChannelIDs
		}
	}

	return nil
}

func testErrString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
