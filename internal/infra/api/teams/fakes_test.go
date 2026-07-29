// SPDX-License-Identifier: AGPL-3.0-or-later

package teams_test

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/service"
)

var errFakeNotFound = errors.New("fake: not found")

// fakeRegistry maps a (project id, kind) pair to a service.Provider —
// keyed on the pair, not the project alone, so a test can prove the handler
// resolves the exact kind it asked for (review fit#4) rather than any kind
// configured for the project.
type fakeRegistry struct {
	providers map[fakeRegistryKey]service.Provider
}

type fakeRegistryKey struct {
	projectID uuid.UUID
	kind      provider.ProviderKind
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{providers: make(map[fakeRegistryKey]service.Provider)}
}

// set registers p for (projectID, kind). Tests that only care about a single
// kind may also use setTeams for brevity.
func (f *fakeRegistry) set(projectID uuid.UUID, kind provider.ProviderKind, p service.Provider) {
	f.providers[fakeRegistryKey{projectID: projectID, kind: kind}] = p
}

func (f *fakeRegistry) ForProjectKind(_ context.Context, projectID uuid.UUID, kind provider.ProviderKind) (service.Provider, error) {
	p, ok := f.providers[fakeRegistryKey{projectID: projectID, kind: kind}]
	if !ok {
		return nil, errFakeNotFound
	}

	return p, nil
}

// fakeConfigStore serves the raw stored credentials LoadProvider reads for
// the JWT audience (bot_app_id). Keyed on (project, kind) — a lookup for one
// kind must never be satisfied by another kind's stored row.
type fakeConfigStore struct {
	rows map[fakeConfigKey]map[string]string
}

type fakeConfigKey struct {
	projectID uuid.UUID
	kind      string
}

func newFakeConfigStore() *fakeConfigStore {
	return &fakeConfigStore{rows: make(map[fakeConfigKey]map[string]string)}
}

func (f *fakeConfigStore) add(projectID uuid.UUID, kind string, creds map[string]string) {
	f.rows[fakeConfigKey{projectID: projectID, kind: kind}] = creds
}

func (f *fakeConfigStore) LoadProvider(_ context.Context, projectID uuid.UUID, kind string) ([]byte, error) {
	creds, ok := f.rows[fakeConfigKey{projectID: projectID, kind: kind}]
	if !ok {
		return nil, errFakeNotFound
	}

	b, _ := json.Marshal(creds)

	return b, nil
}

// fakeFollowUpper is a followUpper test double.
type fakeFollowUpper struct {
	err   error
	last  command.FollowUpCommand
	calls int
}

func (f *fakeFollowUpper) Handle(_ context.Context, cmd command.FollowUpCommand) (command.FollowUpResult, error) {
	f.last = cmd
	f.calls++

	return command.FollowUpResult{Outcome: command.OutcomeAppended}, f.err
}

// fakeTeamsProvider is a hermetic service.Provider test double, avoiding a
// dependency on the real Teams adapter's HTTP calls for transport-layer
// tests.
type fakeTeamsProvider struct {
	inbound    service.InboundMessage
	inboundErr error
}

func (p *fakeTeamsProvider) Deliver(_ context.Context, _ service.OutboundMessage) (service.MessageRef, error) {
	return service.MessageRef{}, errors.New("fake: Deliver not used in transport tests")
}

func (p *fakeTeamsProvider) ParseInbound(_ context.Context, _ []byte) (service.InboundMessage, error) {
	return p.inbound, p.inboundErr
}
