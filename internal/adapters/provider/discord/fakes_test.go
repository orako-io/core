// SPDX-License-Identifier: AGPL-3.0-or-later

package discord_test

import (
	"context"
	"errors"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
)

// fakeMemberStore is an in-memory member lookup for tests.
type fakeMemberStore struct {
	byID      map[uuid.UUID]model.Member
	byDiscord map[string]model.Member
}

func newFakeMemberStore() *fakeMemberStore {
	return &fakeMemberStore{byID: make(map[uuid.UUID]model.Member), byDiscord: make(map[string]model.Member)}
}

func (f *fakeMemberStore) add(m model.Member) {
	f.byID[m.ID] = m

	if m.DiscordUserID != "" {
		f.byDiscord[m.DiscordUserID] = m
	}
}

func (f *fakeMemberStore) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	m, ok := f.byID[id]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

func (f *fakeMemberStore) ByDiscordUserID(_ context.Context, discordUserID string) (model.Member, error) {
	m, ok := f.byDiscord[discordUserID]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

// fakeLedger is an in-memory ledgerReader for tests.
// fakeSurfaces is a gatewaySurfaceReader fake keyed by thread id.
type fakeSurfaces struct {
	byThread map[string]model.ConversationSurface
}

func newFakeSurfaces() *fakeSurfaces {
	return &fakeSurfaces{byThread: map[string]model.ConversationSurface{}}
}

func (f *fakeSurfaces) ByThread(_ context.Context, _, threadID string) (model.ConversationSurface, error) {
	if s, ok := f.byThread[threadID]; ok {
		return s, nil
	}

	return model.ConversationSurface{}, errors.New("surface not found")
}

type fakeLedger struct {
	byRef     map[[2]string]model.ProviderMessage
	byChannel map[string]model.ProviderMessage
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{
		byRef:     make(map[[2]string]model.ProviderMessage),
		byChannel: make(map[string]model.ProviderMessage),
	}
}

func (f *fakeLedger) add(channelID, messageRef string, msg model.ProviderMessage) {
	f.byRef[[2]string{channelID, messageRef}] = msg
	f.byChannel[channelID] = msg
}

func (f *fakeLedger) ByProviderRef(_ context.Context, channelID, messageRef string) (model.ProviderMessage, error) {
	m, ok := f.byRef[[2]string{channelID, messageRef}]
	if !ok {
		return model.ProviderMessage{}, adaptererr.ErrNotFound
	}

	return m, nil
}

func (f *fakeLedger) LatestByChannel(_ context.Context, channelID string) (model.ProviderMessage, error) {
	m, ok := f.byChannel[channelID]
	if !ok {
		return model.ProviderMessage{}, adaptererr.ErrNotFound
	}

	return m, nil
}

// fakeFollowUpper is a followUpper test double. FollowUp always succeeds with
// OutcomeAppended (the only outcome since the claim/second-opinion teardown).
type fakeFollowUpper struct {
	last  command.FollowUpCommand
	calls []command.FollowUpCommand
}

func (f *fakeFollowUpper) Handle(_ context.Context, cmd command.FollowUpCommand) (command.FollowUpResult, error) {
	f.last = cmd
	f.calls = append(f.calls, cmd)

	return command.FollowUpResult{Outcome: command.OutcomeAppended}, nil
}

// gatedFollowUpper is a followUpper test double that blocks inside Handle
// until release is closed, closing started the moment it is entered. Used to
// prove Gateway.Close waits for an in-flight handler invocation (review
// finding #10) and to capture the ctx.Err() the handler observed once
// released, proving the lifecycle context propagates cancellation into
// in-flight work. Single-call only.
type gatedFollowUpper struct {
	release chan struct{}
	started chan struct{}
	ctxErr  error
}

func newGatedFollowUpper() *gatedFollowUpper {
	return &gatedFollowUpper{release: make(chan struct{}), started: make(chan struct{})}
}

func (f *gatedFollowUpper) Handle(ctx context.Context, _ command.FollowUpCommand) (command.FollowUpResult, error) {
	close(f.started)
	<-f.release

	f.ctxErr = ctx.Err()

	return command.FollowUpResult{}, nil
}
