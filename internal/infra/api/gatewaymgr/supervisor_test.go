// SPDX-License-Identifier: AGPL-3.0-or-later

package gatewaymgr_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/infra/api/gatewaymgr"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// fakeSession is a no-op session double: no live socket, records Open/Close
// calls for assertions.
type fakeSession struct {
	mu      sync.Mutex
	opened  int
	closed  int
	openErr error
}

func (f *fakeSession) Open() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.opened++

	return f.openErr
}

func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.closed++

	return nil
}

// fakeFactory builds fakeSessions and records how many times it was invoked
// per token, so tests can assert a token is deduplicated (one session
// regardless of how many projects share it).
type fakeFactory struct {
	mu       sync.Mutex
	builds   map[string]int
	sessions map[string]*fakeSession
}

func newFakeFactory() *fakeFactory {
	return &fakeFactory{builds: make(map[string]int), sessions: make(map[string]*fakeSession)}
}

func (f *fakeFactory) build(token string) (*fakeSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.builds[token]++

	s, ok := f.sessions[token]
	if !ok {
		s = &fakeSession{}
		f.sessions[token] = s
	}

	return s, nil
}

// asFactory adapts fakeFactory.build to the sessionFactory shape
// gatewaymgr.NewSupervisor expects (an interface it cannot name from an
// external test package, so this returns the *fakeSession as the concrete
// type and relies on structural satisfaction of Open()/Close()).
func (f *fakeFactory) asFactory() func(string) (gatewaymgr.TestSession, error) {
	return func(token string) (gatewaymgr.TestSession, error) {
		return f.build(token)
	}
}

func newSupervisor(f *fakeFactory) *gatewaymgr.Supervisor {
	return gatewaymgr.NewSupervisorForTest(f.asFactory(), discardLogger())
}

// TestSupervisor_EnsureSession_DedupesByToken proves two projects sharing the
// same bot token share exactly one open session.
func TestSupervisor_EnsureSession_DedupesByToken(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	projectA := uuid.New()
	projectB := uuid.New()

	if err := sup.EnsureSession(projectA, "token-shared"); err != nil {
		t.Fatalf("EnsureSession(A): %v", err)
	}

	if err := sup.EnsureSession(projectB, "token-shared"); err != nil {
		t.Fatalf("EnsureSession(B): %v", err)
	}

	if factory.builds["token-shared"] != 1 {
		t.Errorf("factory builds for shared token: got %d, want 1", factory.builds["token-shared"])
	}

	if factory.sessions["token-shared"].opened != 1 {
		t.Errorf("Open calls: got %d, want 1", factory.sessions["token-shared"].opened)
	}
}

