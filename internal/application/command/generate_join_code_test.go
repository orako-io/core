// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// ---- fakes ---------------------------------------------------------------

type fakeJoinCodeMinter struct {
	code      string
	err       error
	gotOrg    uuid.UUID
	gotProj   uuid.UUID
	gotActor  uuid.UUID
	callCount int
}

func (f *fakeJoinCodeMinter) CreateJoinToken(_ context.Context, orgID, projectID, createdBy uuid.UUID) (string, error) {
	f.callCount++
	f.gotOrg, f.gotProj, f.gotActor = orgID, projectID, createdBy

	return f.code, f.err
}

type fakeJoinCodeProjects struct {
	byID       model.Project
	byIDErr    error
	defProj    model.Project
	defProjErr error
	byIDCalls  int
	defCalls   int
}

func (f *fakeJoinCodeProjects) ByID(_ context.Context, _ uuid.UUID) (model.Project, error) {
	f.byIDCalls++

	return f.byID, f.byIDErr
}

func (f *fakeJoinCodeProjects) DefaultProjectByOrg(_ context.Context, _ uuid.UUID) (model.Project, error) {
	f.defCalls++

	return f.defProj, f.defProjErr
}

// ---- tests ---------------------------------------------------------------

func TestGenerateJoinCodeUsesDefaultProjectWhenUnset(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	projID := uuid.New()
	actor := uuid.New()

	minter := &fakeJoinCodeMinter{code: "fresh-code"}
	projects := &fakeJoinCodeProjects{defProj: model.Project{ID: projID, Name: "default", OrgID: orgID}}

	h := MustNewGenerateJoinCodeHandler(minter, projects)

	res, err := h.Handle(context.Background(), GenerateJoinCodeCommand{OrgID: orgID, ActorMemberID: actor})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if res.Code != "fresh-code" {
		t.Errorf("Code = %q, want the minted code", res.Code)
	}

	if res.ProjectID != projID || res.ProjectName != "default" {
		t.Errorf("project = (%s,%q), want the org's default (%s,default)", res.ProjectID, res.ProjectName, projID)
	}

	if projects.defCalls != 1 || projects.byIDCalls != 0 {
		t.Errorf("expected the default project resolver only, got def=%d byID=%d", projects.defCalls, projects.byIDCalls)
	}

	// Rotation: the mint delegates to CreateJoinToken (which revokes-then-inserts)
	// with the resolved project and the actor.
	if minter.callCount != 1 || minter.gotOrg != orgID || minter.gotProj != projID || minter.gotActor != actor {
		t.Errorf("mint args = (org %s, proj %s, actor %s), want (%s,%s,%s)",
			minter.gotOrg, minter.gotProj, minter.gotActor, orgID, projID, actor)
	}
}

func TestGenerateJoinCodeUsesExplicitProjectInOrg(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	projID := uuid.New()

	minter := &fakeJoinCodeMinter{code: "code"}
	projects := &fakeJoinCodeProjects{byID: model.Project{ID: projID, Name: "backend", OrgID: orgID}}

	h := MustNewGenerateJoinCodeHandler(minter, projects)

	res, err := h.Handle(context.Background(), GenerateJoinCodeCommand{OrgID: orgID, ProjectID: projID})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if res.ProjectID != projID || res.ProjectName != "backend" {
		t.Errorf("project = (%s,%q), want the named project (%s,backend)", res.ProjectID, res.ProjectName, projID)
	}

	if projects.byIDCalls != 1 || projects.defCalls != 0 {
		t.Errorf("expected the ByID resolver only, got byID=%d def=%d", projects.byIDCalls, projects.defCalls)
	}

	if minter.gotProj != projID {
		t.Errorf("mint project = %s, want the named project %s", minter.gotProj, projID)
	}
}

func TestGenerateJoinCodeRejectsProjectOutsideOrg(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	otherOrg := uuid.New()
	projID := uuid.New()

	minter := &fakeJoinCodeMinter{code: "code"}
	// The named project belongs to a DIFFERENT org than the caller.
	projects := &fakeJoinCodeProjects{byID: model.Project{ID: projID, Name: "victim", OrgID: otherOrg}}

	h := MustNewGenerateJoinCodeHandler(minter, projects)

	_, err := h.Handle(context.Background(), GenerateJoinCodeCommand{OrgID: orgID, ProjectID: projID})

	var forb errs.ForbiddenError
	if !errors.As(err, &forb) {
		t.Fatalf("error = %v, want ForbiddenError for a cross-org project", err)
	}

	if minter.callCount != 0 {
		t.Error("a cross-org project must not mint a code")
	}
}

func TestGenerateJoinCodeRejectsNilOrg(t *testing.T) {
	t.Parallel()

	minter := &fakeJoinCodeMinter{code: "code"}
	h := MustNewGenerateJoinCodeHandler(minter, &fakeJoinCodeProjects{})

	_, err := h.Handle(context.Background(), GenerateJoinCodeCommand{OrgID: uuid.Nil})

	var inv errs.InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("error = %v, want InvalidError for a nil org", err)
	}

	if minter.callCount != 0 {
		t.Error("a nil org must not mint a code")
	}
}

func TestGenerateJoinCodeTranslatesMissingDefaultProject(t *testing.T) {
	t.Parallel()

	minter := &fakeJoinCodeMinter{code: "code"}
	projects := &fakeJoinCodeProjects{defProjErr: adaptererr.ErrNotFound}

	h := MustNewGenerateJoinCodeHandler(minter, projects)

	_, err := h.Handle(context.Background(), GenerateJoinCodeCommand{OrgID: uuid.New()})

	var nf errs.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("error = %v, want NotFoundError when the org has no default project", err)
	}

	if minter.callCount != 0 {
		t.Error("a missing default project must not mint a code")
	}
}
