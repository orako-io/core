// SPDX-License-Identifier: AGPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/auth"
	"github.com/orako-io/core/internal/pkg/errs"
)

// ErrInvalidReset is returned for a bad/expired reset token or an email that no
// longer has a local account — uniform, so it cannot probe which emails exist.
var ErrInvalidReset = errs.InvalidError{Field: "reset", Reason: "invalid or expired reset link"}

// resetDeliverTimeout bounds the detached reset-email delivery (D2).
const resetDeliverTimeout = 30 * time.Second

// passwordResetStore reads the local account's current reset version to stamp a
// reset token and atomically spends that version while replacing the password.
// *identity.AccountStore satisfies it.
type passwordResetStore interface {
	ResetVersionByEmail(ctx context.Context, email string) (accountID uuid.UUID, version int, ok bool, err error)
	ResetPassword(ctx context.Context, email string, expectedVersion int, hash string) (updated bool, err error)
}

// ResetHandler drives the self-host "forgot password" flow: RequestReset emails a
// signed link, PerformReset spends it to write a new password. Both are uniform
// against email enumeration.
type ResetHandler struct {
	store   passwordResetStore
	mailer  service.Mailer
	secret  string
	baseURL string
	ttl     time.Duration
	now     func() time.Time
	log     *slog.Logger
}

// NewResetHandler builds the handler. secret must be ORAKO_AUTH_HS256_SECRET;
// baseURL is the dashboard origin the reset link points at.
func NewResetHandler(store passwordResetStore, mailer service.Mailer, secret, baseURL string, ttl time.Duration, logger *slog.Logger) ResetHandler {
	if logger == nil {
		logger = slog.Default()
	}

	return ResetHandler{store: store, mailer: mailer, secret: secret, baseURL: baseURL, ttl: ttl, now: time.Now, log: logger}
}

// RequestReset emails a reset link when email has a local account, and does
// nothing otherwise. The lookup + mint + SMTP send run asynchronously on a
// detached context so the caller returns immediately in EVERY case — response
// latency can't reveal whether the email exists (M3 timing enumeration).
// Failures are logged.
func (h ResetHandler) RequestReset(ctx context.Context, email string) {
	go func() {
		// Detached from the request, but time-bounded so a slow store/SMTP can't
		// leak the goroutine forever (D2).
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), resetDeliverTimeout)
		defer cancel()

		h.deliverReset(bg, email)
	}()
}

// deliverReset does the actual lookup, token mint, and email send.
func (h ResetHandler) deliverReset(ctx context.Context, email string) {
	accountID, version, ok, err := h.store.ResetVersionByEmail(ctx, email)
	if err != nil {
		h.log.WarnContext(ctx, "reset: lookup failed", slog.Any("error", err))
		return
	}

	if !ok {
		// Unknown email or an IdP-only account with no local password. Silently
		// no-op — never reveal which case it was.
		return
	}

	// Stamp the token with the current reset version so it can be spent once (L1).
	token, err := auth.MintResetToken(h.secret, email, version, h.ttl, h.now())
	if err != nil {
		h.log.WarnContext(ctx, "reset: minting token failed", slog.String("account_id", accountID.String()), slog.Any("error", err))
		return
	}

	if err := h.mailer.Send(ctx, resetEmail(email, h.resetLink(email, token))); err != nil {
		h.log.WarnContext(ctx, "reset: sending email failed", slog.String("account_id", accountID.String()), slog.Any("error", err))
		return
	}

	h.log.InfoContext(ctx, "password reset email sent", slog.String("account_id", accountID.String()))
}

