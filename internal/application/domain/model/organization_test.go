// SPDX-License-Identifier: AGPL-3.0-or-later

package model_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
)

func TestNewOrganization(t *testing.T) {
	t.Parallel()

	org, err := model.NewOrganization(uuid.New(), "Acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if org.Name != "Acme" || org.CreatedAt.IsZero() {
		t.Errorf("org = %+v", org)
	}
}

func TestNewOrganization_Invalid(t *testing.T) {
	t.Parallel()

	if _, err := model.NewOrganization(uuid.Nil, "Acme"); err == nil {
		t.Error("expected error for nil id")
	}

	if _, err := model.NewOrganization(uuid.New(), "  "); err == nil {
		t.Error("expected error for blank name")
	}
}

func TestNewAccount(t *testing.T) {
	t.Parallel()

	acc, err := model.NewAccount(uuid.New(), "sarah@example.com", "sub-1", "Sarah")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if acc.Email != "sarah@example.com" || acc.Subject != "sub-1" || acc.DisplayName != "Sarah" {
		t.Errorf("account = %+v", acc)
	}
}

func TestNewAccount_RequiresEmail(t *testing.T) {
	t.Parallel()

	if _, err := model.NewAccount(uuid.New(), "", "sub", "name"); err == nil {
		t.Error("expected error for empty email")
	}

	if _, err := model.NewAccount(uuid.Nil, "e@x.com", "", ""); err == nil {
		t.Error("expected error for nil id")
	}
}

func TestOrgRole_Valid(t *testing.T) {
	t.Parallel()

	for _, r := range []model.OrgRole{model.OrgRoleAdmin, model.OrgRoleMember} {
		if !r.Valid() {
			t.Errorf("%q should be valid", r)
		}
	}

	if model.OrgRole("owner").Valid() {
		t.Error(`"owner" should not be valid`)
	}
}
