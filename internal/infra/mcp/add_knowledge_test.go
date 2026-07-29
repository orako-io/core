// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"testing"

	"github.com/google/uuid"
)

// TestAddKnowledgeAdminGate proves the org-admin gate: a plain member is
// rejected (and the app layer is never called), while an org admin succeeds and
// the org + author are taken from the caller identity, not the payload.
func TestAddKnowledgeAdminGate(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)

	// Non-admin member: rejected, app layer untouched.
	memberCS := connectMCP(t, tf, tf.memberToken)

	rejected := callTool(t, memberCS, toolAddKnowledge, map[string]any{
		"question": "How do I test Orako?",
		"answer":   "Connect an agent and ask.",
	}, nil)

	if !rejected.IsError {
		t.Fatal("add_knowledge by a non-admin member should be rejected, got success")
	}

	if tf.deps.addKnowledge.calls != 0 {
		t.Errorf("non-admin call reached the app layer (%d calls); the gate must block it first", tf.deps.addKnowledge.calls)
	}

	// Org admin: succeeds.
	adminCS := connectMCP(t, tf, tf.adminToken)

	var out AddKnowledgeOutput

	res := callTool(t, adminCS, toolAddKnowledge, map[string]any{
		"question": "How do I test Orako?",
		"answer":   "Connect an agent and ask.",
		"tags":     []string{"testing"},
	}, &out)

	if res.IsError {
		t.Fatalf("add_knowledge by an org admin should succeed: %+v", res)
	}

	if out.Count != 1 || len(out.EntryIDs) != 1 {
		t.Errorf("expected one created entry, got count=%d ids=%v", out.Count, out.EntryIDs)
	}

	got := tf.deps.addKnowledge.got
	if got.Question != "How do I test Orako?" || got.Answer != "Connect an agent and ask." {
		t.Errorf("app layer did not receive the payload content: %+v", got)
	}

	// Org + author come from the caller identity, never the payload (which has no
	// such fields).
	if got.OrgID == uuid.Nil {
		t.Error("OrgID must be populated from the caller identity")
	}

	if got.AuthorMemberID == uuid.Nil {
		t.Error("AuthorMemberID must be populated from the caller identity")
	}
}

// TestSuggestKnowledgeAnyMember proves suggest_knowledge is NOT admin-gated: a
// plain project member's agent may propose knowledge, the org + author are taken
// from the caller identity (never the payload), and the result reports the
// pending status.
func TestSuggestKnowledgeAnyMember(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)

	// A non-admin member succeeds (contrast add_knowledge, which rejects them).
	memberCS := connectMCP(t, tf, tf.memberToken)

	var out SuggestKnowledgeOutput

	res := callTool(t, memberCS, toolSuggestKnowledge, map[string]any{
		"question": "Why does the pgx pool exhaust in dev?",
		"answer":   "The reflex hot-reload leaks connections; set a max lifetime.",
		"tags":     []string{"pgx", "dev"},
	}, &out)

	if res.IsError {
		t.Fatalf("suggest_knowledge by a plain member should succeed: %+v", res)
	}

	if tf.deps.suggestKnowledge.calls != 1 {
		t.Errorf("expected exactly one app-layer call, got %d", tf.deps.suggestKnowledge.calls)
	}

	if out.EntryID == "" || out.Status != "pending" {
		t.Errorf("expected a pending entry id, got id=%q status=%q", out.EntryID, out.Status)
	}

	got := tf.deps.suggestKnowledge.got
	if got.Question == "" || got.Answer == "" {
		t.Errorf("app layer did not receive the payload content: %+v", got)
	}

	if got.OrgID == uuid.Nil {
		t.Error("OrgID must be populated from the caller identity")
	}

	if got.AuthorMemberID == uuid.Nil {
		t.Error("AuthorMemberID must be populated from the caller identity")
	}
}

// TestAddKnowledgeBatch proves the batch form: entries[] creates one entry per
// item and reports the count.
func TestAddKnowledgeBatch(t *testing.T) {
	t.Parallel()

	tf := newTestFixture(t)
	adminCS := connectMCP(t, tf, tf.adminToken)

	var out AddKnowledgeOutput

	res := callTool(t, adminCS, toolAddKnowledge, map[string]any{
		"entries": []map[string]any{
			{"question": "q1", "answer": "a1"},
			{"question": "q2", "answer": "a2"},
		},
	}, &out)

	if res.IsError {
		t.Fatalf("batch add_knowledge should succeed: %+v", res)
	}

	if out.Count != 2 || tf.deps.addKnowledge.calls != 2 {
		t.Errorf("batch of 2 should create 2 entries: count=%d calls=%d", out.Count, tf.deps.addKnowledge.calls)
	}
}
