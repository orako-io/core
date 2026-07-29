// SPDX-License-Identifier: AGPL-3.0-or-later

package discord_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/adapters/provider/discord"
	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/application/service"
)

// discordMaxContentRunes mirrors Discord's 2000-character Content cap, enforced
// by the fake API below so an un-truncated body fails a test the way prod's
// HTTP 400 / code 50035 did.
const discordMaxContentRunes = 2000

// rejectsOverLongContent replies with Discord's real over-length error (code
// 50035) when content exceeds the 2000-character cap, and returns whether it
// did so. Shared by the DM and channel message handlers.
func rejectsOverLongContent(w http.ResponseWriter, content string) bool {
	if utf8.RuneCountInString(content) <= discordMaxContentRunes {
		return false
	}

	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 50035, "message": "Invalid Form Body"})

	return true
}

var (
	_ service.Provider      = (*discord.Provider)(nil)
	_ service.Editor        = (*discord.Provider)(nil)
	_ service.ChannelPoster = (*discord.Provider)(nil)
)

// fakeDiscordAPI fakes the Discord REST endpoints the Provider calls, and
// records the last request body/method per path for assertions.
type fakeDiscordAPI struct {
	lastBody   map[string]any
	lastMethod string
	// contents accumulates every message "content" posted to the DM channel,
	// in order — the split-delivery assertions read the whole sequence.
	contents []string
	// uploadedFiles maps an uploaded filename to its bytes, captured from
	// multipart (file) message sends — the attachment-delivery assertions.
	uploadedFiles map[string][]byte
	blockDM       bool // when true, DM channel creation succeeds but sending fails with code 50007
}

// recordMultipartFiles parses a multipart Discord message send and records each
// uploaded file's bytes. Returns true when the request was multipart (a file
// send) so the JSON path is skipped.
func (f *fakeDiscordAPI) recordMultipartFiles(r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return false
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return true
	}

	for _, headers := range r.MultipartForm.File {
		for _, h := range headers {
			file, err := h.Open()
			if err != nil {
				continue
			}

			data, _ := io.ReadAll(file)
			_ = file.Close()
			f.uploadedFiles[h.Filename] = data
		}
	}

	return true
}

func newFakeDiscordAPI(t *testing.T) (*httptest.Server, *fakeDiscordAPI) {
	t.Helper()

	fake := &fakeDiscordAPI{lastBody: map[string]any{}, uploadedFiles: map[string][]byte{}}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v9/users/@me/channels", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "dm-channel-1"})
	})

	mux.HandleFunc("/api/v9/channels/dm-channel-1/messages", func(w http.ResponseWriter, r *http.Request) {
		fake.lastMethod = r.Method

		if fake.recordMultipartFiles(r) {
			fake.contents = append(fake.contents, "")

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": fmt.Sprintf("msg-%d", len(fake.contents))})

			return
		}

		_ = json.NewDecoder(r.Body).Decode(&fake.lastBody)

		if fake.blockDM {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 50007, "message": "Cannot send messages to this user"})

			return
		}

		content, _ := fake.lastBody["content"].(string)
		if rejectsOverLongContent(w, content) {
			return
		}

		fake.contents = append(fake.contents, content)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": fmt.Sprintf("msg-%d", len(fake.contents))})
	})

	mux.HandleFunc("/api/v9/channels/dm-channel-1/messages/msg-1", func(w http.ResponseWriter, r *http.Request) {
		fake.lastMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&fake.lastBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "msg-1"})
	})

	mux.HandleFunc("/api/v9/channels/alert-channel/messages", func(w http.ResponseWriter, r *http.Request) {
		fake.lastMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&fake.lastBody)

		if content, _ := fake.lastBody["content"].(string); rejectsOverLongContent(w, content) {
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "msg-channel-1"})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server, fake
}

