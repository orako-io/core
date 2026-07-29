// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/pkg/edition"
	"github.com/orako-io/core/internal/pkg/errs"
)

// The Community edition (free self-host: no billing, no valid license) enforces
// hard resource caps of 5 members / 1 org / 1 project. Licensed and privately
// managed deployments provide their own limits.

// orgCounter counts organizations in the instance (satisfied by
// *identity.OrganizationStore).
type orgCounter interface {
	CountAll(ctx context.Context) (int, error)
}

// projectCounter counts an org's projects (satisfied by *identity.ProjectStore).
type projectCounter interface {
	CountByOrg(ctx context.Context, orgID uuid.UUID) (int, error)
}

// orgMemberCounter counts an org's active members (satisfied by
// *identity.OrganizationStore).
type orgMemberCounter interface {
	CountByOrg(ctx context.Context, orgID uuid.UUID) (int, error)
}

// communityLimitErr is the typed error a cap breach returns. It maps through
// the existing errs.InvalidError channel (no new transport wiring) and its
// message doubles as the dashboard upgrade prompt.
func communityLimitErr(resource string, limit int) error {
	return errs.InvalidError{
		Field: resource,
		Reason: fmt.Sprintf(
			"the free self-host edition is limited to %d %s; add a license key in Settings → License to raise the limit — see https://orako.io/pricing",
			limit, resource,
		),
	}
}

// CommunityMemberGate caps members per org at the community limit. It satisfies
// SeatGate, so it drops into the existing add-member seam with no
// handler change.
type CommunityMemberGate struct {
	projects projectOrgReader
	members  orgMemberCounter
	max      int
}

// AllowNewMember blocks once the project's org holds `max` members. A project
// with no org (legacy) is never gated.
func (g CommunityMemberGate) AllowNewMember(ctx context.Context, projectID uuid.UUID) error {
	if g.max <= 0 {
		return nil // 0/negative = unlimited; self-safe against a zero-value gate.
	}

	project, err := g.projects.ByID(ctx, projectID)
	if err != nil {
		return translateGateErr(err)
	}

	if project.OrgID == uuid.Nil {
		return nil
	}

	n, err := g.members.CountByOrg(ctx, project.OrgID)
	if err != nil {
		return translateGateErr(err)
	}

	if n >= g.max {
		return communityLimitErr("members", g.max)
	}

	return nil
}

// CommunityOrgGate caps the instance at `max` organizations.
type CommunityOrgGate struct {
	orgs orgCounter
	max  int
}

// AllowNewOrg blocks once the instance holds `max` organizations.
func (g CommunityOrgGate) AllowNewOrg(ctx context.Context) error {
	if g.max <= 0 {
		return nil // 0/negative = unlimited.
	}

	n, err := g.orgs.CountAll(ctx)
	if err != nil {
		return errs.InternalError{Err: err}
	}

	if n >= g.max {
		return communityLimitErr("organizations", g.max)
	}

	return nil
}

// CommunityProjectGate caps an org at `max` projects.
type CommunityProjectGate struct {
	projects projectCounter
	max      int
}

// AllowNewProject blocks once the org holds `max` projects. The org's
// auto-created "default" project counts toward the cap.
func (g CommunityProjectGate) AllowNewProject(ctx context.Context, orgID uuid.UUID) error {
	if g.max <= 0 {
		return nil // 0/negative = unlimited.
	}

	if orgID == uuid.Nil {
		return nil
	}

	n, err := g.projects.CountByOrg(ctx, orgID)
	if err != nil {
		return errs.InternalError{Err: err}
	}

	if n >= g.max {
		return communityLimitErr("projects", g.max)
	}

	return nil
}

// instanceSeatCounter counts distinct billable persons across the whole instance
// (satisfied by *identity.OrganizationStore).
type instanceSeatCounter interface {
	CountInstanceSeats(ctx context.Context) (int, error)
}

// LicensedSeatGate caps members INSTANCE-WIDE at the license's seat count — a paid
// self-host is one customer with N seats across all its orgs. Satisfies
// SeatGate.
type LicensedSeatGate struct {
	seats instanceSeatCounter
	max   int
}

// AllowNewMember blocks once the instance is at its licensed seat count.
func (g LicensedSeatGate) AllowNewMember(ctx context.Context, _ uuid.UUID) error {
	if g.max <= 0 {
		return nil
	}

	n, err := g.seats.CountInstanceSeats(ctx)
	if err != nil {
		return errs.InternalError{Err: err}
	}

	if n >= g.max {
		return errs.InvalidError{
			Field:  "seats",
			Reason: fmt.Sprintf("your license covers %d seats; upgrade your plan to add more — see https://orako.io/pricing", g.max),
		}
	}

	return nil
}

