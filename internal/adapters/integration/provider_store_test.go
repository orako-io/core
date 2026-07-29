// SPDX-License-Identifier: AGPL-3.0-or-later

package integration_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/integration"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestProjectProviderStoreUpsertAndLoad proves the full round-trip: an Upsert
// followed by a LoadSlack returns the stored credentials unchanged.
func TestProjectProviderStoreUpsertAndLoad(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)
	projectID := testsupport.SeedProject(t, pool)

	creds := integration.SlackCredentials{
		BotToken: "xoxb-test-token",
		TeamID:   "T0TEST",
		TeamName: "Test Team",
		AppID:    "A0TEST",
	}

	if err := store.UpsertSlack(t.Context(), projectID, creds); err != nil {
		t.Fatalf("UpsertSlack: %v", err)
	}

	got, err := store.LoadSlack(t.Context(), projectID)
	if err != nil {
		t.Fatalf("LoadSlack: %v", err)
	}

	if got.BotToken != creds.BotToken {
		t.Errorf("BotToken: got %q, want %q", got.BotToken, creds.BotToken)
	}

	if got.TeamID != creds.TeamID {
		t.Errorf("TeamID: got %q, want %q", got.TeamID, creds.TeamID)
	}

	if got.TeamName != creds.TeamName {
		t.Errorf("TeamName: got %q, want %q", got.TeamName, creds.TeamName)
	}

	if got.AppID != creds.AppID {
		t.Errorf("AppID: got %q, want %q", got.AppID, creds.AppID)
	}
}

// TestProjectProviderStoreUpsertIdempotent proves that calling UpsertSlack
// twice overwrites the credentials rather than returning an error (ON CONFLICT
// DO UPDATE semantics).
func TestProjectProviderStoreUpsertIdempotent(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)
	projectID := testsupport.SeedProject(t, pool)

	first := integration.SlackCredentials{
		BotToken: "xoxb-first",
		TeamID:   "T0FIRST",
		TeamName: "First Team",
		AppID:    "A0FIRST",
	}

	second := integration.SlackCredentials{
		BotToken: "xoxb-second",
		TeamID:   "T0SECOND",
		TeamName: "Second Team",
		AppID:    "A0SECOND",
	}

	if err := store.UpsertSlack(t.Context(), projectID, first); err != nil {
		t.Fatalf("first UpsertSlack: %v", err)
	}

	// Re-authorisation: overwrite with second credentials.
	if err := store.UpsertSlack(t.Context(), projectID, second); err != nil {
		t.Fatalf("second UpsertSlack: %v", err)
	}

	got, err := store.LoadSlack(t.Context(), projectID)
	if err != nil {
		t.Fatalf("LoadSlack: %v", err)
	}

	if got.BotToken != second.BotToken {
		t.Errorf("BotToken: got %q, want %q (second upsert should win)", got.BotToken, second.BotToken)
	}
}

// TestProjectProviderStoreLoadNotFound proves LoadSlack returns ErrNotFound
// when no Slack credentials have been stored for the project.
func TestProjectProviderStoreLoadNotFound(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)
	projectID := testsupport.SeedProject(t, pool)

	_, err := store.LoadSlack(t.Context(), projectID)
	if !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("LoadSlack (absent): got %v, want ErrNotFound", err)
	}
}

// ── Generic UpsertProvider / LoadProvider / LoadAllProviders ─────────────────

// TestProviderStore_UpsertAndLoad proves the generic round-trip:
// UpsertProvider followed by LoadProvider returns the correct kind and raw JSON.
func TestProviderStore_UpsertAndLoad(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)
	projectID := testsupport.SeedProject(t, pool)

	creds := map[string]string{
		"bot_token":      "xoxb-generic-test",
		"signing_secret": "sig_test",
	}

	credJSON, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	if err := store.UpsertProvider(t.Context(), projectID, "slack", credJSON, nil); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}

	gotCreds, err := store.LoadProvider(t.Context(), projectID, "slack")
	if err != nil {
		t.Fatalf("LoadProvider: %v", err)
	}

	var gotMap map[string]string
	if err := json.Unmarshal(gotCreds, &gotMap); err != nil {
		t.Fatalf("Unmarshal creds: %v", err)
	}

	if gotMap["bot_token"] != creds["bot_token"] {
		t.Errorf("bot_token: got %q, want %q", gotMap["bot_token"], creds["bot_token"])
	}

	if gotMap["signing_secret"] != creds["signing_secret"] {
		t.Errorf("signing_secret: got %q, want %q", gotMap["signing_secret"], creds["signing_secret"])
	}
}

