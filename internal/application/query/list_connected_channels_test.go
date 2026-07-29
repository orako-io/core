// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// fakeConfiguredChannelsReader is an in-memory ConfiguredChannelsReader.
type fakeConfiguredChannelsReader struct {
	kinds []string
	err   error
}

func (f *fakeConfiguredChannelsReader) ConfiguredKinds(_ context.Context, _ uuid.UUID) ([]string, error) {
	return f.kinds, f.err
}

// TestListConnectedChannels_AlwaysIncludesDashboard proves the dashboard
// channel is always offered, even with no external provider configured.
func TestListConnectedChannels_AlwaysIncludesDashboard(t *testing.T) {
	t.Parallel()

	h := MustNewListConnectedChannelsHandler(&fakeConfiguredChannelsReader{kinds: nil})

	got, err := h.Handle(context.Background(), ListConnectedChannelsQuery{ProjectID: uuid.New()})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if !slices.Contains(got, "dashboard") {
		t.Errorf("channels %v must always include dashboard", got)
	}

	if len(got) != 1 {
		t.Errorf("channels %v: want exactly [dashboard] with no configured provider", got)
	}
}

// TestListConnectedChannels_ReportsAllFunctionalProviders proves every real
// adapter kind (slack, teams, telegram, discord) is offered once configured —
// as of phase 4, none of them are stubs anymore.
func TestListConnectedChannels_ReportsAllFunctionalProviders(t *testing.T) {
	t.Parallel()

	cases := []string{"slack", "teams", "telegram", "discord"}

	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			h := MustNewListConnectedChannelsHandler(&fakeConfiguredChannelsReader{kinds: []string{kind}})

			got, err := h.Handle(context.Background(), ListConnectedChannelsQuery{ProjectID: uuid.New()})
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}

			if !slices.Contains(got, kind) {
				t.Errorf("channels %v must include configured kind %q", got, kind)
			}

			if !slices.Contains(got, "dashboard") {
				t.Errorf("channels %v must still include dashboard", got)
			}
		})
	}
}

// TestListConnectedChannels_PropagatesReaderError proves a store failure
// surfaces rather than being swallowed.
func TestListConnectedChannels_PropagatesReaderError(t *testing.T) {
	t.Parallel()

	h := MustNewListConnectedChannelsHandler(&fakeConfiguredChannelsReader{err: errors.New("db unreachable")})

	if _, err := h.Handle(context.Background(), ListConnectedChannelsQuery{ProjectID: uuid.New()}); err == nil {
		t.Fatal("want error when the reader fails, got nil")
	}
}
