// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/repository"
)

type fakeActiveJoinCodeReader struct {
	token repository.JoinToken
	ok    bool
	err   error
}

func (f fakeActiveJoinCodeReader) ActiveJoinToken(_ context.Context, _ uuid.UUID) (repository.JoinToken, bool, error) {
	return f.token, f.ok, f.err
}

type fakeJoinCodeProjectNamer struct {
	name string
	err  error
}

func (f fakeJoinCodeProjectNamer) ReadProjectName(_ context.Context, _ uuid.UUID) (string, error) {
	return f.name, f.err
}

func TestGetJoinCodeReturnsNoneWhenAbsent(t *testing.T) {
	t.Parallel()

	h := MustNewGetJoinCodeHandler(fakeActiveJoinCodeReader{ok: false}, fakeJoinCodeProjectNamer{})

	view, err := h.Handle(context.Background(), GetJoinCodeQuery{OrgID: uuid.New()})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if view.Active {
		t.Error("Active = true, want false when the org has no live code")
	}

	if view.Code != "" || view.ProjectID != uuid.Nil {
		t.Errorf("expected an empty inactive view, got code=%q project=%s", view.Code, view.ProjectID)
	}
}

func TestGetJoinCodeReturnsActiveWithProjectName(t *testing.T) {
	t.Parallel()

	projID := uuid.New()
	reader := fakeActiveJoinCodeReader{
		token: repository.JoinToken{Token: "live-code", OrgID: uuid.New(), ProjectID: projID},
		ok:    true,
	}

	h := MustNewGetJoinCodeHandler(reader, fakeJoinCodeProjectNamer{name: "default"})

	view, err := h.Handle(context.Background(), GetJoinCodeQuery{OrgID: uuid.New()})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if !view.Active {
		t.Error("Active = false, want true for a live code")
	}

	if view.Code != "live-code" || view.ProjectID != projID || view.ProjectName != "default" {
		t.Errorf("view = (%q,%s,%q), want (live-code,%s,default)", view.Code, view.ProjectID, view.ProjectName, projID)
	}
}