// TestProviderStore_UpsertIsIdempotent proves that a second UpsertProvider
// with the same (project, kind) overwrites rather than erroring.
func TestProviderStore_UpsertIsIdempotent(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)
	projectID := testsupport.SeedProject(t, pool)

	creds1, _ := json.Marshal(map[string]string{"bot_token": "first", "signing_secret": "s1"})
	creds2, _ := json.Marshal(map[string]string{"bot_token": "second", "signing_secret": "s2"})

	if err := store.UpsertProvider(t.Context(), projectID, "slack", creds1, nil); err != nil {
		t.Fatalf("first UpsertProvider: %v", err)
	}

	if err := store.UpsertProvider(t.Context(), projectID, "slack", creds2, nil); err != nil {
		t.Fatalf("second UpsertProvider: %v", err)
	}

	gotCreds, err := store.LoadProvider(t.Context(), projectID, "slack")
	if err != nil {
		t.Fatalf("LoadProvider: %v", err)
	}

	var gotMap map[string]string
	if err := json.Unmarshal(gotCreds, &gotMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if gotMap["bot_token"] != "second" {
		t.Errorf("after upsert: bot_token = %q, want %q", gotMap["bot_token"], "second")
	}
}

// TestProviderStore_LoadProvider_NotFound proves LoadProvider returns ErrNotFound
// when no provider has been stored for the project.
func TestProviderStore_LoadProvider_NotFound(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)

	_, err := store.LoadProvider(t.Context(), uuid.New(), "slack") // random project — not seeded
	if !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// TestProviderStore_LoadProvider_WrongKindNotFound proves LoadProvider is
// kind-exact: a project configured for one kind must not satisfy a lookup for
// a different kind by falling back to whatever is stored (review fit#4 — the
// per-(project,kind) contract this store now implements).
func TestProviderStore_LoadProvider_WrongKindNotFound(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)
	projectID := testsupport.SeedProject(t, pool)

	creds, _ := json.Marshal(map[string]string{"bot_token": "xoxb-kind-exact"})

	if err := store.UpsertProvider(t.Context(), projectID, "slack", creds, nil); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}

	if _, err := store.LoadProvider(t.Context(), projectID, "discord"); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Errorf("LoadProvider(discord) on a slack-only project: got %v, want ErrNotFound", err)
	}
}

// TestProviderStore_LoadAllProviders proves LoadAllProviders returns rows for
// all configured projects, including multi-kind entries.
func TestProviderStore_LoadAllProviders(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)

	p1 := testsupport.SeedProject(t, pool)
	p2 := testsupport.SeedProject(t, pool)

	slackCreds, _ := json.Marshal(map[string]string{
		"bot_token":      "xoxb-all-test",
		"signing_secret": "sig",
	})
	telegramCreds, _ := json.Marshal(map[string]string{
		"bot_token": "123:ABC",
	})

	if err := store.UpsertProvider(t.Context(), p1, "slack", slackCreds, nil); err != nil {
		t.Fatalf("UpsertProvider(slack, p1): %v", err)
	}

	if err := store.UpsertProvider(t.Context(), p2, "telegram", telegramCreds, nil); err != nil {
		t.Fatalf("UpsertProvider(telegram, p2): %v", err)
	}

	rows, err := store.LoadAllProviders(t.Context())
	if err != nil {
		t.Fatalf("LoadAllProviders: %v", err)
	}

	kindsByProject := map[uuid.UUID]string{}

	for _, row := range rows {
		if row.ProjectID == p1 || row.ProjectID == p2 {
			kindsByProject[row.ProjectID] = row.Kind
		}
	}

	if kindsByProject[p1] != "slack" {
		t.Errorf("p1: kind = %q, want %q", kindsByProject[p1], "slack")
	}

	if kindsByProject[p2] != "telegram" {
		t.Errorf("p2: kind = %q, want %q", kindsByProject[p2], "telegram")
	}
}

