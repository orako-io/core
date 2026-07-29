// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gatewaymgr supervises the persistent gateway connections that
// providers with no inbound webhook need — today, exactly one discordgo
// session per distinct Discord bot token. Discord delivers DM replies and
// button interactions only over its gateway websocket, so the connection
// must be kept open independently of any HTTP request; this package owns
// that lifecycle (open at boot / on configuration, close on shutdown) while
// discordgo itself owns reconnection.
package gatewaymgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider/discord"
	"github.com/orako-io/core/internal/application/service"
)

// session is the narrow lifecycle the supervisor manages. *discordgo.Session
// satisfies it directly; tests inject a fake to avoid a live socket.
type session interface {
	Open() error
	Close() error
}

// SessionFactory builds and configures a session for a bot token. The
// production factory (NewDiscordSessionFactory) registers the Discord
// gateway's handlers on a real discordgo.Session before returning it.
type SessionFactory func(botToken string) (session, error)

// allProvidersLoader is the narrow store dependency for the boot-time sync.
// *integration.ProjectProviderStore satisfies it (service.AllProvidersLoader).
type allProvidersLoader interface {
	LoadAllProviders(ctx context.Context) ([]service.ProviderRow, error)
}

// entry tracks one open session and how many projects currently reference its
// bot token.
type entry struct {
	sess session
	refs int
}

// Supervisor ensures exactly one open gateway session per distinct bot token,
// regardless of how many projects configure Discord with that token, and
// closes sessions whose token is no longer referenced by any project. Safe
// for concurrent use.
type Supervisor struct {
	mu           sync.Mutex           `exhaustruct:"optional"`
	sessions     map[string]*entry    // bot token -> entry
	projectToken map[uuid.UUID]string // project id -> its current bot token
	factory      SessionFactory
	logger       *slog.Logger
}

// NewSupervisor builds a Supervisor. factory and logger are required.
func NewSupervisor(factory SessionFactory, logger *slog.Logger) *Supervisor {
	return &Supervisor{
		sessions:     make(map[string]*entry),
		projectToken: make(map[uuid.UUID]string),
		factory:      factory,
		logger:       logger,
	}
}

// TestSession is an exported alias for the unexported session lifecycle
// interface, so external tests can inject a fake session without a live
// socket. Test-only seam.
type TestSession = session

// NewSupervisorForTest builds a Supervisor with an injected factory, bypassing
// the production discordgo wiring in NewDiscordSessionFactory. Test-only seam.
func NewSupervisorForTest(factory func(botToken string) (TestSession, error), logger *slog.Logger) *Supervisor {
	return NewSupervisor(SessionFactory(factory), logger)
}

// NewDiscordSessionFactory builds the production SessionFactory: a real
// discordgo.Session with gw's MESSAGE_CREATE/INTERACTION_CREATE handlers and
// intents registered (Session.Open is not called here — EnsureSession does
// that uniformly for the real and test factories).
func NewDiscordSessionFactory(gw *discord.Gateway) SessionFactory {
	return func(botToken string) (session, error) {
		s, err := discordgo.New("Bot " + botToken)
		if err != nil {
			return nil, fmt.Errorf("gatewaymgr: building discordgo session: %w", err)
		}

		gw.Register(s)

		return s, nil
	}
}

// Status reports live session/project counts for diagnostics. sessions is the
// number of open gateway websockets (one per distinct bot token); 0 means no
// gateway is connected, so no inbound DM or interaction can be received.
func (s *Supervisor) Status() (sessions, projects int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.sessions), len(s.projectToken)
}

// EnsureSession makes projectID's active gateway session match token: a
// no-op if unchanged, opens a new shared session on the first project to use
// a token, and releases (closing when the last reference drops) the
// project's previous token if it changed. Called at boot for every
// already-configured discord project (via SyncFromStore) and on every
// subsequent ConfigureProvider call for kind=="discord" (via RegisterFromMap,
// wired as the registry's register hook).
//
// Lock discipline: the decision of *what* to mutate is always made under
// s.mu, but the dial itself (factory + Open, a websocket handshake) runs
// unlocked so one slow/hanging dial cannot serialize every other caller
// (other EnsureSession calls, or Close). Two callers can therefore race to
// dial the same brand-new token concurrently; the second to re-acquire the
// lock loses (first-wins) and discards its redundant session.
func (s *Supervisor) EnsureSession(projectID uuid.UUID, token string) error {
	if token == "" {
		return fmt.Errorf("gatewaymgr: empty bot token for project %s", projectID)
	}

	s.mu.Lock()

	if old, ok := s.projectToken[projectID]; ok && old == token {
		s.mu.Unlock()
		return nil
	}

	if e, ok := s.sessions[token]; ok {
		// Fast path: a session for token is already open (shared with
		// another project, or committed by a concurrent dialer already) —
		// no I/O needed, just adjust refcounts under the lock.
		e.refs++

		toClose := s.retargetProjectLocked(projectID, token)

		s.mu.Unlock()
		s.closeEntry(toClose)

		return nil
	}

	s.mu.Unlock()

	// Slow path: no session exists for token yet. Dial outside the lock.
	sess, err := s.factory(token)
	if err != nil {
		return fmt.Errorf("gatewaymgr: building session for project %s: %w", projectID, err)
	}

	if err := sess.Open(); err != nil {
		return fmt.Errorf("gatewaymgr: opening session for project %s: %w", projectID, err)
	}

	s.mu.Lock()

	if cur, ok := s.projectToken[projectID]; ok && cur == token {
		// A concurrent call for the same project+token already committed
		// while we dialed. Nothing left to add; discard our redundant dial.
		s.mu.Unlock()
		s.closeSession(sess)

		return nil
	}

	if e, ok := s.sessions[token]; ok {
		// Lost the race: another caller committed a session for this token
		// while we were dialing. Discard ours.
		e.refs++

		toClose := s.retargetProjectLocked(projectID, token)

		s.mu.Unlock()
		s.closeEntry(toClose)
		s.closeSession(sess)

		return nil
	}

	s.sessions[token] = &entry{sess: sess, refs: 1}

	toClose := s.retargetProjectLocked(projectID, token)

	s.mu.Unlock()

	s.closeEntry(toClose)

	s.logger.Info("gatewaymgr: opened discord gateway session", slog.String("project_id", projectID.String()))

	return nil
}

