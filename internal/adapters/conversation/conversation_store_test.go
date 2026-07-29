// SPDX-License-Identifier: AGPL-3.0-or-later

package conversation_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/conversation"
	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/testsupport"
)

// TestConversationStoreDeleteConversation proves the hard delete: the
// conversation row is gone and its messages / provider_messages fall via ON
// DELETE CASCADE. An absent id is ErrNotFound.
func TestConversationStoreDeleteConversation(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewStore(pool)

	projectID := testsupport.SeedProject(t, pool)
	memberID := testsupport.SeedMember(t, pool)
	convID := uuid.New()

	conv, err := model.NewConversation(convID, projectID, memberID, "delete me?")
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO messages (id, conversation_id, author_member_id, role, body)
		 VALUES ($1, $2, $3, 'question', 'delete me?')`,
		uuid.New(), convID, memberID,
	); err != nil {
		t.Fatalf("seeding message: %v", err)
	}

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO provider_messages (id, conversation_id, member_id, provider_kind, channel_id, message_ref, state)
		 VALUES ($1, $2, $3, 'discord', 'chan', 'ref', 'posted')`,
		uuid.New(), convID, memberID,
	); err != nil {
		t.Fatalf("seeding provider_message: %v", err)
	}

	if err := store.DeleteConversation(t.Context(), convID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}

	if _, err := store.ConversationByID(t.Context(), convID); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("ConversationByID after delete: got %v, want ErrNotFound", err)
	}

	for table, col := range map[string]string{
		"messages":          "conversation_id",
		"provider_messages": "conversation_id",
	} {
		var count int
		if err := pool.QueryRow(t.Context(),
			"SELECT count(*) FROM "+table+" WHERE "+col+" = $1", convID,
		).Scan(&count); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}

		if count != 0 {
			t.Errorf("%s: %d rows survived conversation delete, want 0", table, count)
		}
	}

	if err := store.DeleteConversation(t.Context(), uuid.New()); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("DeleteConversation(absent): got %v, want ErrNotFound", err)
	}
}

// TestConversationStoreCreateAndByID proves the create-then-fetch round trip
// including the optional responder assignment.
func TestConversationStoreCreateAndByID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		withSpecialist bool
	}{
		{name: "minimal", withSpecialist: false},
		{name: "with_specialist", withSpecialist: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pool := testsupport.RequirePostgres(t)
			projectID := testsupport.SeedProject(t, pool)
			askerID := testsupport.SeedMember(t, pool)
			store := conversation.NewStore(pool)

			conv, err := model.NewConversation(uuid.New(), projectID, askerID, "What is X?")
			if err != nil {
				t.Fatalf("NewConversation: %v", err)
			}

			if tc.withSpecialist {
				conv.ResponderMemberID = testsupport.SeedMember(t, pool)
			}

			if err := store.CreateConversation(t.Context(), conv); err != nil {
				t.Fatalf("CreateConversation: %v", err)
			}

			got, err := store.ConversationByID(t.Context(), conv.ID)
			if err != nil {
				t.Fatalf("ConversationByID: %v", err)
			}

			if got.ID != conv.ID {
				t.Errorf("ID: got %v, want %v", got.ID, conv.ID)
			}

			if got.Question != "What is X?" {
				t.Errorf("Question: got %q", got.Question)
			}

			if got.Status != model.ConversationStatusOpen {
				t.Errorf("Status: got %q, want open", got.Status)
			}

			if tc.withSpecialist && got.ResponderMemberID != conv.ResponderMemberID {
				t.Errorf("ResponderMemberID: got %v, want %v", got.ResponderMemberID, conv.ResponderMemberID)
			}
		})
	}
}

