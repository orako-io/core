// SPDX-License-Identifier: AGPL-3.0-or-later

package model_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

var (
	anyID        = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	anyProjectID = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	anyMemberID  = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	anyConvID    = uuid.MustParse("44444444-4444-4444-4444-444444444444")
	anyTime      = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
)

// TestNewEvent validates the Event constructor's invariant enforcement.
func TestNewEvent(t *testing.T) {
	t.Parallel()

	validPayload := []byte(`{"conversation_id":"abc"}`)

	cases := []struct {
		name       string
		id         uuid.UUID
		projectID  uuid.UUID
		eventType  model.EventType
		payload    []byte
		occurredAt time.Time
		wantErr    bool
		wantField  string
	}{
		{
			name:       "valid",
			id:         anyID,
			projectID:  anyProjectID,
			eventType:  model.EventTypeConversationOpened,
			payload:    validPayload,
			occurredAt: anyTime,
		},
		{
			name:       "nil id",
			id:         uuid.Nil,
			projectID:  anyProjectID,
			eventType:  model.EventTypeMessagePosted,
			payload:    validPayload,
			occurredAt: anyTime,
			wantErr:    true,
			wantField:  "id",
		},
		{
			name:       "nil project_id",
			id:         anyID,
			projectID:  uuid.Nil,
			eventType:  model.EventTypeMessagePosted,
			payload:    validPayload,
			occurredAt: anyTime,
			wantErr:    true,
			wantField:  "project_id",
		},
		{
			name:       "unspecified event type",
			id:         anyID,
			projectID:  anyProjectID,
			eventType:  model.EventTypeUnspecified,
			payload:    validPayload,
			occurredAt: anyTime,
			wantErr:    true,
			wantField:  "type",
		},
		{
			name:       "empty payload",
			id:         anyID,
			projectID:  anyProjectID,
			eventType:  model.EventTypePresenceChanged,
			payload:    nil,
			occurredAt: anyTime,
			wantErr:    true,
			wantField:  "payload",
		},
		{
			name:       "zero occurred_at",
			id:         anyID,
			projectID:  anyProjectID,
			eventType:  model.EventTypePresenceChanged,
			payload:    validPayload,
			occurredAt: time.Time{},
			wantErr:    true,
			wantField:  "occurred_at",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ev, err := model.NewEvent(tc.id, tc.projectID, tc.eventType, tc.payload, tc.occurredAt)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewEvent: want error for field %q, got nil", tc.wantField)
				}

				var inv errs.InvalidError
				if ok := asInvalidError(err, &inv); !ok {
					t.Fatalf("NewEvent: want InvalidError, got %T: %v", err, err)
				}

				if inv.Field != tc.wantField {
					t.Errorf("InvalidError.Field = %q, want %q", inv.Field, tc.wantField)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewEvent: unexpected error: %v", err)
			}

			if ev.Seq != 0 {
				t.Errorf("NewEvent: Seq = %d, want 0 (store assigns)", ev.Seq)
			}

			if ev.ProjectID != tc.projectID {
				t.Errorf("NewEvent: ProjectID = %v, want %v", ev.ProjectID, tc.projectID)
			}
		})
	}
}

// TestEventTypeValid covers Valid() for every named constant and the zero value.
func TestEventTypeValid(t *testing.T) {
	t.Parallel()

	valid := []model.EventType{
		model.EventTypeConversationOpened,
		model.EventTypeMessagePosted,
		model.EventTypeConversationClosed,
		model.EventTypeKBEntryCreated,
		model.EventTypeKBAnswerSuperseded,
		model.EventTypeKBFeedback,
		model.EventTypeMemberLifecycle,
		model.EventTypePresenceChanged,
	}

	for _, et := range valid {
		if !et.Valid() {
			t.Errorf("EventType(%d).Valid() = false, want true", et)
		}
	}

	if model.EventTypeUnspecified.Valid() {
		t.Error("EventTypeUnspecified.Valid() = true, want false")
	}
}

