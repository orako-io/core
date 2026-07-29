// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"

	"github.com/google/uuid"
)

// chatDirectoryResolver maps a member's email to their external chat id on a
// provider that exposes a workspace directory. Slack does (users.lookupByEmail);
// Discord does not (no email exposed — it self-binds via OAuth instead).
// *slack.OrgDirectory satisfies it. Optional: a nil resolver turns auto-bind
// off, and ANY error (no provider, user not found, transient) is treated by the
// caller as "no binding — fall back to email", never a failure.
type chatDirectoryResolver interface {
	LookupSlackByEmail(ctx context.Context, projectID uuid.UUID, email string) (string, error)
}