// TestConversationStoreBySlackThread proves the Slack inbound correlation now
// resolves through the conversation_surfaces row that SetSlackThread writes:
// (channel, thread_ts) → surface → conversation, with a miss → ErrNotFound.
// UUID-derived channel/thread values isolate the test from pre-existing rows.
func TestConversationStoreBySlackThread(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	projectID := testsupport.SeedProject(t, pool)
	askerID := testsupport.SeedMember(t, pool)
	store := conversation.NewStore(pool)

	// Unique per run: prevents collision with rows created by earlier runs.
	runID := uuid.NewString()
	channelID := "C-" + runID[:8]
	threadTS := runID[9:22] + ".000001"

	conv, _ := model.NewConversation(uuid.New(), projectID, askerID, "Slack question?")

	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	// Outbound Deliver records the correlation surface.
	if err := store.SetSlackThread(t.Context(), conv.ID, channelID, threadTS); err != nil {
		t.Fatalf("SetSlackThread: %v", err)
	}

	got, err := store.ConversationBySlackThread(t.Context(), channelID, threadTS)
	if err != nil {
		t.Fatalf("ConversationBySlackThread: %v", err)
	}

	if got.ID != conv.ID {
		t.Fatalf("ID: got %v, want %v", got.ID, conv.ID)
	}

	// thread_ts is unique only within a channel: the SAME thread_ts in a
	// DIFFERENT channel must NOT resolve this conversation.
	if _, err := store.ConversationBySlackThread(t.Context(), "C-other-"+runID[:8], threadTS); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("cross-channel thread_ts: got %v, want ErrNotFound", err)
	}

	// Absent thread: a different unique ID guarantees no match.
	absentTS := uuid.NewString()[:13] + ".000000"
	if _, err := store.ConversationBySlackThread(t.Context(), channelID, absentTS); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("absent thread: got %v, want ErrNotFound", err)
	}

	// Re-delivery overwrites the binding (last writer wins), matching the old
	// flat-column UPDATE: the new thread_ts resolves, the old no longer does.
	newTS := runID[9:22] + ".000002"
	if err := store.SetSlackThread(t.Context(), conv.ID, channelID, newTS); err != nil {
		t.Fatalf("SetSlackThread(re-deliver): %v", err)
	}

	if got, err := store.ConversationBySlackThread(t.Context(), channelID, newTS); err != nil || got.ID != conv.ID {
		t.Fatalf("after re-deliver, new thread_ts: got (%v, %v), want conv %v", got.ID, err, conv.ID)
	}

	if _, err := store.ConversationBySlackThread(t.Context(), channelID, threadTS); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("after re-deliver, old thread_ts: got %v, want ErrNotFound", err)
	}
}

// TestConversationStoreByTelegramMessage proves Telegram inbound correlation
// resolves through the surface SetTelegramThread writes: (chat, message_id) →
// surface → conversation. Critically it proves the SAME message_id in a
// DIFFERENT chat resolves the RIGHT conversation — message_id is unique only
// per chat, which the flat-column (chat, message) lookup relied on and the
// (provider, channel_id, thread_id) surface key preserves.
func TestConversationStoreByTelegramMessage(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	projectID := testsupport.SeedProject(t, pool)
	askerID := testsupport.SeedMember(t, pool)
	store := conversation.NewStore(pool)

	runID := uuid.NewString()
	chatA := "chatA-" + runID[:8]
	chatB := "chatB-" + runID[:8]
	// Same message_id reused across the two chats — the realistic Telegram case
	// (per-chat monotonic message ids collide across chats).
	messageID := "47"

	convA, _ := model.NewConversation(uuid.New(), projectID, askerID, "in chat A?")
	convB, _ := model.NewConversation(uuid.New(), projectID, askerID, "in chat B?")

	for _, c := range []model.Conversation{convA, convB} {
		if err := store.CreateConversation(t.Context(), c); err != nil {
			t.Fatalf("CreateConversation: %v", err)
		}
	}

	if err := store.SetTelegramThread(t.Context(), convA.ID, chatA, messageID); err != nil {
		t.Fatalf("SetTelegramThread(A): %v", err)
	}

	if err := store.SetTelegramThread(t.Context(), convB.ID, chatB, messageID); err != nil {
		t.Fatalf("SetTelegramThread(B): %v", err)
	}

	gotA, err := store.ConversationByTelegramMessage(t.Context(), chatA, messageID)
	if err != nil || gotA.ID != convA.ID {
		t.Fatalf("chatA message %s: got (%v, %v), want conv %v", messageID, gotA.ID, err, convA.ID)
	}

	gotB, err := store.ConversationByTelegramMessage(t.Context(), chatB, messageID)
	if err != nil || gotB.ID != convB.ID {
		t.Fatalf("chatB message %s: got (%v, %v), want conv %v", messageID, gotB.ID, err, convB.ID)
	}

	// A miss on a message_id never delivered → ErrNotFound.
	if _, err := store.ConversationByTelegramMessage(t.Context(), chatA, "99999"); !errors.Is(err, adaptererr.ErrNotFound) {
		t.Fatalf("absent message: got %v, want ErrNotFound", err)
	}
}