// TestNewMember validates Member constructor invariants.
func TestNewMember(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		id          uuid.UUID
		email       string
		displayName string
		wantErr     bool
		wantField   string
	}{
		{
			name:        "valid",
			id:          anyID,
			email:       "alice@example.com",
			displayName: "Alice",
		},
		{
			name:        "nil id",
			id:          uuid.Nil,
			email:       "alice@example.com",
			displayName: "Alice",
			wantErr:     true,
			wantField:   "id",
		},
		{
			name:        "blank display_name",
			id:          anyID,
			email:       "alice@example.com",
			displayName: "   ",
			wantErr:     true,
			wantField:   "display_name",
		},
		{
			name:        "empty email allowed",
			id:          anyID,
			email:       "",
			displayName: "Purged",
		},
		{
			name:        "comma instead of dot rejected",
			id:          anyID,
			email:       "kawaii.house.pro@gmail,com", // the 2026-07 invite-loop typo
			displayName: "Typo",
			wantErr:     true,
			wantField:   "email",
		},
		{
			name:        "missing at rejected",
			id:          anyID,
			email:       "no-at-sign.example.com",
			displayName: "NoAt",
			wantErr:     true,
			wantField:   "email",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m, err := model.NewMember(tc.id, tc.email, tc.displayName)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewMember: want error on field %q, got nil", tc.wantField)
				}

				var inv errs.InvalidError
				if !asInvalidError(err, &inv) {
					t.Fatalf("want InvalidError, got %T: %v", err, err)
				}

				if inv.Field != tc.wantField {
					t.Errorf("InvalidError.Field = %q, want %q", inv.Field, tc.wantField)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewMember: unexpected error: %v", err)
			}

			if m.Status != model.MemberStatusInvited {
				t.Errorf("NewMember: Status = %q, want %q", m.Status, model.MemberStatusInvited)
			}
		})
	}
}

// TestNewPresence validates Presence constructor invariants.
func TestNewPresence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		memberID  uuid.UUID
		online    bool
		updatedAt time.Time
		wantErr   bool
		wantField string
	}{
		{name: "online", memberID: anyMemberID, online: true, updatedAt: anyTime},
		{name: "offline", memberID: anyMemberID, online: false, updatedAt: anyTime},
		{
			name:      "nil member_id",
			memberID:  uuid.Nil,
			online:    true,
			updatedAt: anyTime,
			wantErr:   true,
			wantField: "member_id",
		},
		{
			name:      "zero updated_at",
			memberID:  anyMemberID,
			online:    true,
			updatedAt: time.Time{},
			wantErr:   true,
			wantField: "updated_at",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := model.NewPresence(tc.memberID, tc.online, tc.updatedAt)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewPresence: want error on field %q, got nil", tc.wantField)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewPresence: unexpected error: %v", err)
			}

			if p.Online != tc.online {
				t.Errorf("NewPresence: Online = %v, want %v", p.Online, tc.online)
			}
		})
	}
}

// TestNewProjectInOrg validates Project constructor invariants.
func TestNewProjectInOrg(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		id        uuid.UUID
		projName  string
		orgID     uuid.UUID
		wantErr   bool
		wantField string
	}{
		{name: "valid", id: anyID, projName: "orako-core", orgID: anyID},
		{name: "nil id", id: uuid.Nil, projName: "orako-core", orgID: anyID, wantErr: true, wantField: "id"},
		{name: "blank name", id: anyID, projName: "  ", orgID: anyID, wantErr: true, wantField: "name"},
		{name: "nil org", id: anyID, projName: "orako-core", orgID: uuid.Nil, wantErr: true, wantField: "org_id"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			proj, err := model.NewProjectInOrg(tc.id, tc.projName, tc.orgID)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewProjectInOrg: want error on field %q, got nil", tc.wantField)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewProjectInOrg: unexpected error: %v", err)
			}

			if proj.Name != tc.projName {
				t.Errorf("NewProjectInOrg: Name = %q, want %q", proj.Name, tc.projName)
			}
		})
	}
}

// TestNewConversation validates Conversation constructor invariants.
func TestNewConversation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		id            uuid.UUID
		projectID     uuid.UUID
		askerMemberID uuid.UUID
		question      string
		wantErr       bool
		wantField     string
	}{
		{
			name:          "valid",
			id:            anyID,
			projectID:     anyProjectID,
			askerMemberID: anyMemberID,
			question:      "What does orako do?",
		},
		{name: "nil id", id: uuid.Nil, projectID: anyProjectID, askerMemberID: anyMemberID, question: "q", wantErr: true, wantField: "id"},
		{name: "nil project_id", id: anyID, projectID: uuid.Nil, askerMemberID: anyMemberID, question: "q", wantErr: true, wantField: "project_id"},
		{name: "nil asker_member_id", id: anyID, projectID: anyProjectID, askerMemberID: uuid.Nil, question: "q", wantErr: true, wantField: "asker_member_id"},
		{name: "blank question", id: anyID, projectID: anyProjectID, askerMemberID: anyMemberID, question: "  ", wantErr: true, wantField: "question"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			conv, err := model.NewConversation(tc.id, tc.projectID, tc.askerMemberID, tc.question)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewConversation: want error on field %q, got nil", tc.wantField)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewConversation: unexpected error: %v", err)
			}

			if conv.Status != model.ConversationStatusOpen {
				t.Errorf("NewConversation: Status = %q, want %q", conv.Status, model.ConversationStatusOpen)
			}
		})
	}
}

