// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/domain/repository"
)

func TestListProjects_ReturnsMemberships(t *testing.T) {
	t.Parallel()

	memberID := uuid.New()
	p1 := uuid.New()
	p2 := uuid.New()

	reader := &fakeProjectsByMemberReader{
		projects: []repository.ProjectWithRole{
			{ID: p1, Name: "alpha", Role: model.RoleDev},
			{ID: p2, Name: "beta", Role: model.RoleLead},
		},
	}

	h := MustNewListProjectsHandler(reader)

	summaries, err := h.Handle(t.Context(), ListProjectsQuery{CallerMemberID: memberID})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(summaries) != 2 {
		t.Fatalf("got %d summaries, want 2", len(summaries))
	}

	if summaries[0].ID != p1 || summaries[0].Name != "alpha" || summaries[0].Role != model.RoleDev {
		t.Errorf("summaries[0] = %+v, want {%v alpha dev}", summaries[0], p1)
	}

	if summaries[1].ID != p2 || summaries[1].Name != "beta" || summaries[1].Role != model.RoleLead {
		t.Errorf("summaries[1] = %+v, want {%v beta lead}", summaries[1], p2)
	}
}

func TestListProjects_NoMemberships_ReturnsEmpty(t *testing.T) {
	t.Parallel()

	reader := &fakeProjectsByMemberReader{}
	h := MustNewListProjectsHandler(reader)

	summaries, err := h.Handle(t.Context(), ListProjectsQuery{CallerMemberID: uuid.New()})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(summaries) != 0 {
		t.Errorf("got %d summaries, want 0", len(summaries))
	}
}
