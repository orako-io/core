// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// scriptedAdder returns a per-email scripted outcome.
type scriptedAdder struct {
	already map[string]bool
	failing map[string]bool
	calls   int
}

func (a *scriptedAdder) Handle(_ context.Context, cmd AddMemberCommand) (AddMemberResult, error) {
	a.calls++

	if a.failing[cmd.Email] {
		return AddMemberResult{}, errors.New("boom")
	}

	return AddMemberResult{MemberID: uuid.New(), AlreadyMember: a.already[cmd.Email]}, nil
}

// TestInviteMembers_PerEmailStatus proves the batch reports invited / already /
// invalid / error per email, dedups, and never aborts on one failure.
func TestInviteMembers_PerEmailStatus(t *testing.T) {
	t.Parallel()

	adder := &scriptedAdder{
		already: map[string]bool{"bob@co.com": true},
		failing: map[string]bool{"carol@co.com": true},
	}
	h := MustNewInviteMembersHandler(adder)

	res, err := h.Handle(t.Context(), InviteMembersCommand{
		ProjectID: uuid.New(),
		Emails:    []string{"alice@co.com", "Alice@co.com", "bob@co.com", "carol@co.com", "not-an-email", ""},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got := map[string]string{}
	for _, r := range res.Results {
		got[r.Email] = r.Status
	}

	if got["alice@co.com"] != "invited" || got["bob@co.com"] != "already" ||
		got["carol@co.com"] != "error" || got["not-an-email"] != "invalid" {
		t.Fatalf("statuses = %+v", got)
	}

	// "Alice@co.com" is the same as "alice@co.com" after normalization → one add call for it.
	if _, dup := got["Alice@co.com"]; dup {
		t.Errorf("duplicate email should have been folded, got %+v", got)
	}

	// alice + bob + carol = 3 adder calls (invalid + empty + dup never reach it).
	if adder.calls != 3 {
		t.Errorf("adder called %d times, want 3", adder.calls)
	}
}