// TestNewMessage validates Message constructor invariants.
func TestNewMessage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		id             uuid.UUID
		conversationID uuid.UUID
		authorMemberID uuid.UUID
		role           model.MessageRole
		body           string
		source         model.MessageSource
		wantSource     model.MessageSource
		wantErr        bool
		wantField      string
	}{
		{
			name:           "answer with author",
			id:             anyID,
			conversationID: anyConvID,
			authorMemberID: anyMemberID,
			role:           model.MessageRoleAnswer,
			body:           "Use NewEvent to build events.",
			source:         model.MessageSourceHuman,
			wantSource:     model.MessageSourceHuman,
		},
		{
			name:           "agent-authored follow-up",
			id:             anyID,
			conversationID: anyConvID,
			authorMemberID: anyMemberID,
			role:           model.MessageRoleFollowUp,
			body:           "Agent posted this.",
			source:         model.MessageSourceAgent,
			wantSource:     model.MessageSourceAgent,
		},
		{
			name:           "empty source defaults to human",
			id:             anyID,
			conversationID: anyConvID,
			authorMemberID: anyMemberID,
			role:           model.MessageRoleAnswer,
			body:           "No source given.",
			source:         "",
			wantSource:     model.MessageSourceHuman,
		},
		{
			name:           "system message without author",
			id:             anyID,
			conversationID: anyConvID,
			authorMemberID: uuid.Nil,
			role:           model.MessageRoleSystem,
			body:           "Routing to responder.",
			source:         model.MessageSourceSystem,
			wantSource:     model.MessageSourceSystem,
		},
		{
			name:           "non-system without author",
			id:             anyID,
			conversationID: anyConvID,
			authorMemberID: uuid.Nil,
			role:           model.MessageRoleQuestion,
			body:           "What does X do?",
			wantErr:        true,
			wantField:      "author_member_id",
		},
		{
			name:           "blank body",
			id:             anyID,
			conversationID: anyConvID,
			authorMemberID: anyMemberID,
			role:           model.MessageRoleAnswer,
			body:           " ",
			wantErr:        true,
			wantField:      "body",
		},
		{
			name:           "unspecified role",
			id:             anyID,
			conversationID: anyConvID,
			authorMemberID: anyMemberID,
			role:           model.MessageRoleUnspecified,
			body:           "text",
			wantErr:        true,
			wantField:      "role",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg, err := model.NewMessage(tc.id, tc.conversationID, tc.authorMemberID, tc.role, tc.body, tc.source)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewMessage: want error on field %q, got nil", tc.wantField)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewMessage: unexpected error: %v", err)
			}

			if msg.Body != tc.body {
				t.Errorf("NewMessage: Body = %q, want %q", msg.Body, tc.body)
			}

			if msg.Source != tc.wantSource {
				t.Errorf("NewMessage: Source = %q, want %q", msg.Source, tc.wantSource)
			}
		})
	}
}

// TestEnumStrings checks that all enum String() methods return known values.
func TestEnumStrings(t *testing.T) {
	t.Parallel()

	t.Run("Role", func(t *testing.T) {
		t.Parallel()

		cases := map[model.Role]string{
			model.RoleDev:        "dev",
			model.RoleSpecialist: "specialist",
			model.RoleLead:       "lead",
			model.RoleAdmin:      "admin",
		}

		for role, want := range cases {
			if got := role.String(); got != want {
				t.Errorf("Role(%d).String() = %q, want %q", role, got, want)
			}
		}
	})

	t.Run("MessageRole", func(t *testing.T) {
		t.Parallel()

		cases := map[model.MessageRole]string{
			model.MessageRoleQuestion:      "question",
			model.MessageRoleAnswer:        "answer",
			model.MessageRoleFollowUp:      "follow_up",
			model.MessageRoleSystem:        "system",
			model.MessageRoleSecondOpinion: "second_opinion",
		}

		for r, want := range cases {
			if got := r.String(); got != want {
				t.Errorf("MessageRole(%d).String() = %q, want %q", r, got, want)
			}
		}
	})
}

// asInvalidError checks whether err is (or wraps) an errs.InvalidError,
// assigning it to dst when true.
func asInvalidError(err error, dst *errs.InvalidError) bool {
	return errors.As(err, dst)
}
