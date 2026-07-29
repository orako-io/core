// SPDX-License-Identifier: AGPL-3.0-or-later

package integration_test

import (
	"errors"
	"testing"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/adapters/integration"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

func TestOrgResolvingProviderLoader(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	loader := integration.NewOrgResolvingProviderLoader(identity.NewProjectStore(pool), integration.NewOrgProviderStore(pool, nil))

	orgID := testsupport.SeedOrganization(t, pool)
	projectID := testsupport.SeedProjectInOrg(t, pool, orgID)
	creds := []byte(`{"bot_token":"xoxb-org"}`)

	// No org provider yet → not found for the project.
	if _, err := loader.LoadProvider(t.Context(), projectID, "slack"); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("LoadProvider before config: err = %v, want ErrNotFound", err)
	}

	// Configure the org's provider; the project now resolves through it.
	if err := integration.NewOrgProviderStore(pool, nil).UpsertProvider(t.Context(), orgID, "slack", creds); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}

	got, err := loader.LoadProvider(t.Context(), projectID, "slack")
	if err != nil {
		t.Fatalf("LoadProvider: %v", err)
	}

	if botToken(t, got) != "xoxb-org" {
		t.Errorf("resolved bot_token = %q, want xoxb-org", botToken(t, got))
	}

	// A second project in the same org resolves to the same org credentials.
	project2 := testsupport.SeedProjectInOrg(t, pool, orgID)
	got2, err := loader.LoadProvider(t.Context(), project2, "slack")
	if err != nil {
		t.Fatalf("LoadProvider (project 2): %v", err)
	}

	if botToken(t, got2) != "xoxb-org" {
		t.Errorf("project 2 resolved bot_token = %q, want xoxb-org (shared org connection)", botToken(t, got2))
	}
}
