// SPDX-License-Identifier: AGPL-3.0-or-later

package model

// EmailMessage is a provider-agnostic transactional email. The notifiers and the
// self-host auth flows build it; a concrete Mailer adapter (SMTP today) sends it.
type EmailMessage struct {
	// To is the recipient address.
	To string
	// Subject is the email subject line.
	Subject string
	// TextBody is the plain-text body (always sent).
	TextBody string
	// HTMLBody is the optional HTML body. When empty, a text-only email is sent.
	HTMLBody string `exhaustruct:"optional"`
}
