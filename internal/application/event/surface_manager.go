// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

type surfaceStore interface {
	Create(ctx context.Context, surface model.ConversationSurface) (created bool, err error)
	ByConversationProvider(ctx context.Context, conversationID uuid.UUID, provider string) (model.ConversationSurface, error)
	AddCoveredMember(ctx context.Context, surfaceID, memberID uuid.UUID) error
	Archive(ctx context.Context, surfaceID uuid.UUID) (archived bool, err error)
}

type surfaceAnchorReader interface {
	ConfiguredProvidersWithAlertChannel(ctx context.Context, projectID uuid.UUID) ([]service.ProviderAlertChannel, error)
}

type surfaceProviderResolver interface {
	ForProjectKind(ctx context.Context, projectID uuid.UUID, kind provider.ProviderKind) (service.Provider, error)
}

type surfaceConversationReader interface {
	ConversationByID(ctx context.Context, id uuid.UUID) (model.Conversation, error)
}

// SurfaceManager manages native conversation surfaces.
type SurfaceManager struct {
	surfaces    surfaceStore
	members     memberByIDReader
	convs       surfaceConversationReader
	anchors     surfaceAnchorReader
	providers   surfaceProviderResolver
	attachments fanoutAttachmentReader `exhaustruct:"optional"`
	blobs       fanoutSigner           `exhaustruct:"optional"`
	logger      *slog.Logger
}

// NewSurfaceManager builds a SurfaceManager.
func NewSurfaceManager(
	surfaces surfaceStore,
	members memberByIDReader,
	convs surfaceConversationReader,
	anchors surfaceAnchorReader,
	providers surfaceProviderResolver,
	attachments fanoutAttachmentReader,
	blobs fanoutSigner,
	logger *slog.Logger,
) *SurfaceManager {
	return &SurfaceManager{
		surfaces:    surfaces,
		members:     members,
		convs:       convs,
		anchors:     anchors,
		providers:   providers,
		attachments: attachments,
		blobs:       blobs,
		logger:      logger,
	}
}

// EnsureDiscordThread returns members covered by the conversation thread.
func (m *SurfaceManager) EnsureDiscordThread(
	ctx context.Context,
	projectID, conversationID uuid.UUID,
	memberIDs []uuid.UUID,
) map[uuid.UUID]bool {
	if m == nil {
		return nil
	}

	if surface, err := m.surfaces.ByConversationProvider(ctx, conversationID, model.SurfaceProviderDiscord); err == nil {
		return coveredSet(surface)
	} else if !errors.Is(err, adaptererr.ErrNotFound) {
		m.logger.WarnContext(ctx, "surface: resolving existing surface",
			slog.String("conversation_id", conversationID.String()), slog.Any("error", err))

		return nil
	}

	anchor := m.discordAnchor(ctx, projectID)
	if anchor == "" {
		return nil // no project channel to anchor threads in: DM fallback for all.
	}

	surfacer, poster := m.discordSurfacer(ctx, projectID)
	if surfacer == nil {
		return nil
	}

	conv, err := m.convs.ConversationByID(ctx, conversationID)
	if err != nil {
		m.logger.WarnContext(ctx, "surface: resolving conversation",
			slog.String("conversation_id", conversationID.String()), slog.Any("error", err))

		return nil
	}

	return m.openThread(ctx, surfacer, poster, conv, anchor, memberIDs)
}

