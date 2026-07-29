// SPDX-License-Identifier: AGPL-3.0-or-later

// Package application_test contains integration tests that exercise the full
// composed application against a real Postgres database.
//
// These tests skip automatically when Postgres is unreachable. Set
// ORAKO_INTEGRATION=1 to turn an unreachable Postgres into a hard failure
// (used inside `make up` where the service is guaranteed present).
package application_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/blobstore"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/adapters/mail"
	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/application"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/pkg/edition"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestCoreLoopEndToEnd exercises the full Orako core loop against a real
// Postgres database:
//
//  1. CreateProject  — project seeded
//  2. AddMember      — asker + responder enrolled
//  3. Ask  — conversation opened (noop provider: silent delivery)
//  4. FollowUp       — responder follow-up appended
//  5. GetConversation — park-and-resume read verifies messages in order
//  6. ResolveConversation — conversation resolved (status + final metadata)
//  7. SearchHistory — the resolved conversation is findable
//
// The noop provider accepts Deliver calls without side-effects.
func TestCoreLoopEndToEnd(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t) // skips without DB

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create a provider registry with a real member lookup (no read-through
	// needed for this test) — ForMember must resolve each responder's actual
	// delivery channel (dashboard, by AddMember's default) rather than falling
	// back to an ambiguous "any provider configured for the project" guess.
	reg, regErr := provider.New(nil, provider.Deps{Members: identity.NewMemberStore(pool)}, nil)
	if regErr != nil {
		t.Fatalf("provider.New: %v", regErr)
	}

	app, err := application.New(pool, reg, mail.NewNoop(log), nil, blobstore.Noop{}, nil, "http://localhost:8080", edition.NewLive(edition.Edition{}), nil, nil, log)
	if err != nil {
		t.Fatalf("application.New: %v", err)
	}

	defer func() {
		if cerr := app.Close(); cerr != nil {
			t.Logf("app.Close: %v", cerr)
		}
	}()

	// Start the event router so the GoChannel bus drains published events.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	routerErr := make(chan error, 1)

	go func() { routerErr <- app.RunEvents(ctx) }()

	// Wait until the router is ready to process messages.
	select {
	case <-app.Running():
	case err := <-routerErr:
		t.Fatalf("event router failed to start: %v", err)
	}

	// 1. Project (bare, no org) — created directly via the store. CreateProject
	// now requires an org + creator member, which this conversation-flow test
	// does not set up.
	projectID := uuid.New()

	proj, err := model.NewProject(projectID, "e2e-test-project")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	if err := identity.NewProjectStore(pool).Create(t.Context(), proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// 2. AddMember — enrol asker and responder (with expertise tags).
	askerRes, err := app.Commands.AddMember.Handle(t.Context(), command.AddMemberCommand{
		ProjectID:   projectID,
		Email:       "agent@e2e.test",
		DisplayName: "Agent",
		Domains:     []string{},
	})
	if err != nil {
		t.Fatalf("AddMember (asker): %v", err)
	}

	specialistRes, err := app.Commands.AddMember.Handle(t.Context(), command.AddMemberCommand{
		ProjectID:   projectID,
		Email:       "responder@e2e.test",
		DisplayName: "Expert",
		Domains:     []string{"architecture"},
	})
	if err != nil {
		t.Fatalf("AddMember (responder): %v", err)
	}

	// A freshly added member is 'invited'; Ask only routes to an
	// ACTIVE responder, so activate them (what accepting the invite does).
	for _, id := range []uuid.UUID{askerRes.MemberID, specialistRes.MemberID} {
		if err = app.Commands.SetMemberActivation.Handle(t.Context(), command.SetMemberActivationCommand{
			MemberID: id,
			Active:   true,
		}); err != nil {
			t.Fatalf("SetMemberActivation: %v", err)
		}
	}

	// 3. Ask — open a conversation (noop provider: Deliver is a no-op).
	// The handler now pre-checks that a provider is configured; register the
	// noop kind so the check passes without real credentials.
	if err = reg.RegisterFromMap(projectID, "noop", map[string]string{}); err != nil {
		t.Fatalf("RegisterFromMap noop: %v", err)
	}

	const question = "Why does the Orako server never call an LLM?"

	askRes, err := app.Commands.Ask.Handle(t.Context(), command.AskCommand{
		ProjectID:         projectID,
		AskerMemberID:     askerRes.MemberID,
		ResponderMemberID: specialistRes.MemberID,
		Question:          question,
		Context:           "Reviewing architectural decisions for the Orako MVP.",
		Summary:           "why the orako server stays llm-free",
		Tags:              []string{"architecture", "llm-free"},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	convID := askRes.ConversationID

	// 4. FollowUp — simulates an inbound reply from the responder.
	const answer = "The Orako server is LLM-free by design: it stores, routes, and retrieves; LLM work belongs to the calling agent. This keeps variable cost at zero."

	if _, err = app.Commands.FollowUp.Handle(t.Context(), command.FollowUpCommand{
		ConversationID: convID,
		AuthorMemberID: specialistRes.MemberID,
		Message:        answer,
	}); err != nil {
		t.Fatalf("FollowUp: %v", err)
	}

	// 5. GetConversation — verify messages are present and ordered. The asker is
	//    a party to the conversation, so the visibility check admits them.
	view, err := app.Queries.GetConversation.Handle(t.Context(), query.GetConversationQuery{
		ConversationID: convID,
		CallerMemberID: askerRes.MemberID,
	})
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}

	// The responder's message is an answer: the dialogue state machine parks
	// the conversation on the asker's side (status answered, still repliable).
	if view.Status != model.ConversationStatusAnswered {
		t.Errorf("Status: got %v, want Answered", view.Status)
	}

	// At minimum: the initial question message + the follow-up.
	if len(view.Messages) < 2 {
		t.Fatalf("Messages: got %d, want >= 2", len(view.Messages))
	}

	if view.Messages[0].Role != model.MessageRoleQuestion {
		t.Errorf("Messages[0].Role: got %v, want Question", view.Messages[0].Role)
	}

	if view.Messages[0].Body != question {
		t.Errorf("Messages[0].Body: got %q, want %q", view.Messages[0].Body, question)
	}

	// 6. ResolveConversation — agent supplies the distilled resolution; Orako
	//    stores it verbatim as the thread's answer and freezes the final metadata.
	if _, err = app.Commands.ResolveConversation.Handle(t.Context(), command.ResolveConversationCommand{
		ConversationID: convID,
		CloserMemberID: askerRes.MemberID,
		Resolution:     answer,
		Summary:        "the orako server is llm-free by design",
		Tags:           []string{"architecture", "llm-free"},
	}); err != nil {
		t.Fatalf("ResolveConversation: %v", err)
	}

	// Verify the conversation is now resolved.
	closedView, err := app.Queries.GetConversation.Handle(t.Context(), query.GetConversationQuery{
		ConversationID: convID,
		CallerMemberID: askerRes.MemberID,
	})
	if err != nil {
		t.Fatalf("GetConversation (post-close): %v", err)
	}

	if closedView.Status != model.ConversationStatusResolved {
		t.Errorf("Status (post-close): got %v, want Resolved", closedView.Status)
	}

	// 7. SearchHistory — the resolved conversation must be findable by the
	//    embedding-free engine (FTS over its summary/tags/title/messages).
	hits, err := app.Queries.SearchHistory.Handle(t.Context(), query.SearchHistoryQuery{
		ProjectIDs: []uuid.UUID{projectID},
		Query:      "llm-free design",
	})
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}

	var found bool

	for _, h := range hits {
		if h.ConversationID == convID {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("SearchHistory: conversation %v not found in %d hits", convID, len(hits))
	}
}

// TestExpertiseTagsEndToEnd proves, against a real Postgres database, that:
//   - AddMember lands the expertise tags in project_members.domains and
//     ListExperts surfaces every project member with those domains (no role
//     filter — the Part 1 side effect that returned empty is fixed);
//   - AssignRole SETs (replaces) a member's tags via UPDATE, is idempotent, and
//     no longer fails with ErrDuplicate for an already-enrolled member (the
//     second Part 1 side effect).
func TestExpertiseTagsEndToEnd(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t) // skips without DB

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	reg, regErr := provider.New(nil, provider.Deps{}, nil)
	if regErr != nil {
		t.Fatalf("provider.New: %v", regErr)
	}

	app, err := application.New(pool, reg, mail.NewNoop(log), nil, blobstore.Noop{}, nil, "http://localhost:8080", edition.NewLive(edition.Edition{}), nil, nil, log)
	if err != nil {
		t.Fatalf("application.New: %v", err)
	}

	defer func() {
		if cerr := app.Close(); cerr != nil {
			t.Logf("app.Close: %v", cerr)
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	routerErr := make(chan error, 1)
	go func() { routerErr <- app.RunEvents(ctx) }()

	select {
	case <-app.Running():
	case rerr := <-routerErr:
		t.Fatalf("event router failed to start: %v", rerr)
	}

	projectID := uuid.New()

	proj, err := model.NewProject(projectID, "expertise-tags-project")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	if err := identity.NewProjectStore(pool).Create(t.Context(), proj); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// AddMember with expertise tags.
	memberRes, err := app.Commands.AddMember.Handle(t.Context(), command.AddMemberCommand{
		ProjectID:   projectID,
		Email:       "tagged@e2e.test",
		DisplayName: "Tagged Member",
		Domains:     []string{"Backend", "CTO"},
	})
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	// ListExperts must surface the member WITH their domains — every member
	// is routable now (no role filter).
	specs, err := app.Queries.ListExperts.Handle(t.Context(), query.ListExpertsQuery{
		ProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("ListExperts: %v", err)
	}

	var found *query.Expert

	for i := range specs {
		if specs[i].MemberID == memberRes.MemberID {
			found = &specs[i]
			break
		}
	}

	if found == nil {
		t.Fatalf("ListExperts: member %v not returned (got %d experts)", memberRes.MemberID, len(specs))
	}

	if len(found.Domains) != 2 || found.Domains[0] != "Backend" || found.Domains[1] != "CTO" {
		t.Errorf("ListExperts Domains = %v, want [Backend CTO]", found.Domains)
	}

	// AssignRole SETs the member's tags via UPDATE (no ErrDuplicate for an
	// enrolled member — the Part 1 regression).
	if err = app.Commands.AssignRole.Handle(t.Context(), command.AssignRoleCommand{
		ProjectID: projectID,
		MemberID:  memberRes.MemberID,
		Domains:   []string{"Frontend", "QA"},
	}); err != nil {
		t.Fatalf("AssignRole (set tags): %v", err)
	}

	specs, err = app.Queries.ListExperts.Handle(t.Context(), query.ListExpertsQuery{
		ProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("ListExperts (post-set): %v", err)
	}

	found = nil

	for i := range specs {
		if specs[i].MemberID == memberRes.MemberID {
			found = &specs[i]
			break
		}
	}

	if found == nil {
		t.Fatalf("ListExperts (post-set): member %v not returned", memberRes.MemberID)
	}

	// Tags REPLACED, not appended.
	if len(found.Domains) != 2 || found.Domains[0] != "Frontend" || found.Domains[1] != "QA" {
		t.Errorf("post-set Domains = %v, want [Frontend QA] (replace, not append)", found.Domains)
	}

	// Idempotent: setting the same tags again succeeds (no unique-violation).
	if err = app.Commands.AssignRole.Handle(t.Context(), command.AssignRoleCommand{
		ProjectID: projectID,
		MemberID:  memberRes.MemberID,
		Domains:   []string{"Frontend", "QA"},
	}); err != nil {
		t.Fatalf("AssignRole (idempotent re-set): %v", err)
	}
}
