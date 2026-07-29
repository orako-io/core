// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/repository"
)

func TestScopeProjects(t *testing.T) {
	t.Parallel()

	p1, p2, p3 := uuid.New(), uuid.New(), uuid.New()
	memberships := []repository.ProjectWithRole{{ID: p1, Name: "one"}, {ID: p2, Name: "two"}}

	// An empty scope means "all the member's projects" — unchanged.
	if got := scopeProjects(memberships, nil); len(got) != 2 {
		t.Fatalf("empty scope: got %d projects, want all 2", len(got))
	}

	// The scope drives order (so the first survivor is the primary) and drops a
	// non-member id: token scope [p2, p3, p1] over memberships {p1, p2} yields
	// [p2, p1] — p3 is intersected out (unreachable), never surfaced.
	got := scopeProjects(memberships, []uuid.UUID{p2, p3, p1})
	if ids := projectIDsOf(got); len(ids) != 2 || ids[0] != p2 || ids[1] != p1 {
		t.Fatalf("scope order/intersection: got %v, want [%s %s]", projectIDsOf(got), p2, p1)
	}

	// A single-project scope reaches exactly that project.
	if ids := projectIDsOf(scopeProjects(memberships, []uuid.UUID{p1})); len(ids) != 1 || ids[0] != p1 {
		t.Fatalf("single scope: got %v, want [%s]", ids, p1)
	}

	// A scope naming only a non-member project resolves to nothing reachable —
	// this is where a stale or forged token scope is rejected.
	if got := scopeProjects(memberships, []uuid.UUID{p3}); len(got) != 0 {
		t.Fatalf("non-member scope must be empty, got %v", projectIDsOf(got))
	}
}
