// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
	"github.com/orako-io/core/internal/adapters/messaging"
	"github.com/orako-io/core/internal/application/domain/repository"
)

type fixedAuthenticator struct {
	identity CallerIdentity
	err      error
}

func (f fixedAuthenticator) Authenticate(_ context.Context, _ string) (CallerIdentity, error) {
	return f.identity, f.err
}

func (f fixedAuthenticator) AuthenticateAccount(_ context.Context, _ string) (CallerIdentity, error) {
	return f.identity, f.err
}

func publishEnvelope(t *testing.T, pub message.Publisher, projectID uuid.UUID) {
	t.Helper()

	env := &orakov1.Envelope{
		ProjectId: projectID.String(),
		Type:      orakov1.EventType_EVENT_TYPE_PRESENCE_CHANGED,
		Payload: &orakov1.Envelope_PresenceChanged{
			PresenceChanged: &orakov1.PresenceChanged{MemberId: uuid.NewString(), Online: true},
		},
	}

	payload, err := proto.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}

	if err := pub.Publish(messaging.EventsTopic, message.NewMessage(uuid.NewString(), payload)); err != nil {
		t.Fatal(err)
	}
}

func TestEventsSSEStreamsOwnProjectOnly(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	ownProject := uuid.New()
	foreignProject := uuid.New()

	bus := gochannel.NewGoChannel(gochannel.Config{}, watermill.NopLogger{})

	handler := NewEventsSSEHandler(
		fixedAuthenticator{identity: CallerIdentity{MemberID: memberID}},
		bus,
		fakeMemberships{rows: []repository.ProjectWithRole{{ID: ownProject}}},
		newTestLogger(),
	)

	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	reader := bufio.NewReader(resp.Body)

	// Wait for the connected comment so the subscription is live before
	// publishing.
	if line, err := reader.ReadString('\n'); err != nil || !strings.HasPrefix(line, ": connected") {
		t.Fatalf("expected connected comment, got %q (%v)", line, err)
	}

	publishEnvelope(t, bus, foreignProject) // must be dropped
	publishEnvelope(t, bus, ownProject)     // must arrive

	var eventLine string

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended early: %v", err)
		}

		if strings.HasPrefix(line, "event: ") {
			eventLine = strings.TrimSpace(line)

			break
		}
	}

	if eventLine != "event: presence_changed" {
		t.Errorf("event line = %q", eventLine)
	}

	dataLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(dataLine, ownProject.String()) {
		t.Errorf("data does not carry the own project: %q", dataLine)
	}

	if strings.Contains(dataLine, foreignProject.String()) {
		t.Errorf("foreign project leaked into the stream: %q", dataLine)
	}
}

func TestEventsSSERejectsUnauthenticated(t *testing.T) {
	t.Parallel()

	bus := gochannel.NewGoChannel(gochannel.Config{}, watermill.NopLogger{})

	handler := NewEventsSSEHandler(
		fixedAuthenticator{err: errors.New("nope")},
		bus,
		fakeMemberships{},
		newTestLogger(),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/events/stream", nil))

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestEventsSSERejectsAccountOnly(t *testing.T) {
	t.Parallel()

	bus := gochannel.NewGoChannel(gochannel.Config{}, watermill.NopLogger{})

	handler := NewEventsSSEHandler(
		fixedAuthenticator{identity: CallerIdentity{MemberID: uuid.Nil}},
		bus,
		fakeMemberships{},
		newTestLogger(),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/events/stream", nil))

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401 for a member-less identity", rec.Code)
	}
}