// retargetProjectLocked points projectID at token and releases whatever
// token it previously referenced, if any and if different, returning the
// entry to close outside the lock when that release dropped the last
// reference (nil otherwise). The caller must already have accounted for the
// new reference to token (incremented its refcount or just created it)
// before calling this. Caller must hold s.mu.
func (s *Supervisor) retargetProjectLocked(projectID uuid.UUID, token string) *entry {
	old, hadOld := s.projectToken[projectID]

	s.projectToken[projectID] = token

	if !hadOld || old == token {
		return nil
	}

	return s.releaseLocked(old)
}

// releaseLocked decrements token's refcount and, once no project references
// it, removes it from s.sessions and returns it so the caller can close it
// outside the lock (returns nil when the token is still referenced or
// unknown). Caller must hold s.mu.
func (s *Supervisor) releaseLocked(token string) *entry {
	e, ok := s.sessions[token]
	if !ok {
		return nil
	}

	e.refs--
	if e.refs > 0 {
		return nil
	}

	delete(s.sessions, token)

	return e
}

// closeEntry closes e's session, if e is non-nil, logging any error. Must be
// called unlocked.
func (s *Supervisor) closeEntry(e *entry) {
	if e == nil {
		return
	}

	s.closeSession(e.sess)
}

// closeSession closes sess, logging any error. Must be called unlocked.
func (s *Supervisor) closeSession(sess session) {
	if err := sess.Close(); err != nil {
		s.logger.Warn("gatewaymgr: closing discord gateway session", slog.Any("error", err))
	}
}

// ReleaseProject drops projectID's reference to whatever gateway session it
// currently tracks, if any, closing the session when it was the last
// reference. Called when a project's provider kind changes away from
// "discord" so the old session — possibly holding now-orphaned or revoked
// credentials — does not stay connected forever.
func (s *Supervisor) ReleaseProject(projectID uuid.UUID) {
	s.mu.Lock()

	old, ok := s.projectToken[projectID]
	if !ok {
		s.mu.Unlock()
		return
	}

	delete(s.projectToken, projectID)

	toClose := s.releaseLocked(old)

	s.mu.Unlock()

	s.closeEntry(toClose)
}

// RegisterFromMap is the registry's register hook shape
// (provider.Registry.SetRegisterHook): it ensures a session for every
// kind=="discord" registration, and releases the project's existing gateway
// session (if any) for every other kind, so a project switching off discord
// doesn't leave its old session connected. Safe to install unconditionally
// regardless of which providers a project uses.
func (s *Supervisor) RegisterFromMap(projectID uuid.UUID, kind string, creds map[string]string) {
	if kind != "discord" {
		s.ReleaseProject(projectID)
		return
	}

	if err := s.EnsureSession(projectID, creds["bot_token"]); err != nil {
		s.logger.Error("gatewaymgr: ensuring session from RegisterFromMap",
			slog.String("project_id", projectID.String()), slog.Any("error", err))
	}
}

// SyncFromStore opens a session for every already-configured discord project
// in store. Intended to run once at boot, after the provider registry has
// hydrated (registry hydration and this sync are independent: hydration
// builds in-memory Provider values for Deliver/Edit, this opens the gateway
// sockets those Provider values have no part in).
func (s *Supervisor) SyncFromStore(ctx context.Context, store allProvidersLoader) error {
	rows, err := store.LoadAllProviders(ctx)
	if err != nil {
		return fmt.Errorf("gatewaymgr: loading providers for boot sync: %w", err)
	}

	for _, row := range rows {
		if row.Kind != "discord" {
			continue
		}

		// Empty credentials = a project-level row whose connection lives at the
		// org level; nothing to open here. Skip quietly (not an error).
		if len(row.Credentials) == 0 {
			continue
		}

		var creds map[string]string

		if err := json.Unmarshal(row.Credentials, &creds); err != nil {
			s.logger.Error("gatewaymgr: decoding discord credentials during boot sync",
				slog.String("project_id", row.ProjectID.String()), slog.Any("error", err))

			continue
		}

		if err := s.EnsureSession(row.ProjectID, creds["bot_token"]); err != nil {
			s.logger.Error("gatewaymgr: opening session during boot sync",
				slog.String("project_id", row.ProjectID.String()), slog.Any("error", err))
		}
	}

	return nil
}

// Close gracefully closes every open session. Safe to call once during
// server shutdown (e.g. deferred right after construction, alongside the
// other owned resources). The closes themselves run unlocked so a slow
// Close on one session cannot delay the others or block a concurrent
// EnsureSession from observing the cleared state.
func (s *Supervisor) Close() error {
	s.mu.Lock()

	sessions := s.sessions
	s.sessions = make(map[string]*entry)
	s.projectToken = make(map[uuid.UUID]string)

	s.mu.Unlock()

	var errs []error

	for _, e := range sessions {
		if err := e.sess.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
