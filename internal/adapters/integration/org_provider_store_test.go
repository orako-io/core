// SPDX-License-Identifier: AGPL-3.0-or-later

package integration_test

import (
	"encoding/json"
	"errors"
	"testing"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/integration"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// botToken extracts the bot_token field from raw credential JSON for
// order-independent comparison (JSONB does not preserve key order).
func botToken(t *testing.T, raw []byte) string {
	t.Helper()

	var c struct {
		BotToken string `json:"bot_token"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal credentials: %v", err)
	}

	return c.BotToken
}

func TestOrgProviderStoreRoundTrip(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewOrgProviderStore(pool, nil)

	orgID := testsupport.SeedOrganization(t, pool)
	creds := []byte(`{"bot_token":"xoxb-test","team_id":"T1"}`)

	// Absent → not found.
	if _, err := store.LoadProvider(t.Context(), orgID, "slack"); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("LoadProvider(absent) err = %v, want ErrNotFound", err)
	}

	// Insert.
	if err := store.UpsertProvider(t.Context(), orgID, "slack", creds); err != nil {
		t.Fatalf("UpsertProvider (insert): %v", err)
	}

	got, err := store.LoadProvider(t.Context(), orgID, "slack")
	if err != nil {
		t.Fatalf("LoadProvider: %v", err)
	}

	if botToken(t, got) != "xoxb-test" {
		t.Errorf("bot_token = %q, want xoxb-test", botToken(t, got))
	}

	// Update (rotate) — one row per (org, kind), not a duplicate.
	rotated := []byte(`{"bot_token":"xoxb-rotated","team_id":"T1"}`)
	if err := store.UpsertProvider(t.Context(), orgID, "slack", rotated); err != nil {
		t.Fatalf("UpsertProvider (rotate): %v", err)
	}

	kinds, err := store.ConfiguredKinds(t.Context(), orgID)
	if err != nil {
		t.Fatalf("ConfiguredKinds: %v", err)
	}

	if len(kinds) != 1 || kinds[0] != "slack" {
		t.Errorf("ConfiguredKinds = %v, want [slack] (rotation must not duplicate)", kinds)
	}

	got, _ = store.LoadProvider(t.Context(), orgID, "slack")
	if botToken(t, got) != "xoxb-rotated" {
		t.Errorf("after rotate, bot_token = %q, want xoxb-rotated", botToken(t, got))
	}

	// LoadAllProviders includes the row.
	all, err := store.LoadAllProviders(t.Context())
	if err != nil {
		t.Fatalf("LoadAllProviders: %v", err)
	}

	found := false
	for _, r := range all {
		if r.OrgID == orgID && r.Kind == "slack" {
			found = true
		}
	}

	if !found {
		t.Error("LoadAllProviders did not include the org's slack provider")
	}

	// Delete is idempotent.
	if err := store.DeleteProvider(t.Context(), orgID, "slack"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}

	if err := store.DeleteProvider(t.Context(), orgID, "slack"); err != nil {
		t.Fatalf("DeleteProvider (idempotent): %v", err)
	}

	if _, err := store.LoadProvider(t.Context(), orgID, "slack"); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("after delete, LoadProvider err = %v, want ErrNotFound", err)
	}
}