// BuildMemberGate returns the member-add gate for the resolved edition: the
// billing seat gate under SaaS, the community member cap under Community, or nil
// (no enforcement) for a Licensed instance.
func BuildMemberGate(pool *pgxpool.Pool, projects projectOrgReader, ed edition.Edition) SeatGate {
	orgs := identity.NewOrganizationStore(pool)

	switch ed.Kind {
	case edition.SaaS:
		// The SaaS billing seat gate is injected by the SaaS build (application.New
		// takes it as saasGate); core carries no billing gate.
		return nil
	case edition.Community:
		// Community caps members PER ORG (5/org).
		if ed.Limits.MaxMembers > 0 {
			return CommunityMemberGate{projects: projects, members: orgs, max: ed.Limits.MaxMembers}
		}
	case edition.Licensed:
		// A paid license caps seats INSTANCE-WIDE (1 customer = N seats, all orgs).
		if ed.Limits.MaxMembers > 0 {
			return LicensedSeatGate{seats: identity.NewOrganizationStore(pool), max: ed.Limits.MaxMembers}
		}
	}

	return nil
}

// BuildOrgGate returns the org-creation gate: the community cap under Community,
// else nil.
func BuildOrgGate(pool *pgxpool.Pool, ed edition.Edition) OrgLimitGate {
	if ed.Enforced() && ed.Limits.MaxOrgs > 0 {
		return CommunityOrgGate{orgs: identity.NewOrganizationStore(pool), max: ed.Limits.MaxOrgs}
	}

	return nil
}

// BuildProjectGate returns the project-creation gate: the community cap under
// Community, else nil.
func BuildProjectGate(pool *pgxpool.Pool, ed edition.Edition) ProjectLimitGate {
	if ed.Enforced() && ed.Limits.MaxProjects > 0 {
		return CommunityProjectGate{projects: identity.NewProjectStore(pool), max: ed.Limits.MaxProjects}
	}

	return nil
}

// ── Live gates ───────────────────────────────────────────────────────────────
//
// The Build* gates above are resolved from a fixed edition (boot-time). The Live*
// wrappers below re-derive the right gate from the CURRENT edition on every check
// by calling the same Build* funcs with live.Current(). So a license that expires
// at runtime (self-host stops paying → key past ExpiresAt → reverts to Community)
// starts enforcing the community caps without needing a restart. A nil concrete
// gate (SaaS, or an unlimited axis) means "allow".

// NewLiveMemberGate returns a SeatGate that re-resolves per member-add.
func NewLiveMemberGate(pool *pgxpool.Pool, projects projectOrgReader, live *edition.Live) SeatGate {
	return liveMemberGate{pool: pool, projects: projects, live: live}
}

type liveMemberGate struct {
	pool     *pgxpool.Pool
	projects projectOrgReader
	live     *edition.Live
}

func (g liveMemberGate) AllowNewMember(ctx context.Context, projectID uuid.UUID) error {
	gate := BuildMemberGate(g.pool, g.projects, g.live.Current())
	if gate == nil {
		return nil
	}

	return gate.AllowNewMember(ctx, projectID)
}

// NewLiveOrgGate returns an OrgLimitGate that re-resolves per org-create.
func NewLiveOrgGate(pool *pgxpool.Pool, live *edition.Live) OrgLimitGate {
	return liveOrgGate{pool: pool, live: live}
}

type liveOrgGate struct {
	pool *pgxpool.Pool
	live *edition.Live
}

func (g liveOrgGate) AllowNewOrg(ctx context.Context) error {
	gate := BuildOrgGate(g.pool, g.live.Current())
	if gate == nil {
		return nil
	}

	return gate.AllowNewOrg(ctx)
}

// NewLiveProjectGate returns a ProjectLimitGate that re-resolves per project-create.
func NewLiveProjectGate(pool *pgxpool.Pool, live *edition.Live) ProjectLimitGate {
	return liveProjectGate{pool: pool, live: live}
}

type liveProjectGate struct {
	pool *pgxpool.Pool
	live *edition.Live
}

func (g liveProjectGate) AllowNewProject(ctx context.Context, orgID uuid.UUID) error {
	gate := BuildProjectGate(g.pool, g.live.Current())
	if gate == nil {
		return nil
	}

	return gate.AllowNewProject(ctx, orgID)
}

// translateGateErr maps an adapter not-found to a domain not-found; anything
// else is internal.
func translateGateErr(err error) error {
	if errors.Is(err, adaptererr.ErrNotFound) {
		return errs.NotFoundError{Resource: "project"}
	}

	return errs.InternalError{Err: err}
}