func (m *SurfaceManager) openThread(
	ctx context.Context,
	surfacer service.ThreadSurfacer,
	poster service.ChannelPoster,
	conv model.Conversation,
	anchor string,
	memberIDs []uuid.UUID,
) map[uuid.UUID]bool {
	threadID, err := surfacer.CreateThread(ctx, anchor, conv.DisplayTitle())
	if err != nil {
		m.logger.WarnContext(ctx, "surface: creating thread; falling back to DMs",
			slog.String("conversation_id", conv.ID.String()), slog.Any("error", err))

		return nil
	}

	covered := m.inviteMembers(ctx, surfacer, threadID, memberIDs)
	if len(covered) == 0 {
		return nil
	}

	surface := model.ConversationSurface{
		ID:               uuid.New(),
		ConversationID:   conv.ID,
		Provider:         model.SurfaceProviderDiscord,
		Kind:             model.SurfaceKindThread,
		ChannelID:        anchor,
		ThreadID:         threadID,
		CoveredMemberIDs: covered,
	}

	created, err := m.surfaces.Create(ctx, surface)
	if err != nil {
		m.logger.WarnContext(ctx, "surface: persisting surface; falling back to DMs",
			slog.String("conversation_id", conv.ID.String()), slog.Any("error", err))

		return nil
	}

	if !created {
		if winner, err := m.surfaces.ByConversationProvider(ctx, conv.ID, model.SurfaceProviderDiscord); err == nil {
			return coveredSet(winner)
		}

		return nil
	}

	if poster != nil {
		m.postOpeningQuestion(ctx, poster, threadID, conv)
	}

	return coveredSet(surface)
}

func (m *SurfaceManager) postOpeningQuestion(ctx context.Context, poster service.ChannelPoster, threadID string, conv model.Conversation) {
	text := service.FormatQuestion(conv.Question, conv.Context, "")

	atts := signConversationAttachments(ctx, m.attachments, m.blobs, conv.ID, m.logger)
	filePoster, canFiles := poster.(service.ChannelAttachmentPoster)

	if len(atts) > 0 && canFiles {
		if _, err := filePoster.PostChannelWithFiles(ctx, threadID, text, atts); err != nil {
			m.logger.WarnContext(ctx, "surface: posting opening question with files",
				slog.String("conversation_id", conv.ID.String()), slog.Any("error", err))
		}

		return
	}

	if _, err := poster.PostChannel(ctx, threadID, text); err != nil {
		m.logger.WarnContext(ctx, "surface: posting opening question",
			slog.String("conversation_id", conv.ID.String()), slog.Any("error", err))
	}
}

// CoverParticipant adds a participant to an existing Discord thread.
func (m *SurfaceManager) CoverParticipant(ctx context.Context, conversationID, memberID uuid.UUID) bool {
	if m == nil {
		return false
	}

	surface, err := m.surfaces.ByConversationProvider(ctx, conversationID, model.SurfaceProviderDiscord)
	if err != nil {
		if !errors.Is(err, adaptererr.ErrNotFound) {
			m.logger.WarnContext(ctx, "surface: resolving surface for participant",
				slog.String("conversation_id", conversationID.String()), slog.Any("error", err))
		}

		return false
	}

	if surface.ArchivedAt != nil {
		return false
	}

	if surface.Covers(memberID) {
		return true
	}

	member, err := m.members.ByID(ctx, memberID)
	if err != nil || member.DiscordUserID == "" {
		return false
	}

	conv, err := m.convs.ConversationByID(ctx, conversationID)
	if err != nil {
		return false
	}

	surfacer, _ := m.discordSurfacer(ctx, conv.ProjectID)
	if surfacer == nil {
		return false
	}

	if err := surfacer.AddThreadMember(ctx, surface.ThreadID, member.DiscordUserID); err != nil {
		m.logger.WarnContext(ctx, "surface: inviting participant to thread; falling back to DM",
			slog.String("member_id", memberID.String()), slog.Any("error", err))

		return false
	}

	if err := m.surfaces.AddCoveredMember(ctx, surface.ID, memberID); err != nil {
		m.logger.WarnContext(ctx, "surface: recording covered participant",
			slog.String("member_id", memberID.String()), slog.Any("error", err))
	}

	return true
}