// PerformReset verifies the reset token and writes the new password. Rejects an
// unverifiable/expired token, a too-short password, or an email whose local
// account has since gone.
func (h ResetHandler) PerformReset(ctx context.Context, token, newPassword string) error {
	if len(newPassword) < minPasswordLen {
		return errs.InvalidError{Field: fieldPassword, Reason: fmt.Sprintf("password must be at least %d characters", minPasswordLen)}
	}

	email, tokenVersion, err := auth.VerifyResetToken(h.secret, token)
	if err != nil {
		return ErrInvalidReset
	}

	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return errs.InvalidError{Field: fieldPassword, Reason: "password is too long (max 72 bytes)"}
	}

	updated, err := h.store.ResetPassword(ctx, email, tokenVersion, hash)
	if err != nil {
		return errs.InternalError{Err: fmt.Errorf("resetting password: %w", err)}
	}

	// A zero-row conditional update uniformly represents an unknown/IdP-only
	// account, an already-spent token, or a token superseded by another reset.
	if !updated {
		return ErrInvalidReset
	}

	return nil
}

// resetLink builds the dashboard URL the reset email points at. It reuses the
// /auth/callback route (type=reset) that already handles the local set-password
// screen for invites.
func (h ResetHandler) resetLink(email, token string) string {
	base := strings.TrimRight(h.baseURL, "/")

	return base + "/auth/callback?" + url.Values{
		"token_hash": {token},
		"type":       {"reset"},
		"email":      {email}, // display prefill only, never trusted
	}.Encode()
}

// resetEmail builds the "reset your password" email. The HTML mirrors the
// invitation email's card styling for a consistent look.
func resetEmail(email, resetURL string) model.EmailMessage {
	var text strings.Builder

	text.WriteString("Hi,\n\n")
	text.WriteString("We received a request to reset the password for your Orako account.\n\n")
	fmt.Fprintf(&text, "Reset your password: %s\n\n", resetURL)
	text.WriteString("If you didn't ask for this, you can ignore this email — your password stays unchanged.\n\n")
	text.WriteString("— Orako\n")

	return model.EmailMessage{
		To:       email,
		Subject:  "Reset your Orako password",
		TextBody: text.String(),
		HTMLBody: resetHTML(resetURL),
	}
}

// resetHTML renders the reset email as email-client-safe HTML (tables + inline
// styles), visually consistent with the invitation email.
func resetHTML(resetURL string) string {
	esc := template.HTMLEscapeString

	return `<!DOCTYPE html>
<html><body style="margin:0;padding:0;background-color:#F7F8FA;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background-color:#F7F8FA;padding:40px 16px;">
<tr><td align="center">
  <table role="presentation" width="440" cellpadding="0" cellspacing="0" style="max-width:440px;width:100%;background-color:#FFFFFF;border:1px solid #E9EBEE;border-radius:18px;">
  <tr><td align="center" style="padding:40px 36px 32px;font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
    <table role="presentation" cellpadding="0" cellspacing="0"><tr>
      <td align="center" style="width:44px;height:44px;border-radius:13px;background-color:#5850EC;font-size:22px;line-height:44px;color:#FFFFFF;font-weight:700;">&#128273;</td>
    </tr></table>
    <h1 style="margin:22px 0 0;font-size:22px;line-height:1.3;font-weight:700;color:#171B24;">
      Reset your password
    </h1>
    <p style="margin:12px 0 0;font-size:14.5px;line-height:1.6;color:#6B7280;">
      We received a request to reset your Orako password. Choose a new one below.
      If you didn&rsquo;t ask for this, you can safely ignore this email.
    </p>
    <table role="presentation" cellpadding="0" cellspacing="0" width="100%" style="margin-top:26px;"><tr>
      <td align="center" style="border-radius:11px;background-color:#5850EC;">
        <a href="` + esc(resetURL) + `" style="display:inline-block;padding:14px 24px;width:100%;box-sizing:border-box;font-size:15px;font-weight:600;color:#FFFFFF;text-decoration:none;font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">Reset password</a>
      </td>
    </tr></table>
  </td></tr>
  </table>
  <p style="margin:20px 0 0;font-size:12px;color:#9AA1AC;font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
    If the button doesn&rsquo;t work, copy this link: ` + esc(resetURL) + `<br>&mdash; Orako
  </p>
</td></tr>
</table>
</body></html>`
}
