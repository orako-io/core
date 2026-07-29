// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/adapters/messaging"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

const projectAddedCoalesceWindow = 10 * time.Second

const projectAddedSendTimeout = 30 * time.Second

// ProjectAddedNotifier batches project-added emails per member.
type ProjectAddedNotifier struct {
	members  memberByIDReader
	projects projectByIDReader
	orgs     orgByIDReader
	mailer   service.Mailer
	baseURL  string
	window   time.Duration
	logger   *slog.Logger
	stopCh   chan struct{}

	mu      sync.Mutex
	pending map[uuid.UUID][]uuid.UUID // memberID → projectIDs collected this window
	timers  map[uuid.UUID]*time.Timer
	closed  bool
	wg      sync.WaitGroup
}

// NewProjectAddedNotifier builds a ProjectAddedNotifier.
func NewProjectAddedNotifier(
	members memberByIDReader,
	projects projectByIDReader,
	orgs orgByIDReader,
	mailer service.Mailer,
	baseURL string,
	window time.Duration,
	logger *slog.Logger,
) *ProjectAddedNotifier {
	if window <= 0 {
		window = projectAddedCoalesceWindow
	}

	return &ProjectAddedNotifier{ //nolint:exhaustruct // synchronization fields intentionally start at their zero values
		members:  members,
		projects: projects,
		orgs:     orgs,
		mailer:   mailer,
		baseURL:  baseURL,
		window:   window,
		logger:   logger,
		stopCh:   make(chan struct{}),
		pending:  make(map[uuid.UUID][]uuid.UUID),
		timers:   make(map[uuid.UUID]*time.Timer),
	}
}

// Handler returns the event subscriber.
func (n *ProjectAddedNotifier) Handler() message.NoPublishHandlerFunc {
	return func(msg *message.Message) error {
		env, err := messaging.DecodeEnvelope(msg.Payload)
		if err != nil {
			return err
		}

		if env.GetType() != orakov1.EventType_EVENT_TYPE_MEMBER_LIFECYCLE {
			return nil
		}

		lifecycle := env.GetMemberLifecycle()
		if lifecycle == nil || lifecycle.GetTransition() != orakov1.MemberTransition_MEMBER_TRANSITION_ADDED_TO_PROJECT {
			return nil
		}

		memberID, err := uuid.Parse(lifecycle.GetMemberId())
		if err != nil {
			n.logger.WarnContext(msg.Context(), "project-added notifier: malformed member id",
				slog.String("value", lifecycle.GetMemberId()))

			return nil //nolint:nilerr // poison message: a malformed id never parses; retrying is useless
		}

		projectID, err := uuid.Parse(lifecycle.GetProjectId())
		if err != nil {
			n.logger.WarnContext(msg.Context(), "project-added notifier: malformed project id",
				slog.String("value", lifecycle.GetProjectId()))

			return nil //nolint:nilerr // poison message: a malformed id never parses; retrying is useless
		}

		n.collect(memberID, projectID)

		return nil
	}
}

func (n *ProjectAddedNotifier) collect(memberID, projectID uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.closed {
		return
	}

	first := len(n.pending[memberID]) == 0
	n.pending[memberID] = append(n.pending[memberID], projectID)

	if first {
		n.wg.Add(1)
		n.timers[memberID] = time.AfterFunc(n.window, func() {
			defer n.wg.Done()

			n.fire(memberID)
		})
	}
}

func (n *ProjectAddedNotifier) fire(memberID uuid.UUID) {
	n.mu.Lock()
	projectIDs := n.pending[memberID]
	delete(n.pending, memberID)
	delete(n.timers, memberID)
	n.mu.Unlock()

	if len(projectIDs) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), projectAddedSendTimeout)
	defer cancel()

	stopWatching := make(chan struct{})
	defer close(stopWatching)

	go func() {
		select {
		case <-n.stopCh:
			cancel()
		case <-stopWatching:
		}
	}()

	member, err := n.members.ByID(ctx, memberID)
	if err != nil || member.Email == "" {
		n.logger.WarnContext(ctx, "project-added notifier: cannot resolve member for email",
			slog.String("member_id", memberID.String()), slog.Any("error", err))

		return
	}

	names := make([]string, 0, len(projectIDs))
	orgName := "your team"

	for i, pid := range projectIDs {
		project, err := n.projects.ByID(ctx, pid)
		if err != nil {
			continue
		}

		names = append(names, project.Name)

		if i == 0 && project.OrgID != uuid.Nil {
			if org, orgErr := n.orgs.ByID(ctx, project.OrgID); orgErr == nil && strings.TrimSpace(org.Name) != "" {
				orgName = org.Name
			}
		}
	}

	if err := n.mailer.Send(ctx, projectAddedEmail(member, orgName, names, n.baseURL)); err != nil {
		n.logger.WarnContext(ctx, "project-added notifier: send failed",
			slog.String("member_id", memberID.String()), slog.Any("error", err))

		return
	}

	n.logger.InfoContext(ctx, "project-added email sent",
		slog.String("member_id", memberID.String()), slog.Int("projects", len(projectIDs)))
}

// Close cancels and drains pending notifications.
func (n *ProjectAddedNotifier) Close() {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()

		return
	}

	n.closed = true
	close(n.stopCh)

	for memberID, timer := range n.timers {
		if timer.Stop() {
			n.wg.Done()
		}

		delete(n.timers, memberID)
		delete(n.pending, memberID)
	}

	n.mu.Unlock()
	n.wg.Wait()
}

func projectAddedEmail(member model.Member, orgName string, names []string, baseURL string) model.EmailMessage {
	projectsLabel := "a new project"

	switch len(names) {
	case 0:
	case 1:
		projectsLabel = names[0]
	default:
		projectsLabel = strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}

	subject := fmt.Sprintf("You've been added to %s on Orako", projectsLabel)
	dashboard := strings.TrimRight(baseURL, "/")

	var text strings.Builder

	fmt.Fprintf(&text, "Hi %s,\n\n", displayNameOrThere(member))
	fmt.Fprintf(&text, "You've been added to %s in %s on Orako.\n\n", projectsLabel, orgName)
	text.WriteString("Questions routed to these projects can now reach you where you chose to be contacted.\n\n")

	if dashboard != "" {
		fmt.Fprintf(&text, "Open the dashboard: %s\n\n", dashboard)
	}

	text.WriteString("— Orako\n")

	return model.EmailMessage{
		To:       member.Email,
		Subject:  subject,
		TextBody: text.String(),
		HTMLBody: "",
	}
}