// TestProviderStore_UpsertPersistsAlertChannelID proves the alert_channel_id
// column round-trips through UpsertProvider and is overwritten (not appended)
// on a repeat upsert for the same (project, kind).
func TestProviderStore_UpsertPersistsAlertChannelID(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)
	projectID := testsupport.SeedProject(t, pool)

	creds, _ := json.Marshal(map[string]string{"bot_token": "xoxb-alert-test", "signing_secret": "sig"})

	if err := store.UpsertProvider(t.Context(), projectID, "slack", creds, []string{"C0ALERTS"}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}

	var gotAlertChannelIDs []string
	if err := pool.QueryRow(t.Context(),
		`SELECT alert_channel_ids FROM project_providers WHERE project_id = $1 AND kind = 'slack'`, projectID,
	).Scan(&gotAlertChannelIDs); err != nil {
		t.Fatalf("scanning alert_channel_id: %v", err)
	}

	if len(gotAlertChannelIDs) != 1 || gotAlertChannelIDs[0] != "C0ALERTS" {
		t.Errorf("alert_channel_id: got %q, want %q", gotAlertChannelIDs, "C0ALERTS")
	}

	if err := store.UpsertProvider(t.Context(), projectID, "slack", creds, []string{"C0SECOND"}); err != nil {
		t.Fatalf("second UpsertProvider: %v", err)
	}

	if err := pool.QueryRow(t.Context(),
		`SELECT alert_channel_ids FROM project_providers WHERE project_id = $1 AND kind = 'slack'`, projectID,
	).Scan(&gotAlertChannelIDs); err != nil {
		t.Fatalf("scanning alert_channel_id (2nd): %v", err)
	}

	if len(gotAlertChannelIDs) != 1 || gotAlertChannelIDs[0] != "C0SECOND" {
		t.Errorf("alert_channel_id after re-upsert: got %q, want %q", gotAlertChannelIDs, "C0SECOND")
	}
}

// TestProviderStore_UpsertEmptyAlertChannelIDPreservesStoredValue proves the
// review #12 fix: a credential rotation (ConfigureProvider always resends
// the full credential map) that leaves the alert-channel field blank must
// not wipe a previously configured alert channel.
func TestProviderStore_UpsertEmptyAlertChannelIDPreservesStoredValue(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)
	projectID := testsupport.SeedProject(t, pool)

	firstCreds, _ := json.Marshal(map[string]string{"bot_token": "first-token"})

	if err := store.UpsertProvider(t.Context(), projectID, "telegram", firstCreds, []string{"C0ALERTS"}); err != nil {
		t.Fatalf("first UpsertProvider: %v", err)
	}

	// Credential rotation: same kind, new credentials, blank alert channel —
	// simulates a re-save where the UI form's alert-channel field was empty.
	rotatedCreds, _ := json.Marshal(map[string]string{"bot_token": "rotated-token"})

	if err := store.UpsertProvider(t.Context(), projectID, "telegram", rotatedCreds, nil); err != nil {
		t.Fatalf("rotation UpsertProvider: %v", err)
	}

	var gotAlertChannelIDs []string
	if err := pool.QueryRow(t.Context(),
		`SELECT alert_channel_ids FROM project_providers WHERE project_id = $1 AND kind = 'telegram'`, projectID,
	).Scan(&gotAlertChannelIDs); err != nil {
		t.Fatalf("scanning alert_channel_id: %v", err)
	}

	if len(gotAlertChannelIDs) != 1 || gotAlertChannelIDs[0] != "C0ALERTS" {
		t.Errorf("alert_channel_id after credential rotation with blank field: got %q, want unchanged %q", gotAlertChannelIDs, "C0ALERTS")
	}

	gotCreds, err := store.LoadProvider(t.Context(), projectID, "telegram")
	if err != nil {
		t.Fatalf("LoadProvider: %v", err)
	}

	var gotMap map[string]string
	if err := json.Unmarshal(gotCreds, &gotMap); err != nil {
		t.Fatalf("Unmarshal creds: %v", err)
	}

	if gotMap["bot_token"] != "rotated-token" {
		t.Errorf("bot_token after rotation: got %q, want %q (the rotation itself must still take effect)", gotMap["bot_token"], "rotated-token")
	}
}

