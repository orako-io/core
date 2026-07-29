// SPDX-License-Identifier: AGPL-3.0-or-later

package slack_test

import (
	"context"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
)

// threadKey is the composite lookup key for fakeConvStore.
type threadKey struct {
	channelID string
	threadTS  string
}

// fakeMemberStore is an in-memory memberLookup for tests.
type fakeMemberStore struct {
	byID      map[uuid.UUID]model.Member
	bySlackID map[string]model.Member
}

func newFakeMemberStore() *fakeMemberStore {
	return &fakeMemberStore{
		byID:      make(map[uuid.UUID]model.Member),
		bySlackID: make(map[string]model.Member),
	}
}

func (f *fakeMemberStore) add(m model.Member) {
	f.byID[m.ID] = m

	if m.SlackUserID != "" {
		f.bySlackID[m.SlackUserID] = m
	}
}

func (f *fakeMemberStore) ByID(_ context.Context, id uuid.UUID) (model.Member, error) {
	m, ok := f.byID[id]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

func (f *fakeMemberStore) BySlackUserID(_ context.Context, slackUserID string) (model.Member, error) {
	m, ok := f.bySlackID[slackUserID]
	if !ok {
		return model.Member{}, adaptererr.ErrNotFound
	}

	return m, nil
}

// fakeConvStore is an in-memory conversationAccess for tests.
type fakeConvStore struct {
	byThread map[threadKey]model.Conversation
	// threads records calls to SetSlackThread; key is conversation ID.
	threads map[uuid.UUID][2]string
}

func newFakeConvStore() *fakeConvStore {
	return &fakeConvStore{
		byThread: make(map[threadKey]model.Conversation),
		threads:  make(map[uuid.UUID][2]string),
	}
}

func (f *fakeConvStore) addConversation(channelID, threadTS string, conv model.Conversation) {
	f.byThread[threadKey{channelID, threadTS}] = conv
}

func (f *fakeConvStore) ConversationBySlackThread(_ context.Context, channelID, threadTS string) (model.Conversation, error) {
	c, ok := f.byThread[threadKey{channelID, threadTS}]
	if !ok {
		return model.Conversation{}, adaptererr.ErrNotFound
	}

	return c, nil
}

func (f *fakeConvStore) SetSlackThread(_ context.Context, id uuid.UUID, channelID, threadTS string) error {
	f.threads[id] = [2]string{channelID, threadTS}
	return nil
}

// ledgerKey is the composite lookup key for fakeLedger.
type ledgerKey struct {
	channelID  string
	messageRef string
}

// fakeLedger is an in-memory ledgerReader for tests: correlates (channel,
// message ref) → conversation, the pool-DM fallback path.
type fakeLedger struct {
	byRef map[ledgerKey]model.ProviderMessage
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{byRef: make(map[ledgerKey]model.ProviderMessage)}
}

func (f *fakeLedger) add(channelID, messageRef string, msg model.ProviderMessage) {
	f.byRef[ledgerKey{channelID, messageRef}] = msg
}

func (f *fakeLedger) ByProviderRef(_ context.Context, channelID, messageRef string) (model.ProviderMessage, error) {
	m, ok := f.byRef[ledgerKey{channelID, messageRef}]
	if !ok {
		return model.ProviderMessage{}, adaptererr.ErrNotFound
	}

	return m, nil
}
