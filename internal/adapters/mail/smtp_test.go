// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/orako-io/core/internal/application/domain/model"
)

func TestBuildMIME_PlainText(t *testing.T) {
	t.Parallel()

	raw := string(buildMIME("Orako <no-reply@orako.io>", model.EmailMessage{
		To:       "sarah@example.com",
		Subject:  "You have a question",
		TextBody: "Hi Sarah,\nOpen your inbox.",
	}))

	for _, want := range []string{
		"From: Orako <no-reply@orako.io>\r\n",
		"To: sarah@example.com\r\n",
		"Subject: You have a question\r\n",
		"Content-Type: text/plain; charset=\"UTF-8\"",
		"Open your inbox.",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("plain MIME missing %q\n---\n%s", want, raw)
		}
	}

	if strings.Contains(raw, "multipart/alternative") {
		t.Error("plain-text message should not be multipart")
	}
}

func TestBuildMIME_Multipart(t *testing.T) {
	t.Parallel()

	raw := string(buildMIME("Orako <no-reply@orako.io>", model.EmailMessage{
		To:       "sarah@example.com",
		Subject:  "You have a question",
		TextBody: "plain body",
		HTMLBody: "<p>html body</p>",
	}))

	for _, want := range []string{
		"multipart/alternative; boundary=",
		"Content-Type: text/plain; charset=\"UTF-8\"",
		"Content-Type: text/html; charset=\"UTF-8\"",
		"plain body",
		"<p>html body</p>",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("multipart MIME missing %q\n---\n%s", want, raw)
		}
	}
}

func TestNewSMTP_RequiresHostAndFrom(t *testing.T) {
	t.Parallel()

	if _, err := NewSMTP(Config{From: "x@y.z"}); err == nil {
		t.Error("expected error when host is missing")
	}

	if _, err := NewSMTP(Config{Host: "smtp.example.com"}); err == nil {
		t.Error("expected error when From is missing")
	}
}

func TestNoopMailer_SendReturnsNil(t *testing.T) {
	t.Parallel()

	if err := NewNoop(nil).Send(context.Background(), model.EmailMessage{To: "a@b.c"}); err != nil {
		t.Errorf("noop Send returned error: %v", err)
	}
}

func TestSMTPMailer_SendCancelInterruptsStartTLS(t *testing.T) {
	t.Parallel()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	startTLSReceived := make(chan struct{})
	serverDone := make(chan struct{})

	go func() {
		defer close(serverDone)

		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		reader := bufio.NewReader(conn)
		_, _ = conn.Write([]byte("220 smtp.test ESMTP\r\n"))

		if _, readErr := reader.ReadString('\n'); readErr != nil {
			return
		}

		_, _ = conn.Write([]byte("250-smtp.test\r\n250 STARTTLS\r\n"))

		if line, readErr := reader.ReadString('\n'); readErr == nil && strings.HasPrefix(line, "STARTTLS") {
			close(startTLSReceived)
		}

		_, _ = reader.ReadByte()
	}()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}

	mailer, err := NewSMTP(Config{
		Host:     host,
		Port:     port,
		Username: "user",
		Password: "password",
		From:     "sender@example.com",
	})
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	sendDone := make(chan error, 1)

	go func() {
		sendDone <- mailer.Send(ctx, model.EmailMessage{
			To:       "recipient@example.com",
			Subject:  "subject",
			TextBody: "body",
		})
	}()

	select {
	case <-startTLSReceived:
	case <-time.After(time.Second):
		t.Fatal("SMTP client did not reach STARTTLS")
	}

	cancel()

	select {
	case sendErr := <-sendDone:
		if !errors.Is(sendErr, context.Canceled) {
			t.Fatalf("Send error = %v, want context.Canceled", sendErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Send did not stop after context cancellation")
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("SMTP test server did not observe the closed connection")
	}
}

func TestSMTPMailer_ImplicitTLSDialRespectsCancel(t *testing.T) {
	t.Parallel()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	accepted := make(chan struct{})
	serverDone := make(chan struct{})

	go func() {
		defer close(serverDone)

		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		close(accepted)
		_, _ = io.Copy(io.Discard, conn)
	}()

	host, _, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}

	mailer := &SMTPMailer{
		cfg:          Config{Host: host},
		addr:         listener.Addr().String(),
		auth:         nil,
		headerFrom:   "sender@example.com",
		envelopeFrom: "sender@example.com",
		implicitTLS:  true,
	}

	ctx, cancel := context.WithCancel(t.Context())
	sendDone := make(chan error, 1)

	go func() {
		sendDone <- mailer.Send(ctx, model.EmailMessage{
			To:       "recipient@example.com",
			Subject:  "subject",
			TextBody: "body",
		})
	}()

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("SMTP client did not connect")
	}

	cancel()

	select {
	case sendErr := <-sendDone:
		if !errors.Is(sendErr, context.Canceled) {
			t.Fatalf("Send error = %v, want context.Canceled", sendErr)
		}
	case <-time.After(time.Second):
		t.Fatal("implicit TLS dial did not stop after context cancellation")
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("implicit TLS test server did not observe the closed connection")
	}
}

func TestBindConnContext_AppliesDeadline(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()

	releaseContext, err := bindConnContext(ctx, clientConn)
	if err != nil {
		t.Fatalf("bindConnContext: %v", err)
	}
	defer releaseContext()

	_, err = clientConn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("Read succeeded, want deadline error")
	}

	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("Read error = %v, want network timeout", err)
	}
}
