// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application/service"
)

// ── provider doubles shared by the bridge projector and alert-channel sweeper
// tests. Two distinct concrete types stand in for the capability split: an
// Editor-capable provider (Slack-like) and a Deliver/ParseInbound-only
// provider (Telegram-like, no Editor) — a single flag cannot fake the absence
// of a method, only a genuinely different type can. ──────────────────────────

// editCall records one Editor.Edit invocation.
type editCall struct {
	ref  service.MessageRef
	text string
}

// fakeEditorProvider implements Provider + Editor (a Slack-shaped fake): every
// delivered/edited call is recorded for assertions.
//
// editFailTimes, when > 0, makes the next that-many Edit calls fail (with
// editErr if set, else a generic error) before Edit starts succeeding — the
// double the closure Edit-failure retry test exercises: fails once, the
// router retries the whole message, and the second attempt is skipped by
// the "reserving" guard.
//
// deliverFailTimes is the same shape for Deliver: it lets a
// reserve→deliver→finalize test force Deliver itself to fail once (proving
// the reservation is reverted so a retry actually retries Deliver) while
// deliverAttempts/delivered together prove a separate finalize-only failure
// (Deliver already succeeded) never causes a second Deliver call on retry.
type fakeEditorProvider struct {
	delivered []service.OutboundMessage
	edited    []editCall

	deliverErr       error
	deliverFailTimes int
	deliverAttempts  int // every Deliver call, successful or not

	editErr       error
	editFailTimes int
	editAttempts  int // every Edit call, successful or not
}

var (
	_ service.Provider = (*fakeEditorProvider)(nil)
	_ service.Editor   = (*fakeEditorProvider)(nil)
)

func (f *fakeEditorProvider) Deliver(_ context.Context, msg service.OutboundMessage) (service.MessageRef, error) {
	f.deliverAttempts++

	if f.deliverFailTimes > 0 {
		f.deliverFailTimes--

		if f.deliverErr != nil {
			return service.MessageRef{}, f.deliverErr
		}

		return service.MessageRef{}, errors.New("fakeEditorProvider: forced deliver failure")
	}

	if f.deliverErr != nil {
		return service.MessageRef{}, f.deliverErr
	}

	f.delivered = append(f.delivered, msg)

	return service.MessageRef{ChannelID: "C-EDITOR", MessageID: uuid.NewString()}, nil
}

func (f *fakeEditorProvider) ParseInbound(_ context.Context, _ []byte) (service.InboundMessage, error) {
	return service.InboundMessage{}, errors.New("fakeEditorProvider: ParseInbound not wired")
}

func (f *fakeEditorProvider) Edit(_ context.Context, ref service.MessageRef, text string) error {
	f.editAttempts++

	if f.editFailTimes > 0 {
		f.editFailTimes--

		if f.editErr != nil {
			return f.editErr
		}

		return errors.New("fakeEditorProvider: forced edit failure")
	}

	if f.editErr != nil {
		return f.editErr
	}

	f.edited = append(f.edited, editCall{ref: ref, text: text})

	return nil
}

// fakeNudgeOnlyProvider implements only Provider (a Telegram-shaped fake: no
// Editor). The bridge projector's closure notice for it is entirely carried
// by the closure Deliver — it must never attempt an Edit (there is no Edit
// method to call).
type fakeNudgeOnlyProvider struct {
	delivered []service.OutboundMessage
}

var _ service.Provider = (*fakeNudgeOnlyProvider)(nil)

func (f *fakeNudgeOnlyProvider) Deliver(_ context.Context, msg service.OutboundMessage) (service.MessageRef, error) {
	f.delivered = append(f.delivered, msg)

	return service.MessageRef{ChannelID: "C-NUDGEONLY", MessageID: uuid.NewString()}, nil
}

func (f *fakeNudgeOnlyProvider) ParseInbound(_ context.Context, _ []byte) (service.InboundMessage, error) {
	return service.InboundMessage{}, errors.New("fakeNudgeOnlyProvider: ParseInbound not wired")
}

// postCall records one ChannelPoster.PostChannel invocation.
type postCall struct {
	channelID string
	text      string
}

// fakeChannelPosterProvider implements Provider + ChannelPoster, used by the
// alert-channel rung tests.
type fakeChannelPosterProvider struct {
	posted  []postCall
	postErr error
}

var (
	_ service.Provider      = (*fakeChannelPosterProvider)(nil)
	_ service.ChannelPoster = (*fakeChannelPosterProvider)(nil)
)

func (f *fakeChannelPosterProvider) Deliver(_ context.Context, _ service.OutboundMessage) (service.MessageRef, error) {
	return service.MessageRef{}, errors.New("fakeChannelPosterProvider: Deliver not wired")
}

func (f *fakeChannelPosterProvider) ParseInbound(_ context.Context, _ []byte) (service.InboundMessage, error) {
	return service.InboundMessage{}, errors.New("fakeChannelPosterProvider: ParseInbound not wired")
}

func (f *fakeChannelPosterProvider) PostChannel(_ context.Context, channelID, text string) (service.MessageRef, error) {
	if f.postErr != nil {
		return service.MessageRef{}, f.postErr
	}

	f.posted = append(f.posted, postCall{channelID: channelID, text: text})

	return service.MessageRef{ChannelID: channelID, MessageID: uuid.NewString()}, nil
}

// fakeProjectProviderKey keys fakeProjectProviders.byProjectKind — the alert
// rung resolves by the exact (project, kind) pair the unclaimedForAlert query
// picked, never by project alone (review fit#4).
type fakeProjectProviderKey struct {
	projectID uuid.UUID
	kind      provider.ProviderKind
}

// fakeProjectProviders resolves a Provider by (project, kind) (ForProjectKind,
// the alert rung) or by member id (ForMember, the rung-2 nudge fan-out) — the
// seam alertProviderResolver needs.
type fakeProjectProviders struct {
	byProjectKind map[fakeProjectProviderKey]service.Provider
	byMember      map[uuid.UUID]service.Provider
	err           error
}

func (f *fakeProjectProviders) ForProjectKind(_ context.Context, projectID uuid.UUID, kind provider.ProviderKind) (service.Provider, error) {
	if f.err != nil {
		return nil, f.err
	}

	p, ok := f.byProjectKind[fakeProjectProviderKey{projectID: projectID, kind: kind}]
	if !ok {
		return nil, errors.New("fakeProjectProviders: no provider configured for project/kind")
	}

	return p, nil
}

func (f *fakeProjectProviders) ForMember(_ context.Context, _, memberID uuid.UUID) (service.Provider, error) {
	if f.err != nil {
		return nil, f.err
	}

	p, ok := f.byMember[memberID]
	if !ok {
		return nil, errors.New("fakeProjectProviders: no provider configured for member")
	}

	return p, nil
}

// fakeMemberProviders resolves a Provider by (project, member) — the seam
// bridgeProviderResolver (ForMember) needs. Keyed on member id only: the
// projector routes per-recipient, mirroring providerForCandidate in
// delivery_notifier.go.
type fakeMemberProviders struct {
	byMember map[uuid.UUID]service.Provider
	err      error
}

func (f *fakeMemberProviders) ForMember(_ context.Context, _, memberID uuid.UUID) (service.Provider, error) {
	if f.err != nil {
		return nil, f.err
	}

	p, ok := f.byMember[memberID]
	if !ok {
		return nil, errors.New("fakeMemberProviders: no provider configured for member")
	}

	return p, nil
}