// SurfaceFor resolves the conversation's Discord surface.
func (m *SurfaceManager) SurfaceFor(ctx context.Context, conversationID uuid.UUID) (model.ConversationSurface, bool) {
	if m == nil {
		return model.ConversationSurface{}, false //nolint:exhaustruct // zero value on the not-ok arm
	}

	surface, err := m.surfaces.ByConversationProvider(ctx, conversationID, model.SurfaceProviderDiscord)
	if err != nil {
		if !errors.Is(err, adaptererr.ErrNotFound) {
			m.logger.WarnContext(ctx, "surface: resolving surface",
				slog.String("conversation_id", conversationID.String()), slog.Any("error", err))
		}

		return model.ConversationSurface{}, false //nolint:exhaustruct // zero value on the not-ok arm
	}

	return surface, true
}

// PostToSurface mirrors a message onto the surface thread.
func (m *SurfaceManager) PostToSurface(ctx context.Context, projectID uuid.UUID, surface model.ConversationSurface, authorLabel, body string, atts []service.OutboundAttachment) {
	if m == nil || surface.ArchivedAt != nil {
		return
	}

	prov, err := m.providers.ForProjectKind(ctx, projectID, provider.ProviderKind(surface.Provider))
	if err != nil {
		if !errors.Is(err, service.ErrNoProvider) {
			m.logger.WarnContext(ctx, "surface: resolving provider for post",
				slog.String("project_id", projectID.String()), slog.Any("error", err))
		}

		return
	}

	if len(atts) > 0 && m.postWithFiles(ctx, prov, surface, authorLabel, body, atts) {
		return
	}

	if authorLabel != "" {
		if identity, ok := prov.(service.IdentityPoster); ok {
			if err := identity.PostAsIdentity(ctx, surface.ChannelID, surface.ThreadID, authorLabel, body); err == nil {
				return
			}

			m.logger.WarnContext(ctx, "surface: identity post failed; falling back to plain bot post",
				slog.String("thread_id", surface.ThreadID))
		}
	}

	poster, ok := prov.(service.ChannelPoster)
	if !ok {
		return
	}

	text := body
	if authorLabel != "" {
		text = surfaceRelayText(authorLabel, body)
	}

	if _, err := poster.PostChannel(ctx, surface.ThreadID, text); err != nil {
		m.logger.WarnContext(ctx, "surface: posting to thread",
			slog.String("thread_id", surface.ThreadID), slog.Any("error", err))
	}
}

func (m *SurfaceManager) postWithFiles(ctx context.Context, prov service.Provider, surface model.ConversationSurface, authorLabel, body string, atts []service.OutboundAttachment) bool {
	if authorLabel != "" {
		if identity, ok := prov.(service.IdentityAttachmentPoster); ok {
			if err := identity.PostAsIdentityWithFiles(ctx, surface.ChannelID, surface.ThreadID, authorLabel, body, atts); err == nil {
				return true
			}

			m.logger.WarnContext(ctx, "surface: identity file post failed; trying plain file post",
				slog.String("thread_id", surface.ThreadID))
		}
	}

	poster, ok := prov.(service.ChannelAttachmentPoster)
	if !ok {
		return false
	}

	text := body
	if authorLabel != "" {
		text = surfaceRelayText(authorLabel, body)
	}

	if _, err := poster.PostChannelWithFiles(ctx, surface.ThreadID, text, atts); err != nil {
		m.logger.WarnContext(ctx, "surface: file post to thread failed",
			slog.String("thread_id", surface.ThreadID), slog.Any("error", err))

		return false
	}

	return true
}

