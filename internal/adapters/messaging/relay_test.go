// SPDX-License-Identifier: AGPL-3.0-or-later

package messaging

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	orakov1 "github.com/orako-io/core/gen/orako/v1"
)

// fakeOutbox is an in-memory outboxReader: a sorted event log plus one watermark.
type fakeOutbox struct {
	mu        sync.Mutex
	watermark int64
	seeded    bool
	head      int64 // max global_seq in the "log", for seeding
	events    []OutboxRow
	failAfter bool
}

func (f *fakeOutbox) SeedWatermark(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.seeded {
		f.seeded = true
		f.watermark = f.head
	}

	return nil
}

func (f *fakeOutbox) Watermark(_ context.Context, _ string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.watermark, nil
}

func (f *fakeOutbox) SetWatermark(_ context.Context, _ string, globalSeq int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.watermark = globalSeq

	return nil
}

func (f *fakeOutbox) EventsAfter(_ context.Context, after int64, limit int32) ([]OutboxRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failAfter {
		return nil, errors.New("boom")
	}

	var out []OutboxRow
	for _, e := range f.events {
		if e.GlobalSeq > after {
			out = append(out, e)
			if len(out) == int(limit) {
				break
			}
		}
	}

	return out, nil
}

// capturePub records published messages and can fail on the Nth publish.
type capturePub struct {
	mu        sync.Mutex
	published []*message.Message
	failOn    int // 1-based publish index to fail on; 0 = never
	n         int
}

func (p *capturePub) Publish(_ string, msgs ...*message.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, m := range msgs {
		p.n++
		if p.failOn != 0 && p.n == p.failOn {
			return errors.New("publish failed")
		}

		p.published = append(p.published, m)
	}

	return nil
}

func (p *capturePub) Close() error { return nil }

func (p *capturePub) ids() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]string, len(p.published))
	for i, m := range p.published {
		out[i] = m.UUID
	}

	return out
}

// row builds an OutboxRow with a valid protojson Envelope payload at globalSeq.
func row(t *testing.T, globalSeq int64) (OutboxRow, string) {
	t.Helper()

	eventID := uuid.NewString()
	env := &orakov1.Envelope{
		EventId:   eventID,
		ProjectId: uuid.NewString(),
		Type:      orakov1.EventType_EVENT_TYPE_MESSAGE_POSTED,
	}

	payload, err := protojson.Marshal(env)
	if err != nil {
		t.Fatalf("marshaling envelope: %v", err)
	}

	return OutboxRow{GlobalSeq: globalSeq, Payload: payload}, eventID
}

func newRelay(reader outboxReader, pub message.Publisher) *Relay {
	return NewRelay(reader, pub, nil, slog.New(slog.DiscardHandler))
}

func TestRelay_CatchUp_PublishesInOrderAndAdvances(t *testing.T) {
	t.Parallel()

	r1, id1 := row(t, 1)
	r2, id2 := row(t, 2)
	r3, id3 := row(t, 3)

	reader := &fakeOutbox{events: []OutboxRow{r1, r2, r3}}
	pub := &capturePub{}
	relay := newRelay(reader, pub)

	n, err := relay.catchUp(context.Background())
	if err != nil {
		t.Fatalf("catchUp: %v", err)
	}

	if n != 3 {
		t.Errorf("published = %d, want 3", n)
	}

	if got, want := pub.ids(), []string{id1, id2, id3}; !equalStrings(got, want) {
		t.Errorf("published ids = %v, want %v (in global_seq order)", got, want)
	}

	if reader.watermark != 3 {
		t.Errorf("watermark = %d, want 3", reader.watermark)
	}
}

func TestRelay_CatchUp_EmptyIsNoOp(t *testing.T) {
	t.Parallel()

	reader := &fakeOutbox{watermark: 10, head: 10}
	pub := &capturePub{}

	n, err := newRelay(reader, pub).catchUp(context.Background())
	if err != nil {
		t.Fatalf("catchUp: %v", err)
	}

	if n != 0 || len(pub.published) != 0 {
		t.Errorf("published %d messages, want 0", len(pub.published))
	}
}

func TestRelay_CatchUp_ResumesFromWatermark(t *testing.T) {
	t.Parallel()

	r1, _ := row(t, 1)
	r2, id2 := row(t, 2)
	r3, id3 := row(t, 3)

	// Watermark already at 1: only 2 and 3 should re-publish.
	reader := &fakeOutbox{watermark: 1, events: []OutboxRow{r1, r2, r3}}
	pub := &capturePub{}

	n, err := newRelay(reader, pub).catchUp(context.Background())
	if err != nil {
		t.Fatalf("catchUp: %v", err)
	}

	if n != 2 {
		t.Errorf("published = %d, want 2", n)
	}

	if got, want := pub.ids(), []string{id2, id3}; !equalStrings(got, want) {
		t.Errorf("published ids = %v, want %v", got, want)
	}
}

// A publish failure must stop the pass at the last successfully-delivered
// position, so the next pass resumes there and never skips the failed event.
func TestRelay_CatchUp_PublishFailureStopsAtWatermark(t *testing.T) {
	t.Parallel()

	r1, id1 := row(t, 1)
	r2, _ := row(t, 2)
	r3, _ := row(t, 3)

	reader := &fakeOutbox{events: []OutboxRow{r1, r2, r3}}
	pub := &capturePub{failOn: 2} // 1 delivers, 2 fails

	n, err := newRelay(reader, pub).catchUp(context.Background())
	if err == nil {
		t.Fatal("expected an error when a publish fails")
	}

	if n != 1 {
		t.Errorf("published = %d, want 1 (stopped at the failure)", n)
	}

	if got, want := pub.ids(), []string{id1}; !equalStrings(got, want) {
		t.Errorf("published ids = %v, want %v", got, want)
	}

	if reader.watermark != 1 {
		t.Errorf("watermark = %d, want 1 (not advanced past the failure)", reader.watermark)
	}
}

func TestRelay_Seed_PinsToHeadOnlyOnce(t *testing.T) {
	t.Parallel()

	reader := &fakeOutbox{head: 42}

	if err := newRelay(reader, &capturePub{}).Seed(context.Background()); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if reader.watermark != 42 {
		t.Errorf("watermark = %d, want 42 (seeded to head)", reader.watermark)
	}

	// A second seed after the watermark advanced must not reset it.
	reader.watermark = 100
	if err := newRelay(reader, &capturePub{}).Seed(context.Background()); err != nil {
		t.Fatalf("Seed (2nd): %v", err)
	}

	if reader.watermark != 100 {
		t.Errorf("watermark = %d, want 100 (seed must be a no-op once set)", reader.watermark)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
