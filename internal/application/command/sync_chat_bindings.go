// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/errs"
)

// slackBackfillReader lists the project members still missing a Slack binding
// and reads/updates a member. *identity.MemberStore satisfies it.
type slackBackfillReader interface {
	MembersMissingSlackBinding(ctx context.Context, projectID uuid.UUID) ([]repository.MemberEmail, error)
	ByID(ctx context.Context, id uuid.UUID) (model.Member, error)
	Update(ctx context.Context, member model.Member) error
}

// SyncChatBindingsCommand backfills Slack ids for a project's already-invited
// members by resolving each unbound member's email against the org's Slack
// workspace.
type SyncChatBindingsCommand struct {
	ProjectID uuid.UUID
}

// SyncChatBindingsResult reports how many members were scanned and newly bound.
type SyncChatBindingsResult struct {
	Scanned int
	Bound   int
}

// SyncChatBindingsHandler handles SyncChatBindingsCommand.
type SyncChatBindingsHandler struct {
	members   slackBackfillReader
	directory chatDirectoryResolver `exhaustruct:"optional"`
}

// MustNewSyncChatBindingsHandler builds a handler. A nil directory disables the
// backfill (the command becomes a no-op returning zero counts).
func MustNewSyncChatBindingsHandler(members slackBackfillReader, directory chatDirectoryResolver) SyncChatBindingsHandler {
	if members == nil {
		panic("SyncChatBindingsHandler requires a non-nil member store")
	}

	return SyncChatBindingsHandler{members: members, directory: directory}
}

// Handle resolves every unbound member's email against the org Slack workspace
// and stores the id on a hit. Best-effort per member: a lookup miss or a single
// update failure is skipped, never failing the whole sync.
func (h SyncChatBindingsHandler) Handle(ctx context.Context, cmd SyncChatBindingsCommand) (SyncChatBindingsResult, error) {
	if h.directory == nil {
		return SyncChatBindingsResult{Scanned: 0, Bound: 0}, nil
	}

	missing, err := h.members.MembersMissingSlackBinding(ctx, cmd.ProjectID)
	if err != nil {
		return SyncChatBindingsResult{Scanned: 0, Bound: 0}, errs.InternalError{Err: fmt.Errorf("listing unbound members: %w", err)}
	}

	result := SyncChatBindingsResult{Scanned: len(missing), Bound: 0}

	for _, me := range missing {
		slackID, lookErr := h.directory.LookupSlackByEmail(ctx, cmd.ProjectID, me.Email)
		if lookErr != nil || slackID == "" {
			continue
		}

		member, getErr := h.members.ByID(ctx, me.ID)
		if getErr != nil {
			continue
		}

		member.SlackUserID = slackID
		if updErr := h.members.Update(ctx, member); updErr != nil {
			slog.WarnContext(ctx, "sync_chat_bindings: updating member", slog.String("member_id", me.ID.String()), slog.Any("error", updErr))

			continue
		}

		result.Bound++
	}

	return result, nil
}
