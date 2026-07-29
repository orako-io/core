// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/service"
)

// fakeProviderAlertChannelsReader is an in-memory service.ProviderAlertChannelsReader.
type fakeProviderAlertChannelsReader struct {
	rows []service.ProviderAlertChannel
	err  error
}

func (f *fakeProviderAlertChannelsReader) ConfiguredProvidersWithAlertChannel(_ context.Context, _ uuid.UUID) ([]service.ProviderAlertChannel, error) {
	return f.rows, f.err
}

// TestGetProviderAlertChannels_ReturnsReaderRows proves Handle passes through
// the reader's rows unchanged.
func TestGetProviderAlertChannels_ReturnsReaderRows(t *testing.T) {
	t.Parallel()

	want := []service.ProviderAlertChannel{
		{Kind: "slack", AlertChannelIDs: []string{"C0SLACK"}},
		{Kind: "discord", AlertChannelIDs: nil},
	}

	h := MustNewGetProviderAlertChannelsHandler(&fakeProviderAlertChannelsReader{rows: want})

	got, err := h.Handle(context.Background(), GetProviderAlertChannelsQuery{ProjectID: uuid.New()})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}

	for i, row := range want {
		if !reflect.DeepEqual(got[i], row) {
			t.Errorf("row %d: got %+v, want %+v", i, got[i], row)
		}
	}
}

// TestGetProviderAlertChannels_PropagatesReaderError proves a store failure
// surfaces rather than being swallowed.
func TestGetProviderAlertChannels_PropagatesReaderError(t *testing.T) {
	t.Parallel()

	h := MustNewGetProviderAlertChannelsHandler(&fakeProviderAlertChannelsReader{err: errors.New("db unreachable")})

	if _, err := h.Handle(context.Background(), GetProviderAlertChannelsQuery{ProjectID: uuid.New()}); err == nil {
		t.Fatal("want error when the reader fails, got nil")
	}
}