// TestProviderStore_ConfiguredProvidersWithAlertChannel proves the read path
// for the phase-5 gap: each configured provider comes back with its stored
// alert_channel_id (empty when unset).
func TestProviderStore_ConfiguredProvidersWithAlertChannel(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)
	projectID := testsupport.SeedProject(t, pool)

	slackCreds, _ := json.Marshal(map[string]string{"bot_token": "xoxb-x", "signing_secret": "sig"})
	discordCreds, _ := json.Marshal(map[string]string{"bot_token": "discord-token"})

	if err := store.UpsertProvider(t.Context(), projectID, "slack", slackCreds, []string{"C0SLACKALERT"}); err != nil {
		t.Fatalf("UpsertProvider(slack): %v", err)
	}

	if err := store.UpsertProvider(t.Context(), projectID, "discord", discordCreds, nil); err != nil {
		t.Fatalf("UpsertProvider(discord): %v", err)
	}

	got, err := store.ConfiguredProvidersWithAlertChannel(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ConfiguredProvidersWithAlertChannel: %v", err)
	}

	byKind := map[string][]string{}
	for _, row := range got {
		byKind[row.Kind] = row.AlertChannelIDs
	}

	if len(byKind["slack"]) != 1 || byKind["slack"][0] != "C0SLACKALERT" {
		t.Errorf("slack alert channels: got %v, want [C0SLACKALERT]", byKind["slack"])
	}

	if got, ok := byKind["discord"]; !ok || len(got) != 0 {
		t.Errorf("discord alert channels: got %v (present=%v), want empty", got, ok)
	}
}

// ── Legacy Slack-specific tests (kept for backward compat) ────────────────────

// TestProjectProviderStoreLoadAllSlack proves LoadAllSlack returns all
// projects that have Slack credentials stored.
func TestProjectProviderStoreLoadAllSlack(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := integration.NewProjectProviderStore(pool, nil)

	p1 := testsupport.SeedProject(t, pool)
	p2 := testsupport.SeedProject(t, pool)
	p3 := testsupport.SeedProject(t, pool) // intentionally left without credentials

	creds1 := integration.SlackCredentials{BotToken: "xoxb-p1", TeamID: "T0P1", TeamName: "P1", AppID: "A0P1"}
	creds2 := integration.SlackCredentials{BotToken: "xoxb-p2", TeamID: "T0P2", TeamName: "P2", AppID: "A0P2"}

	if err := store.UpsertSlack(t.Context(), p1, creds1); err != nil {
		t.Fatalf("UpsertSlack p1: %v", err)
	}

	if err := store.UpsertSlack(t.Context(), p2, creds2); err != nil {
		t.Fatalf("UpsertSlack p2: %v", err)
	}

	all, err := store.LoadAllSlack(t.Context())
	if err != nil {
		t.Fatalf("LoadAllSlack: %v", err)
	}

	// p3 has no credentials; all must contain p1 and p2.
	if _, ok := all[p1]; !ok {
		t.Errorf("LoadAllSlack: missing p1 (%v)", p1)
	}

	if _, ok := all[p2]; !ok {
		t.Errorf("LoadAllSlack: missing p2 (%v)", p2)
	}

	if _, ok := all[p3]; ok {
		t.Errorf("LoadAllSlack: unexpected p3 in result (it has no credentials)")
	}

	if all[p1].BotToken != creds1.BotToken {
		t.Errorf("LoadAllSlack p1 BotToken: got %q, want %q", all[p1].BotToken, creds1.BotToken)
	}

	if all[p2].TeamID != creds2.TeamID {
		t.Errorf("LoadAllSlack p2 TeamID: got %q, want %q", all[p2].TeamID, creds2.TeamID)
	}
}
