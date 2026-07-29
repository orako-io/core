// SPDX-License-Identifier: AGPL-3.0-or-later

package teams_test

import (
	"context"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
)

// fakeMemberStore is an in-memory memberLookup for tests.
type fakeMemberStore struct {
	byID    map[uuid.UUID]model.Member
	byTeams map[string]model.Member
}

func newFakeMemberStore() *fakeMemberStore {
	return &fakeMemberStore{byID: make(map[uuid.UUID]model.Member), byTeams: make(map[string]model.Member)}
}

func (f *fakeMemberStore) add(m model.Member) {
	f.byID[m.ID] = m

	if m.TeamsUserID != "" {
		f.byTeams[m.TeamsUserID] = m
	}
}

func (f *fakeMemberStore) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	m, ok := f.byID[id]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

func (f *fakeMemberStore) ByTeamsUserID(_ context.Context, teamsUserID string) (model.Member, error) {
	m, ok := f.byTeams[teamsUserID]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

// fakeLedger is an in-memory ledgerReader for tests.
type fakeLedger struct {
	byRef     map[[2]string]model.ProviderMessage
	byChannel map[string]model.ProviderMessage
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{byRef: make(map[[2]string]model.ProviderMessage), byChannel: make(map[string]model.ProviderMessage)}
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