// TestProvider_Deliver_PlainText proves Deliver creates a DM channel, sends
// the message as plain text — never any components (Claim buttons are retired)
// — and returns the channel/message ref.
func TestProvider_Deliver_PlainText(t *testing.T) {
	t.Parallel()

	server, fake := newFakeDiscordAPI(t)

	members := newFakeMemberStore()
	targetID := uuid.New()
	members.add(model.Member{ID: targetID, DiscordUserID: "discord-user-1"})

	p := discord.NewForTest(discord.Config{BotToken: "test-token"}, server.URL, members, nil)

	ref, err := p.Deliver(t.Context(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "ping",
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	if ref.ChannelID != "dm-channel-1" || ref.MessageID != "msg-1" {
		t.Errorf("MessageRef: got %+v, want {dm-channel-1 msg-1}", ref)
	}

	if components, present := fake.lastBody["components"]; present {
		if arr, ok := components.([]any); ok && len(arr) != 0 {
			t.Errorf("plain-text send must not carry components, got %+v", components)
		}
	}
}

// TestProvider_Deliver_UploadsAttachment proves Deliver, given an outbound
// attachment, downloads the bytes from its signed URL and re-uploads them to
// Discord as a multipart file following the text message.
func TestProvider_Deliver_UploadsAttachment(t *testing.T) {
	t.Parallel()

	const fileBytes = "PNG-outbound-bytes"

	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fileBytes))
	}))
	t.Cleanup(blob.Close)

	server, fake := newFakeDiscordAPI(t)

	members := newFakeMemberStore()
	targetID := uuid.New()
	members.add(model.Member{ID: targetID, DiscordUserID: "discord-user-1"})

	p := discord.NewForTest(discord.Config{BotToken: "test-token"}, server.URL, members, nil)

	_, err := p.Deliver(t.Context(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "here is the diagram",
		Attachments: []service.OutboundAttachment{{
			Filename: "diagram.png", MimeType: "image/png", SizeBytes: int64(len(fileBytes)),
			URL: blob.URL + "/signed/diagram.png",
		}},
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	got, ok := fake.uploadedFiles["diagram.png"]
	if !ok {
		t.Fatalf("no file uploaded to Discord; uploaded=%v", fake.uploadedFiles)
	}

	if string(got) != fileBytes {
		t.Errorf("uploaded bytes = %q, want %q", got, fileBytes)
	}
}

// TestProvider_Deliver_SplitsOverLongContent proves Deliver splits a body
// larger than Discord's 2000-character cap into sequential messages — every
// chunk within the cap (counted by rune, the body is deliberately multi-byte),
// nothing truncated, and the returned ref is the LAST message's. Sending a
// >2000 body raw would fail with error code 50035, the prod bug that
// originally motivated bounding the body.
func TestProvider_Deliver_SplitsOverLongContent(t *testing.T) {
	t.Parallel()

	server, fake := newFakeDiscordAPI(t)

	members := newFakeMemberStore()
	targetID := uuid.New()
	members.add(model.Member{ID: targetID, DiscordUserID: "discord-user-1"})

	p := discord.NewForTest(discord.Config{BotToken: "test-token"}, server.URL, members, nil)

	ref, err := p.Deliver(t.Context(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          strings.Repeat("é ", 750), // 1500 runes / >2250 bytes
		Context:           strings.Repeat("x ", 750),
	})
	if err != nil {
		t.Fatalf("Deliver must not fail on over-long content: %v", err)
	}

	if len(fake.contents) < 2 {
		t.Fatalf("an over-cap body must split into >= 2 messages, got %d", len(fake.contents))
	}

	total := 0
	for i, content := range fake.contents {
		if n := utf8.RuneCountInString(content); n > discordMaxContentRunes {
			t.Errorf("chunk %d rune count = %d, want <= %d", i, n, discordMaxContentRunes)
		}

		total += utf8.RuneCountInString(content)
	}

	// Nothing truncated: the chunks together carry (at least) the full body
	// minus the separator whitespace trimmed at each cut.
	if total < 3000-2*len(fake.contents) {
		t.Errorf("chunks carry %d runes total; content was dropped", total)
	}

	if wantLast := fmt.Sprintf("msg-%d", len(fake.contents)); ref.MessageID != wantLast {
		t.Errorf("ref = %q, want the LAST chunk's id %q", ref.MessageID, wantLast)
	}
}

// TestProvider_Deliver_SplitFooterOnLastChunk proves that when a base URL is
// configured, the answer-link footer lands on the LAST chunk only — the
// responder finishes reading and the link is right there.
func TestProvider_Deliver_SplitFooterOnLastChunk(t *testing.T) {
	t.Parallel()

	server, fake := newFakeDiscordAPI(t)

	members := newFakeMemberStore()
	targetID := uuid.New()
	members.add(model.Member{ID: targetID, DiscordUserID: "discord-user-1"})

	p := discord.NewForTest(discord.Config{BotToken: "test-token", DashboardBaseURL: "https://app.orako.io"}, server.URL, members, nil)

	convID := uuid.New()

	_, err := p.Deliver(t.Context(), service.OutboundMessage{
		ConversationID:    convID,
		ResponderMemberID: targetID,
		Question:          strings.Repeat("Une phrase complète ici. ", 200), // ~5000 runes
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	if len(fake.contents) < 2 {
		t.Fatalf("expected a split, got %d message(s)", len(fake.contents))
	}

	wantURL := "https://app.orako.io/conversations/" + convID.String()

	for i, content := range fake.contents[:len(fake.contents)-1] {
		if strings.Contains(content, wantURL) {
			t.Errorf("chunk %d (non-last) must not carry the answer link", i)
		}
	}

	if last := fake.contents[len(fake.contents)-1]; !strings.HasSuffix(last, wantURL) {
		t.Errorf("the LAST chunk must end with the conversation link %q, got tail %q", wantURL, lastRunes(last, 80))
	}
}

// lastRunes returns the final n runes of s, for readable failure messages on
// truncated bodies.
func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[len(r)-n:])
}

// TestProvider_Deliver_UnboundMember proves Deliver fails clearly when the
// recipient has no Discord binding.
func TestProvider_Deliver_UnboundMember(t *testing.T) {
	t.Parallel()

	server, _ := newFakeDiscordAPI(t)

	members := newFakeMemberStore()
	targetID := uuid.New()
	members.add(model.Member{ID: targetID, DiscordUserID: ""})

	p := discord.NewForTest(discord.Config{BotToken: "test-token"}, server.URL, members, nil)

	_, err := p.Deliver(t.Context(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "ping",
	})
	if err == nil {
		t.Fatal("want error for unbound member, got nil")
	}
}

// TestProvider_Deliver_DMBlocked proves a privacy-blocked DM surfaces
// discord.ErrDMBlocked.
func TestProvider_Deliver_DMBlocked(t *testing.T) {
	t.Parallel()

	server, fake := newFakeDiscordAPI(t)
	fake.blockDM = true

	members := newFakeMemberStore()
	targetID := uuid.New()
	members.add(model.Member{ID: targetID, DiscordUserID: "discord-user-1"})

	p := discord.NewForTest(discord.Config{BotToken: "test-token"}, server.URL, members, nil)

	_, err := p.Deliver(t.Context(), service.OutboundMessage{
		ConversationID:    uuid.New(),
		ResponderMemberID: targetID,
		Question:          "ping",
	})
	if err == nil {
		t.Fatal("want error for blocked DM, got nil")
	}

	if !errors.Is(err, discord.ErrDMBlocked) {
		t.Errorf("got %v, want errors.Is(err, discord.ErrDMBlocked)", err)
	}
}

// TestProvider_Edit_ClearsComponents proves Edit rewrites the message text
// and always clears components — a legacy message delivered before the claim
// teardown may still carry a Claim button; the edit retires it.
func TestProvider_Edit_ClearsComponents(t *testing.T) {
	t.Parallel()

	server, fake := newFakeDiscordAPI(t)

	p := discord.NewForTest(discord.Config{BotToken: "test-token"}, server.URL, newFakeMemberStore(), nil)

	err := p.Edit(t.Context(), service.MessageRef{ChannelID: "dm-channel-1", MessageID: "msg-1"}, "still open")
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if got, _ := fake.lastBody["content"].(string); got != "still open" {
		t.Errorf("edited content: got %q, want %q", got, "still open")
	}

	components, present := fake.lastBody["components"]
	if !present {
		t.Fatalf("edit must send an explicit empty components array (clearing legacy Claim buttons), got %+v", fake.lastBody)
	}

	if arr, ok := components.([]any); !ok || len(arr) != 0 {
		t.Errorf("edit must clear components, got %+v", components)
	}
}

// TestProvider_PostChannel proves PostChannel posts to a named channel.
func TestProvider_PostChannel(t *testing.T) {
	t.Parallel()

	server, _ := newFakeDiscordAPI(t)

	p := discord.NewForTest(discord.Config{BotToken: "test-token"}, server.URL, newFakeMemberStore(), nil)

	ref, err := p.PostChannel(t.Context(), "alert-channel", "resolution posted")
	if err != nil {
		t.Fatalf("PostChannel: %v", err)
	}

	if ref.ChannelID != "alert-channel" {
		t.Errorf("ChannelID: got %q, want alert-channel", ref.ChannelID)
	}
}

// TestProvider_ParseInbound_AlwaysUnrecognized proves ParseInbound always
// returns ErrUnrecognizedMessage: Discord has no inbound webhook.
func TestProvider_ParseInbound_AlwaysUnrecognized(t *testing.T) {
	t.Parallel()

	server, _ := newFakeDiscordAPI(t)

	p := discord.NewForTest(discord.Config{BotToken: "test-token"}, server.URL, newFakeMemberStore(), nil)

	if _, err := p.ParseInbound(t.Context(), []byte("{}")); !errors.Is(err, service.ErrUnrecognizedMessage) {
		t.Errorf("got %v, want ErrUnrecognizedMessage", err)
	}
}