// CloseSurface posts the resolution and archives the thread.
func (m *SurfaceManager) CloseSurface(ctx context.Context, projectID, conversationID uuid.UUID, resolutionText string) {
	if m == nil {
		return
	}

	surface, ok := m.SurfaceFor(ctx, conversationID)
	if !ok || surface.ArchivedAt != nil {
		return
	}

	archived, err := m.surfaces.Archive(ctx, surface.ID)
	if err != nil {
		m.logger.WarnContext(ctx, "surface: archiving surface row",
			slog.String("conversation_id", conversationID.String()), slog.Any("error", err))

		return
	}

	if !archived {
		return // a concurrent close already handled the thread.
	}

	surfacer, poster := m.discordSurfacer(ctx, projectID)

	if poster != nil && resolutionText != "" {
		if _, err := poster.PostChannel(ctx, surface.ThreadID, resolutionText); err != nil {
			m.logger.WarnContext(ctx, "surface: posting resolution to thread",
				slog.String("thread_id", surface.ThreadID), slog.Any("error", err))
		}
	}

	if surfacer != nil {
		if err := surfacer.ArchiveThread(ctx, surface.ThreadID); err != nil {
			m.logger.WarnContext(ctx, "surface: archiving thread",
				slog.String("thread_id", surface.ThreadID), slog.Any("error", err))
		}
	}
}

// DeleteSurface deletes the conversation's platform thread.
func (m *SurfaceManager) DeleteSurface(ctx context.Context, projectID, conversationID uuid.UUID) {
	if m == nil {
		return
	}

	surface, ok := m.SurfaceFor(ctx, conversationID)
	if !ok {
		return
	}

	surfacer, _ := m.discordSurfacer(ctx, projectID)
	if surfacer == nil {
		return
	}

	if err := surfacer.DeleteThread(ctx, surface.ThreadID); err != nil {
		m.logger.WarnContext(ctx, "surface: deleting thread",
			slog.String("thread_id", surface.ThreadID), slog.Any("error", err))
	}
}

func (m *SurfaceManager) inviteMembers(
	ctx context.Context,
	surfacer service.ThreadSurfacer,
	threadID string,
	memberIDs []uuid.UUID,
) []uuid.UUID {
	var covered []uuid.UUID

	seen := map[uuid.UUID]bool{uuid.Nil: true}

	for _, id := range memberIDs {
		if seen[id] {
			continue
		}

		seen[id] = true

		member, err := m.members.ByID(ctx, id)
		if err != nil || member.DiscordUserID == "" {
			continue
		}

		if err := surfacer.AddThreadMember(ctx, threadID, member.DiscordUserID); err != nil {
			m.logger.WarnContext(ctx, "surface: inviting member to thread; they keep the DM path",
				slog.String("member_id", id.String()), slog.Any("error", err))

			continue
		}

		covered = append(covered, id)
	}

	return covered
}

func (m *SurfaceManager) discordAnchor(ctx context.Context, projectID uuid.UUID) string {
	rows, err := m.anchors.ConfiguredProvidersWithAlertChannel(ctx, projectID)
	if err != nil {
		m.logger.WarnContext(ctx, "surface: resolving anchor channel",
			slog.String("project_id", projectID.String()), slog.Any("error", err))

		return ""
	}

	for _, row := range rows {
		if row.Kind == model.SurfaceProviderDiscord && len(row.AlertChannelIDs) > 0 {
			return row.AlertChannelIDs[0]
		}
	}

	return ""
}

func (m *SurfaceManager) discordSurfacer(ctx context.Context, projectID uuid.UUID) (service.ThreadSurfacer, service.ChannelPoster) {
	prov, err := m.providers.ForProjectKind(ctx, projectID, provider.ProviderKindDiscord)
	if err != nil {
		if !errors.Is(err, service.ErrNoProvider) {
			m.logger.WarnContext(ctx, "surface: resolving discord provider",
				slog.String("project_id", projectID.String()), slog.Any("error", err))
		}

		return nil, nil
	}

	surfacer, _ := prov.(service.ThreadSurfacer)
	poster, _ := prov.(service.ChannelPoster)

	return surfacer, poster
}

func coveredSet(surface model.ConversationSurface) map[uuid.UUID]bool {
	if surface.ArchivedAt != nil {
		return nil
	}

	out := make(map[uuid.UUID]bool, len(surface.CoveredMemberIDs))
	for _, id := range surface.CoveredMemberIDs {
		out[id] = true
	}

	return out
}

func surfaceRelayText(authorName, body string) string {
	return fmt.Sprintf("💬 %s:\n%s", authorName, body)
}
