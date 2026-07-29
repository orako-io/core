// SPDX-License-Identifier: AGPL-3.0-or-later

// Package inbound ingests attachments that arrive on a human's reply (a photo
// or document sent on Discord/Telegram/…): it downloads each one from its
// provider URL, stores the bytes through the same upload path an agent uses,
// and returns the resulting attachment ids to link onto the reply. Shared by
// every inbound transport (the generic webhook and the Discord gateway) so the
// download-bound + never-log-the-URL discipline lives in exactly one place.
package inbound

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/service"
)

// defaultMaxBytes caps a single download when the caller passes a non-positive
// limit — a conservative backstop, not the configured server cap.
const defaultMaxBytes = 25 << 20

// uploader stores a downloaded inbound attachment as an unlinked row and
// returns its id. *command.UploadAttachmentHandler satisfies it — the ingestor
// reuses the same blob-Put + row-Create + membership gate + orphan-cleanup path
// as an agent's upload_attachment.
type uploader interface {
	Handle(ctx context.Context, cmd command.UploadAttachmentCommand) (command.UploadAttachmentResult, error)
}

// Ingestor downloads, stores, and records inbound attachments.
type Ingestor struct {
	uploader   uploader
	httpClient *http.Client
	maxBytes   int64
	logger     *slog.Logger
}

// NewIngestor builds an Ingestor. A nil uploader disables ingestion (Ingest
// then returns no ids). Because attachment fetches reach untrusted,
// provider-derived URLs, callers should pass an SSRF-guarded client
// ([NewGuardedClient]) — the production wiring does. A nil client defaults to a
// guarded one; download() additionally rejects any non-https URL.
func NewIngestor(up uploader, httpClient *http.Client, maxBytes int64, logger *slog.Logger) *Ingestor {
	if httpClient == nil {
		httpClient = NewGuardedClient(nil)
	}

	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}

	return &Ingestor{uploader: up, httpClient: httpClient, maxBytes: maxBytes, logger: logger}
}

// NewGuardedClient returns an http.Client whose transport refuses to connect to
// any non-public IP, carrying over base's Timeout (default 30s). The check runs
// in the dialer's Control hook — after DNS resolution, before connect — so it
// also defends against DNS-rebinding: the IP verified is the IP dialed. Wire
// this around the client used for any server-side fetch of an attacker-derived
// URL (inbound attachment downloads).
func NewGuardedClient(base *http.Client) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: guardDialAddress}

	timeout := 30 * time.Second
	if base != nil && base.Timeout > 0 {
		timeout = base.Timeout
	}

	return &http.Client{
		Transport: &http.Transport{DialContext: dialer.DialContext, ForceAttemptHTTP2: true},
		Timeout:   timeout,
	}
}

// guardDialAddress rejects a dial to a non-public address (SSRF guard). address
// is "ip:port" — Control runs per resolved IP, so a hostname that resolves to a
// private/link-local address (e.g. the 169.254.169.254 cloud-metadata endpoint)
// is refused at connect time.
func guardDialAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("ssrf guard: bad dial address: %w", err)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("ssrf guard: unresolved dial host %q", host)
	}

	if isBlockedIP(ip) {
		return errors.New("ssrf guard: refusing to dial a non-public address")
	}

	return nil
}

// isBlockedIP reports whether ip is off-limits for an outbound attachment fetch:
// loopback, link-local (v4 169.254/16 and v6 fe80::/10 — covers cloud metadata),
// private (RFC1918 + ULA fc00::/7), unspecified, and multicast.
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}

// Ingest downloads, stores, and records every attachment, returning the ids of
// those that succeeded. It NEVER fails the caller: any download/store error is
// logged and that one attachment is skipped so the text reply still goes
// through. Returns nil when ingestion is off (nil ingestor or uploader) or the
// message carried no attachments. att.FetchURL may carry a bearer token (e.g.
// Telegram's) — it is fetched once and never logged.
func (i *Ingestor) Ingest(ctx context.Context, conversationID, authorMemberID uuid.UUID, atts []service.InboundAttachment) []uuid.UUID {
	if i == nil || i.uploader == nil || len(atts) == 0 {
		return nil
	}

	ids := make([]uuid.UUID, 0, len(atts))

	for _, att := range atts {
		content, err := i.download(ctx, att.FetchURL)
		if err != nil {
			// Never log FetchURL — it may carry a bearer token.
			i.logger.WarnContext(ctx, "inbound: downloading attachment",
				slog.String("conversation_id", conversationID.String()),
				slog.String("filename", att.Filename),
				slog.Any("error", err))

			continue
		}

		res, err := i.uploader.Handle(ctx, command.UploadAttachmentCommand{
			ConversationID:   conversationID,
			UploaderMemberID: authorMemberID,
			Filename:         att.Filename,
			MimeType:         att.MimeType,
			Content:          content,
		})
		if err != nil {
			i.logger.WarnContext(ctx, "inbound: storing attachment",
				slog.String("conversation_id", conversationID.String()),
				slog.String("filename", att.Filename),
				slog.Any("error", err))

			continue
		}

		ids = append(ids, res.AttachmentID)
	}

	return ids
}

// download fetches an attachment URL, bounded to maxBytes. The URL is never
// logged (it may carry a bearer token). A body exceeding the cap is rejected
// rather than truncated.
func (i *Ingestor) download(ctx context.Context, fetchURL string) ([]byte, error) {
	// Reject anything but an https URL with a host up front: closes non-http
	// schemes (file://, gopher://, …) and blank hosts before a request is built.
	// The URL is never echoed into the error — it may carry a bearer token.
	parsed, err := url.Parse(fetchURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return nil, errors.New("attachment url must be an https URL with a host")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building download request: %w", err)
	}

	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, errors.New("attachment download failed") // omit err: URL may be embedded
	}

	defer resp.Body.Close() //nolint:errcheck // informational close error in defer

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("attachment download: unexpected status %d", resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, i.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading attachment body: %w", err)
	}

	if int64(len(content)) > i.maxBytes {
		return nil, fmt.Errorf("attachment exceeds %d-byte limit", i.maxBytes)
	}

	return content, nil
}
