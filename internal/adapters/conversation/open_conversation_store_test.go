// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/conversation"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestOpenConversationStore_MessageSourceRoundTrip proves the atomic
// open-conversation path persists the opening question's source and
// agent_client. Guards the live bug where OpenConversation's addMessage call
// omitted both columns, so every agent-asked question read back as
// source="human" (the read-side guard normalizes the empty string) and the
// dashboard lost the "Val · Claude Code" attribution on questions.
func TestOpenConversationStore_MessageSourceRoundTrip(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	openStore := conversation.NewOpenConversationStore(pool)
	convStore := conversation.NewStore(pool)

	projectID := testsupport.SeedProject(t, pool)
	memberID := testsupport.SeedMember(t, pool)
	convID := uuid.New()

	conv, err := model.NewConversation(convID, projectID, memberID, "agent-labeled question?")
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	msg, err := model.NewMessage(uuid.New(), convID, memberID, model.MessageRoleQuestion,
		"agent-labeled question?", model.MessageSourceAgent)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

	msg.AgentClient = "claude-code"

	if err := openStore.OpenConversation(t.Context(), conv, msg, nil); err != nil {
		t.Fatalf("OpenConversation: %v", err)
	}

	msgs, err := convStore.MessagesByConversation(t.Context(), convID)
	if err != nil {
		t.Fatalf("MessagesByConversation: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}

	if msgs[0].Source != model.MessageSourceAgent {
		t.Errorf("Source = %q, want %q (the atomic path must persist it)", msgs[0].Source, model.MessageSourceAgent)
	}

	if msgs[0].AgentClient != "claude-code" {
		t.Errorf("AgentClient = %q, want %q", msgs[0].AgentClient, "claude-code")
	}
}
