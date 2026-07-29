// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mail provides the outbound transactional email adapters behind the
// service.Mailer port: SMTPMailer (plain net/smtp, no vendor SDK) and
// NoopMailer for when SMTP is not configured.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// smtpDialTimeout bounds the TCP dial to the SMTP server so a hung mail server
// can't wedge a (possibly detached) sender goroutine.
const smtpDialTimeout = 15 * time.Second

var (
	_ service.Mailer = (*SMTPMailer)(nil)
	_ service.Mailer = (*NoopMailer)(nil)
)

// Config holds the SMTP connection settings. Host, Username, Password and From
// are required; Port defaults to the submission port when empty.
type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// SMTPMailer sends email over SMTP, selecting the mode from the port:
// STARTTLS on 587/2587 (default), implicit TLS / SMTPS on 465/2465.
type SMTPMailer struct {
	cfg  Config
	addr string
	auth smtp.Auth
	// headerFrom is the raw From header value (may include a display name).
	headerFrom string
	// envelopeFrom is the bare address used for the SMTP MAIL FROM command.
	envelopeFrom string
	implicitTLS  bool
}

// NewSMTP builds an SMTPMailer from cfg, failing fast at startup on missing
// fields or an unparseable From address.
func NewSMTP(cfg Config) (*SMTPMailer, error) {
	if cfg.Host == "" {
		return nil, errors.New("mail: SMTP host is required")
	}

	if cfg.From == "" {
		return nil, errors.New("mail: SMTP From address is required")
	}

	// The From header may be "Name <addr>"; the envelope MAIL FROM must be the
	// bare address.
	parsed, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("mail: invalid From address %q: %w", cfg.From, err)
	}

	port := cfg.Port
	if port == "" {
		port = "587"
	}

	return &SMTPMailer{
		cfg:          cfg,
		addr:         net.JoinHostPort(cfg.Host, port),
		auth:         smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host),
		headerFrom:   cfg.From,
		envelopeFrom: parsed.Address,
		implicitTLS:  port == "465" || port == "2465",
	}, nil
}

// Send delivers msg via SMTP, using implicit TLS on 465/2465 and STARTTLS
// otherwise.
func (m *SMTPMailer) Send(ctx context.Context, msg model.EmailMessage) error {
	if msg.To == "" {
		return errors.New("mail: recipient (To) is required")
	}

	// Refuse CR/LF in header values to block SMTP header injection (M1): the
	// Subject is built from user-controlled org/project names, and TrimSpace on
	// those names does not strip internal newlines.
	if strings.ContainsAny(msg.To, "\r\n") || strings.ContainsAny(msg.Subject, "\r\n") {
		return errors.New("mail: header value contains a newline")
	}

	raw := buildMIME(m.headerFrom, msg)

	if m.implicitTLS {
		if err := m.sendImplicitTLS(ctx, msg.To, raw); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				err = ctxErr
			}

			return fmt.Errorf("mail: sending to %s: %w", msg.To, err)
		}

		return nil
	}

	if err := m.sendStartTLS(ctx, msg.To, raw); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}

		return fmt.Errorf("mail: sending to %s: %w", msg.To, err)
	}

	return nil
}

// sendStartTLS delivers over a plain connection upgraded with STARTTLS. Unlike
// net/smtp.SendMail it dials with a bounded, context-aware dialer, so a slow or
// hung SMTP server can't wedge the caller's goroutine indefinitely (the reset
// email path now runs detached — D2).
func (m *SMTPMailer) sendStartTLS(ctx context.Context, to string, raw []byte) error {
	dialer := net.Dialer{Timeout: smtpDialTimeout}

	conn, err := dialer.DialContext(ctx, "tcp", m.addr)
	if err != nil {
		return fmt.Errorf("dialing: %w", err)
	}
	defer func() { _ = conn.Close() }()

	releaseContext, err := bindConnContext(ctx, conn)
	if err != nil {
		return fmt.Errorf("binding connection context: %w", err)
	}
	defer releaseContext()

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.StartTLS(&tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
		return fmt.Errorf("starttls: %w", err)
	}

	if err := client.Auth(m.auth); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if err := client.Mail(m.envelopeFrom); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}

	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}

	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("writing body: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("closing body: %w", err)
	}

	return client.Quit()
}

// sendImplicitTLS delivers over SMTPS: TLS from the first byte, as required on 465.
func (m *SMTPMailer) sendImplicitTLS(ctx context.Context, to string, raw []byte) error {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: smtpDialTimeout},
		Config:    &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12},
	}

	conn, err := dialer.DialContext(ctx, "tcp", m.addr)
	if err != nil {
		return fmt.Errorf("dialing TLS: %w", err)
	}
	defer func() { _ = conn.Close() }()

	releaseContext, err := bindConnContext(ctx, conn)
	if err != nil {
		return fmt.Errorf("binding connection context: %w", err)
	}
	defer releaseContext()

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Auth(m.auth); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if err := client.Mail(m.envelopeFrom); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}

	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}

	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("writing body: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("closing body: %w", err)
	}

	return client.Quit()
}

// bindConnContext propagates both an existing context deadline and later
// cancellation to blocking SMTP operations, whose net/smtp API does not accept
// a context. Cancellation expires the connection deadline and unblocks the
// current read or write.
func bindConnContext(ctx context.Context, conn net.Conn) (func(), error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("setting deadline: %w", err)
		}
	}

	stopCancel := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})

	if err := ctx.Err(); err != nil {
		stopCancel()

		_ = conn.SetDeadline(time.Now())

		return nil, err
	}

	return func() {
		if stopCancel() {
			_ = conn.SetDeadline(time.Time{})
		}
	}, nil
}

// buildMIME assembles a minimal RFC 5322 message. When an HTML body is present
// a multipart/alternative message is produced; otherwise a text/plain message.
func buildMIME(from string, msg model.EmailMessage) []byte {
	var b strings.Builder

	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + msg.To + "\r\n")
	b.WriteString("Subject: " + msg.Subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	if msg.HTMLBody == "" {
		b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n\r\n")
		b.WriteString(msg.TextBody)

		return []byte(b.String())
	}

	const boundary = "orako-boundary-8f3a1c"

	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n\r\n")
	b.WriteString(msg.TextBody + "\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n\r\n")
	b.WriteString(msg.HTMLBody + "\r\n")
	b.WriteString("--" + boundary + "--\r\n")

	return []byte(b.String())
}

// NoopMailer satisfies the Mailer port without sending anything.
type NoopMailer struct {
	logger *slog.Logger
}

// NewNoop builds a NoopMailer. A nil logger disables logging.
func NewNoop(logger *slog.Logger) *NoopMailer {
	return &NoopMailer{logger: logger}
}

// Send records that delivery was skipped without logging recipient PII.
func (m *NoopMailer) Send(_ context.Context, _ model.EmailMessage) error {
	if m.logger != nil {
		m.logger.Debug("email not sent (SMTP not configured)")
	}

	return nil
}