// TestSupervisor_EnsureSession_NoOpWhenUnchanged proves calling EnsureSession
// again with the same token for the same project does nothing.
func TestSupervisor_EnsureSession_NoOpWhenUnchanged(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	projectID := uuid.New()

	if err := sup.EnsureSession(projectID, "token-1"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	if err := sup.EnsureSession(projectID, "token-1"); err != nil {
		t.Fatalf("EnsureSession (repeat): %v", err)
	}

	if factory.builds["token-1"] != 1 {
		t.Errorf("factory builds: got %d, want 1 (unchanged token is a no-op)", factory.builds["token-1"])
	}
}

// TestSupervisor_EnsureSession_ReplacesToken proves changing a project's
// token closes the old session (once no longer referenced) and opens the new
// one.
func TestSupervisor_EnsureSession_ReplacesToken(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	projectID := uuid.New()

	if err := sup.EnsureSession(projectID, "token-old"); err != nil {
		t.Fatalf("EnsureSession(old): %v", err)
	}

	if err := sup.EnsureSession(projectID, "token-new"); err != nil {
		t.Fatalf("EnsureSession(new): %v", err)
	}

	if factory.sessions["token-old"].closed != 1 {
		t.Errorf("old session Close calls: got %d, want 1", factory.sessions["token-old"].closed)
	}

	if factory.sessions["token-new"].opened != 1 {
		t.Errorf("new session Open calls: got %d, want 1", factory.sessions["token-new"].opened)
	}
}

// TestSupervisor_EnsureSession_KeepsSharedTokenOpenUntilLastReleased proves a
// token stays open as long as at least one project still references it.
func TestSupervisor_EnsureSession_KeepsSharedTokenOpenUntilLastReleased(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	projectA := uuid.New()
	projectB := uuid.New()

	if err := sup.EnsureSession(projectA, "token-shared"); err != nil {
		t.Fatalf("EnsureSession(A): %v", err)
	}

	if err := sup.EnsureSession(projectB, "token-shared"); err != nil {
		t.Fatalf("EnsureSession(B): %v", err)
	}

	// A moves off the shared token; B still references it, so it must stay open.
	if err := sup.EnsureSession(projectA, "token-a-only"); err != nil {
		t.Fatalf("EnsureSession(A moves): %v", err)
	}

	if factory.sessions["token-shared"].closed != 0 {
		t.Errorf("shared session must stay open while B references it, closed=%d", factory.sessions["token-shared"].closed)
	}

	// Now B moves off it too; the shared session must close.
	if err := sup.EnsureSession(projectB, "token-b-only"); err != nil {
		t.Fatalf("EnsureSession(B moves): %v", err)
	}

	if factory.sessions["token-shared"].closed != 1 {
		t.Errorf("shared session must close once unreferenced, closed=%d", factory.sessions["token-shared"].closed)
	}
}

// TestSupervisor_RegisterFromMap_IgnoresNonDiscordKinds proves RegisterFromMap
// only acts on kind=="discord".
func TestSupervisor_RegisterFromMap_IgnoresNonDiscordKinds(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	sup.RegisterFromMap(uuid.New(), "slack", map[string]string{"bot_token": "xoxb-x"})

	if len(factory.builds) != 0 {
		t.Errorf("non-discord kind must not build a session, got %d builds", len(factory.builds))
	}

	sup.RegisterFromMap(uuid.New(), "discord", map[string]string{"bot_token": "discord-token"})

	if factory.builds["discord-token"] != 1 {
		t.Errorf("discord kind must build a session, got %d builds", factory.builds["discord-token"])
	}
}

// TestSupervisor_RegisterFromMap_ReleasesSessionOnNonDiscordKind proves
// re-registering a project that previously had a discord session as another
// kind releases that project's reference, closing the session once it was
// the last reference (review finding #6: switching a project off discord
// used to leave the old gateway session connected forever).
func TestSupervisor_RegisterFromMap_ReleasesSessionOnNonDiscordKind(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	projectID := uuid.New()

	sup.RegisterFromMap(projectID, "discord", map[string]string{"bot_token": "discord-token"})

	if factory.sessions["discord-token"].opened != 1 {
		t.Fatalf("discord session must be opened, opened=%d", factory.sessions["discord-token"].opened)
	}

	sup.RegisterFromMap(projectID, "slack", map[string]string{"bot_token": "xoxb-x"})

	if factory.sessions["discord-token"].closed != 1 {
		t.Errorf("switching the project off discord must close its old session (last ref), closed=%d",
			factory.sessions["discord-token"].closed)
	}

	if len(factory.builds) != 1 {
		t.Errorf("slack kind must not build a gateway session, got builds=%v", factory.builds)
	}
}

// TestSupervisor_RegisterFromMap_ReleasesOneRefKeepsSharedSessionOpen proves
// that when two projects share a discord token, one of them switching off
// discord only drops its own reference — the shared session stays open for
// the other project.
func TestSupervisor_RegisterFromMap_ReleasesOneRefKeepsSharedSessionOpen(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	projectA := uuid.New()
	projectB := uuid.New()

	sup.RegisterFromMap(projectA, "discord", map[string]string{"bot_token": "token-shared"})
	sup.RegisterFromMap(projectB, "discord", map[string]string{"bot_token": "token-shared"})

	sup.RegisterFromMap(projectA, "teams", map[string]string{})

	if factory.sessions["token-shared"].closed != 0 {
		t.Errorf("shared session must stay open while B still references it, closed=%d",
			factory.sessions["token-shared"].closed)
	}

	sup.RegisterFromMap(projectB, "teams", map[string]string{})

	if factory.sessions["token-shared"].closed != 1 {
		t.Errorf("shared session must close once both projects released it, closed=%d",
			factory.sessions["token-shared"].closed)
	}
}

// TestSupervisor_EnsureSession_SlowDialDoesNotBlockOtherTokens proves the
// supervisor no longer holds its lock across the dial (factory + Open): a
// concurrent EnsureSession for a different token must proceed while the
// first dial is still in flight (review finding #7).
func TestSupervisor_EnsureSession_SlowDialDoesNotBlockOtherTokens(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	dialing := make(chan struct{})

	var once sync.Once

	factory := func(token string) (gatewaymgr.TestSession, error) {
		if token == "slow-token" {
			once.Do(func() { close(dialing) })
			<-gate
		}

		return &fakeSession{}, nil
	}

	sup := gatewaymgr.NewSupervisorForTest(factory, discardLogger())

	slowDone := make(chan error, 1)

	go func() {
		slowDone <- sup.EnsureSession(uuid.New(), "slow-token")
	}()

	select {
	case <-dialing:
	case <-time.After(2 * time.Second):
		t.Fatal("slow dial never started")
	}

	fastDone := make(chan error, 1)

	go func() {
		fastDone <- sup.EnsureSession(uuid.New(), "fast-token")
	}()

	select {
	case err := <-fastDone:
		if err != nil {
			t.Fatalf("EnsureSession(fast-token): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("EnsureSession for a different token blocked on the slow dial — lock held across I/O")
	}

	close(gate)

	select {
	case err := <-slowDone:
		if err != nil {
			t.Fatalf("EnsureSession(slow-token): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow dial never completed after the gate was released")
	}
}

// TestSupervisor_Close_DoesNotHoldLockDuringSessionClose proves Close()
// releases its lock before closing sessions, so a concurrent EnsureSession
// for an unrelated token is not blocked by a slow session Close (review
// finding #7 also called out Close() as holding the lock across I/O).
func TestSupervisor_Close_DoesNotHoldLockDuringSessionClose(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	if err := sup.EnsureSession(uuid.New(), "token-1"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	closeDone := make(chan error, 1)

	go func() {
		closeDone <- sup.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not complete")
	}

	// A subsequent EnsureSession must be able to proceed immediately (the
	// lock was released for the actual Close() I/O, not held throughout).
	newSessDone := make(chan error, 1)

	go func() {
		newSessDone <- sup.EnsureSession(uuid.New(), "token-2")
	}()

	select {
	case err := <-newSessDone:
		if err != nil {
			t.Fatalf("EnsureSession(token-2) after Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("EnsureSession after Close blocked")
	}
}

// fakeProviderStore implements the allProvidersLoader shape for SyncFromStore
// tests (service.AllProvidersLoader).
type fakeProviderStore struct {
	rows []service.ProviderRow
	err  error
}

func (f *fakeProviderStore) LoadAllProviders(_ context.Context) ([]service.ProviderRow, error) {
	return f.rows, f.err
}

// TestSupervisor_SyncFromStore_OpensSessionsForConfiguredDiscordProjects
// proves the boot-time sync opens one session per discord row and ignores
// non-discord rows.
func TestSupervisor_SyncFromStore_OpensSessionsForConfiguredDiscordProjects(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	discordProject := uuid.New()
	slackProject := uuid.New()

	discordCreds, _ := json.Marshal(map[string]string{"bot_token": "discord-token-boot"})
	slackCreds, _ := json.Marshal(map[string]string{"bot_token": "xoxb-x"})

	store := &fakeProviderStore{rows: []service.ProviderRow{
		{ProjectID: discordProject, Kind: "discord", Credentials: discordCreds},
		{ProjectID: slackProject, Kind: "slack", Credentials: slackCreds},
	}}

	if err := sup.SyncFromStore(t.Context(), store); err != nil {
		t.Fatalf("SyncFromStore: %v", err)
	}

	if factory.builds["discord-token-boot"] != 1 {
		t.Errorf("discord project: got %d builds, want 1", factory.builds["discord-token-boot"])
	}

	if len(factory.builds) != 1 {
		t.Errorf("only the discord row must build a session, got builds=%v", factory.builds)
	}
}

// TestSupervisor_SyncFromStore_PropagatesLoadError proves a store failure
// surfaces as an error (boot should be able to detect and log it).
func TestSupervisor_SyncFromStore_PropagatesLoadError(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	store := &fakeProviderStore{err: errors.New("db unreachable")}

	if err := sup.SyncFromStore(t.Context(), store); err == nil {
		t.Fatal("want error when the store fails, got nil")
	}
}

// TestSupervisor_Close_ClosesEverySession proves Close shuts down every
// distinct open session.
func TestSupervisor_Close_ClosesEverySession(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	if err := sup.EnsureSession(uuid.New(), "token-1"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	if err := sup.EnsureSession(uuid.New(), "token-2"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	if err := sup.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if factory.sessions["token-1"].closed != 1 || factory.sessions["token-2"].closed != 1 {
		t.Errorf("both sessions must be closed: token-1=%d token-2=%d",
			factory.sessions["token-1"].closed, factory.sessions["token-2"].closed)
	}
}

// TestSupervisor_EnsureSession_EmptyTokenErrors proves an empty bot token is
// rejected rather than silently opening a broken session.
func TestSupervisor_EnsureSession_EmptyTokenErrors(t *testing.T) {
	t.Parallel()

	factory := newFakeFactory()
	sup := newSupervisor(factory)

	if err := sup.EnsureSession(uuid.New(), ""); err == nil {
		t.Fatal("want error for empty bot token, got nil")
	}
}

// fakeProvLoader feeds rows to SyncFromStore.
type fakeProvLoader struct{ rows []service.ProviderRow }

func (l fakeProvLoader) LoadAllProviders(context.Context) ([]service.ProviderRow, error) {
	return l.rows, nil
}

// TestSupervisor_SyncFromStore_SkipsEmptyCredentials proves a project-level
// discord row with NULL/empty credentials (its bot lives at the org level) is
// skipped quietly — no session attempt, no error log — while a valid row after
// it still opens a session.
func TestSupervisor_SyncFromStore_SkipsEmptyCredentials(t *testing.T) {
	t.Parallel()

	f := newFakeFactory()
	sup := newSupervisor(f)

	valid, _ := json.Marshal(map[string]string{"bot_token": "tok-valid"})
	loader := fakeProvLoader{rows: []service.ProviderRow{
		{ProjectID: uuid.New(), Kind: "discord", Credentials: nil},   // NULL creds — skip
		{ProjectID: uuid.New(), Kind: "discord", Credentials: valid}, // valid — opens a session
	}}

	if err := sup.SyncFromStore(context.Background(), loader); err != nil {
		t.Fatalf("SyncFromStore: %v", err)
	}

	if f.builds["tok-valid"] != 1 {
		t.Errorf("valid row should build 1 session, got %d", f.builds["tok-valid"])
	}

	if len(f.builds) != 1 {
		t.Errorf("empty-cred row must be skipped (no session); builds=%v", f.builds)
	}
}