// TestConversationStoreFlatColumnsDropped proves migration 0054 dropped the four
// flat correlation columns from conversations — correlation now lives solely on
// conversation_surfaces.
func TestConversationStoreFlatColumnsDropped(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)

	var present int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'conversations'
		   AND column_name IN ('slack_channel_id', 'slack_thread_ts', 'telegram_chat_id', 'telegram_message_id')`,
	).Scan(&present); err != nil {
		t.Fatalf("counting flat columns: %v", err)
	}

	if present != 0 {
		t.Errorf("conversations still has %d flat correlation column(s); migration 0054 did not drop them", present)
	}
}

// TestConversationStoreUpdateStatus proves status transitions are persisted.
func TestConversationStoreUpdateStatus(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	projectID := testsupport.SeedProject(t, pool)
	askerID := testsupport.SeedMember(t, pool)
	store := conversation.NewStore(pool)

	conv, _ := model.NewConversation(uuid.New(), projectID, askerID, "Status test")

	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	transitions := []model.ConversationStatus{
		model.ConversationStatusAnswered,
		model.ConversationStatusResolved,
	}

	for _, status := range transitions {
		if err := store.UpdateStatus(t.Context(), conv.ID, status); err != nil {
			t.Fatalf("UpdateStatus(%q): %v", status, err)
		}

		got, err := store.ConversationByID(t.Context(), conv.ID)
		if err != nil {
			t.Fatalf("ConversationByID after %q: %v", status, err)
		}

		if got.Status != status {
			t.Fatalf("Status: got %q, want %q", got.Status, status)
		}
	}
}

// TestConversationStoreMessages proves messages are appended and retrieved in
// chronological order.
func TestConversationStoreMessages(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	projectID := testsupport.SeedProject(t, pool)
	askerID := testsupport.SeedMember(t, pool)
	targetID := testsupport.SeedMember(t, pool)
	store := conversation.NewStore(pool)

	conv, _ := model.NewConversation(uuid.New(), projectID, askerID, "What is Y?")

	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	msgs := []struct {
		authorID uuid.UUID
		role     model.MessageRole
		body     string
	}{
		{askerID, model.MessageRoleQuestion, "What is Y?"},
		{targetID, model.MessageRoleAnswer, "Y is Z."},
		{askerID, model.MessageRoleFollowUp, "Thanks!"},
		// second_opinion (phase 2): a losing pool candidate's captured reply
		// round-trips like any other role — no DB CHECK constraint gates it
		// (messages.role is plain TEXT), so the sole gate is
		// model.MessageRoleSecondOpinion.String()/messageRoleFromString.
		{targetID, model.MessageRoleSecondOpinion, "I'd have used a partial index too."},
	}

	for _, m := range msgs {
		msg, err := model.NewMessage(uuid.New(), conv.ID, m.authorID, m.role, m.body, model.MessageSourceHuman)
		if err != nil {
			t.Fatalf("NewMessage: %v", err)
		}

		if err := store.AddMessage(t.Context(), msg); err != nil {
			t.Fatalf("AddMessage(%q): %v", m.body, err)
		}
	}

	list, err := store.MessagesByConversation(t.Context(), conv.ID)
	if err != nil {
		t.Fatalf("MessagesByConversation: %v", err)
	}

	if len(list) != len(msgs) {
		t.Fatalf("got %d messages, want %d", len(list), len(msgs))
	}

	for i, got := range list {
		if got.Body != msgs[i].body {
			t.Errorf("message[%d] body: got %q, want %q", i, got.Body, msgs[i].body)
		}

		if got.Role != msgs[i].role {
			t.Errorf("message[%d] role: got %v, want %v", i, got.Role, msgs[i].role)
		}
	}
}

// TestConversationStoreDuplicate proves creating a conversation with the same ID
// returns ErrDuplicate.
func TestConversationStoreDuplicate(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	projectID := testsupport.SeedProject(t, pool)
	askerID := testsupport.SeedMember(t, pool)
	store := conversation.NewStore(pool)

	conv, _ := model.NewConversation(uuid.New(), projectID, askerID, "Dup?")

	if err := store.CreateConversation(t.Context(), conv); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	err := store.CreateConversation(t.Context(), conv)
	if !errors.Is(err, adaptererr.ErrDuplicate) {
		t.Fatalf("second Create: got %v, want ErrDuplicate", err)
	}
}

// TestConversationStoreConversationsByProjectIDs_MultiProject proves the
// multi-project scope: an explicit project set returns rows from exactly
// those projects with resolved names, an empty set falls back to every
// non-archived project in the org (the org-wide read), and an archived
// project is excluded even when explicitly named.
func TestConversationStoreConversationsByProjectIDs_MultiProject(t *testing.T) {
	t.Parallel()

	pool := testsupport.RequirePostgres(t)
	store := conversation.NewStore(pool)
	projectStore := identity.NewProjectStore(pool)

	orgID := testsupport.SeedOrganization(t, pool)
	projectA := testsupport.SeedProjectInOrg(t, pool, orgID)
	projectB := testsupport.SeedProjectInOrg(t, pool, orgID)
	projectArchived := testsupport.SeedProjectInOrg(t, pool, orgID)

	if _, err := pool.Exec(t.Context(), "UPDATE projects SET name = $2 WHERE id = $1", projectA, "Project A"); err != nil {
		t.Fatalf("naming projectA: %v", err)
	}

	if _, err := pool.Exec(t.Context(), "UPDATE projects SET name = $2 WHERE id = $1", projectB, "Project B"); err != nil {
		t.Fatalf("naming projectB: %v", err)
	}

	askerA := testsupport.SeedMember(t, pool)
	askerB := testsupport.SeedMember(t, pool)
	askerArchived := testsupport.SeedMember(t, pool)

	convA, _ := model.NewConversation(uuid.New(), projectA, askerA, "in A?")
	convB, _ := model.NewConversation(uuid.New(), projectB, askerB, "in B?")
	convArchived, _ := model.NewConversation(uuid.New(), projectArchived, askerArchived, "in archived?")

	for _, c := range []model.Conversation{convA, convB, convArchived} {
		if err := store.CreateConversation(t.Context(), c); err != nil {
			t.Fatalf("CreateConversation: %v", err)
		}
	}

	if err := projectStore.SetArchived(t.Context(), projectArchived, true); err != nil {
		t.Fatalf("SetArchived: %v", err)
	}

	// Explicit set: A and B only.
	rows, err := store.ConversationsByProjectIDs(t.Context(), orgID, []uuid.UUID{projectA, projectB}, "", uuid.Nil, true)
	if err != nil {
		t.Fatalf("ConversationsByProjectIDs(explicit): %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("explicit set: got %d rows, want 2", len(rows))
	}

	names := map[uuid.UUID]string{}
	for _, r := range rows {
		names[r.ProjectID] = r.ProjectName
	}

	if names[projectA] != "Project A" || names[projectB] != "Project B" {
		t.Errorf("resolved project names: got %+v, want Project A / Project B", names)
	}

	// Explicit set naming the archived project: still excluded.
	rows, err = store.ConversationsByProjectIDs(t.Context(), orgID, []uuid.UUID{projectA, projectArchived}, "", uuid.Nil, true)
	if err != nil {
		t.Fatalf("ConversationsByProjectIDs(explicit incl. archived): %v", err)
	}

	if len(rows) != 1 || rows[0].ProjectID != projectA {
		t.Fatalf("archived project must be excluded even when named explicitly: got %+v", rows)
	}

	// Empty set: org-wide, archived still excluded.
	rows, err = store.ConversationsByProjectIDs(t.Context(), orgID, nil, "", uuid.Nil, true)
	if err != nil {
		t.Fatalf("ConversationsByProjectIDs(org-wide): %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("org-wide: got %d rows, want 2 (archived excluded)", len(rows))
	}

	for _, r := range rows {
		if r.ProjectID == projectArchived {
			t.Error("org-wide read must exclude the archived project")
		}
	}

	// Member-scoped (includeAll=false): the asker of A sees only their own.
	rows, err = store.ConversationsByProjectIDs(t.Context(), orgID, nil, "", askerA, false)
	if err != nil {
		t.Fatalf("ConversationsByProjectIDs(member-scoped): %v", err)
	}

	if len(rows) != 1 || rows[0].ID != convA.ID {
		t.Fatalf("member-scoped: got %+v, want only convA", rows)
	}

	// Reactivating restores the project to the org-wide read.
	if err := projectStore.SetArchived(t.Context(), projectArchived, false); err != nil {
		t.Fatalf("SetArchived(reactivate): %v", err)
	}

	rows, err = store.ConversationsByProjectIDs(t.Context(), orgID, nil, "", uuid.Nil, true)
	if err != nil {
		t.Fatalf("ConversationsByProjectIDs(org-wide after reactivate): %v", err)
	}

	if len(rows) != 3 {
		t.Fatalf("org-wide after reactivate: got %d rows, want 3", len(rows))
	}
}
