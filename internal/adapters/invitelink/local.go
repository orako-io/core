// SPDX-License-Identifier: AGPL-3.0-or-later

package invitelink

import (
	"context"
	"fmt"
	"time"

	"github.com/orako-io/core/internal/pkg/auth"
)

// Local generates the signed invite token embedded in invitation emails for the
// self-host "local" auth mode: the click carries an HS256 token the accept-invite
// endpoint verifies, no external IdP. Satisfies service.InviteLinkGenerator, the
// self-host counterpart to Supabase.
type Local struct {
	secret string
	ttl    time.Duration
	now    func() time.Time
}

// NewLocal builds the generator. secret must be ORAKO_AUTH_HS256_SECRET.
func NewLocal(secret string, ttl time.Duration) Local {
	return Local{secret: secret, ttl: ttl, now: time.Now}
}

// GenerateInviteLink mints a signed invite token for email. The link type is
// always "invite" (there is no already-registered magiclink path in local mode).
func (l Local) GenerateInviteLink(_ context.Context, email string) (string, string, error) {
	token, err := auth.MintInviteToken(l.secret, email, l.ttl, l.now())
	if err != nil {
		return "", "", fmt.Errorf("minting invite token: %w", err)
	}

	return token, linkTypeInvite, nil
}
