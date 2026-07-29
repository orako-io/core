// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
	"github.com/orako-io/core/internal/pkg/errs"
)

// HeartbeatCommand is the input for refreshing a member's connection-presence.
type HeartbeatCommand struct {
	// MemberID identifies the participant sending the heartbeat.
	MemberID uuid.UUID
	// ProjectID is used for routing the PresenceChanged event.
	ProjectID uuid.UUID
	// Online indicates whether the member is currently connected.
	Online bool
}

// HeartbeatHandler handles HeartbeatCommand.
type HeartbeatHandler struct {
	presenceRepo repository.PresenceRepository
	bus          eventBus
}

// MustNewHeartbeatHandler builds a handler. It panics on nil
// dependencies.
func MustNewHeartbeatHandler(
	presenceRepo repository.PresenceRepository,
	bus eventBus,
) HeartbeatHandler {
	if presenceRepo == nil {
		panic("HeartbeatHandler requires a non-nil PresenceRepository")
	}

	if bus == nil {
		panic("HeartbeatHandler requires a non-nil eventBus")
	}

	return HeartbeatHandler{presenceRepo: presenceRepo, bus: bus}
}

// Handle upserts the member's presence record and emits PresenceChanged.
func (h HeartbeatHandler) Handle(ctx context.Context, cmd HeartbeatCommand) error {
	now := time.Now().UTC()

	presence, err := model.NewPresence(cmd.MemberID, cmd.Online, now)
	if err != nil {
		return err
	}

	if err = h.presenceRepo.Upsert(ctx, presence); err != nil {
		return translateErr(err, "presence")
	}

	if _, err = h.bus.Publish(ctx, &orakov1.Envelope{
		ProjectId: cmd.ProjectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_PRESENCE_CHANGED,
		Payload: &orakov1.Envelope_PresenceChanged{
			PresenceChanged: &orakov1.PresenceChanged{
				MemberId: cmd.MemberID.String(),
				Online:   cmd.Online,
			},
		},
	}); err != nil {
		return errs.InternalError{Err: fmt.Errorf("publishing presence_changed: %w", err)}
	}

	return nil
}
