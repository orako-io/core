// SPDX-License-Identifier: AGPL-3.0-or-later

package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/service"
)

// TestOutboundMessage_Recipient proves the port-semantics precedence every
// adapter must share (rot#7): RecipientMemberID wins when set (a pool
// candidate DM), falling back to ResponderMemberID for the legacy
// direct-ask path.
func TestOutboundMessage_Recipient(t *testing.T) {
	t.Parallel()

	targetID := uuid.New()
	recipientID := uuid.New()

	t.Run("recipient_set_wins_over_specialist", func(t *testing.T) {
		t.Parallel()

		msg := service.OutboundMessage{
			ResponderMemberID: targetID,
			RecipientMemberID: recipientID,
			Question:          "q",
		}

		if got := msg.Recipient(); got != recipientID {
			t.Errorf("Recipient() = %v, want RecipientMemberID %v", got, recipientID)
		}
	})

	t.Run("recipient_nil_falls_back_to_specialist", func(t *testing.T) {
		t.Parallel()

		msg := service.OutboundMessage{
			ResponderMemberID: targetID,
			RecipientMemberID: uuid.Nil,
			Question:          "q",
		}

		if got := msg.Recipient(); got != targetID {
			t.Errorf("Recipient() = %v, want ResponderMemberID %v", got, targetID)
		}
	})
}

// TestConversationURL proves the dashboard deep link matches the web router's
// /conversations/:id route, tolerates a trailing slash on the base, and yields
// "" (no link) when the base URL is unconfigured.
func TestConversationURL(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	cases := map[string]struct {
		base string
		want string
	}{
		"plain_base":     {"https://app.orako.io", "https://app.orako.io/conversations/" + id.String()},
		"trailing_slash": {"https://app.orako.io/", "https://app.orako.io/conversations/" + id.String()},
		"empty_base":     {"", ""},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := service.ConversationURL(tc.base, id); got != tc.want {
				t.Errorf("ConversationURL(%q) = %q, want %q", tc.base, got, tc.want)
			}
		})
	}
}

// TestFormatQuestion proves the shared body: the [orako] prefix always leads, a
// context block is appended only when context is non-empty, and the resume
// footer (carrying the conversation deep link) appears only when convURL is set.
func TestFormatQuestion(t *testing.T) {
	t.Parallel()

	const url = "https://app.orako.io/conversations/x"

	t.Run("question_only", func(t *testing.T) {
		t.Parallel()

		got := service.FormatQuestion("why?", "", "")
		if got != "[orako] why?" {
			t.Errorf("FormatQuestion = %q, want %q", got, "[orako] why?")
		}
	})

	t.Run("with_context_no_url", func(t *testing.T) {
		t.Parallel()

		got := service.FormatQuestion("why?", "src/x.go:1", "")
		if !strings.HasPrefix(got, "[orako] why?") || !strings.Contains(got, "Context: src/x.go:1") {
			t.Errorf("FormatQuestion missing question/context: %q", got)
		}

		if strings.Contains(got, "Answer this thread") {
			t.Errorf("FormatQuestion added a resume footer with no convURL: %q", got)
		}
	})

	t.Run("with_url_appends_resume_footer", func(t *testing.T) {
		t.Parallel()

		got := service.FormatQuestion("why?", "", url)
		if !strings.Contains(got, "Answer this thread") || !strings.Contains(got, url) {
			t.Errorf("FormatQuestion missing resume footer/link: %q", got)
		}

		if !strings.HasPrefix(got, "[orako] why?") {
			t.Errorf("FormatQuestion prefix broken: %q", got)
		}
	})
}

// TestFormatOutbound proves the per-kind chrome decision: a relay is light (no
// [orako] banner, no context, no footer), a history replay carries the answer
// footer once, and a question keeps the classic full body.
func TestFormatOutbound(t *testing.T) {
	t.Parallel()

	const url = "https://app.orako.io/conversations/x"

	base := service.OutboundMessage{
		ProjectID:      uuid.New(),
		ConversationID: uuid.New(),
		Question:       "Jordan:\nUse the composite index.",
		Context:        "ctx",
	}

	t.Run("relay_is_light", func(t *testing.T) {
		t.Parallel()

		msg := base
		msg.Kind = service.MessageKindRelay

		got := service.FormatOutbound(msg, url)
		if got != "💬 Jordan:\nUse the composite index." {
			t.Errorf("relay body = %q, want light body with no banner/context/footer", got)
		}
	})

	t.Run("history_carries_footer_once", func(t *testing.T) {
		t.Parallel()

		msg := base
		msg.Kind = service.MessageKindHistory
		msg.Context = ""

		got := service.FormatOutbound(msg, url)
		if !strings.Contains(got, "Answer this thread") || !strings.Contains(got, url) {
			t.Errorf("history body missing the answer footer: %q", got)
		}

		if strings.HasPrefix(got, "[orako]") {
			t.Errorf("history body should not carry the [orako] banner: %q", got)
		}
	})

	t.Run("question_keeps_full_body", func(t *testing.T) {
		t.Parallel()

		msg := base
		msg.Kind = service.MessageKindQuestion

		got := service.FormatOutbound(msg, url)
		if !strings.HasPrefix(got, "[orako] ") || !strings.Contains(got, "Context: ctx") || !strings.Contains(got, url) {
			t.Errorf("question body should keep prefix+context+footer: %q", got)
		}
	})
}

// TestSplitForDelivery proves the shared splitter (hub-and-spoke phase 3):
// content within the cap comes back as exactly one unchanged chunk, an
// over-cap body is split at paragraph boundaries with every chunk within the
// cap, the answer-link footer lands only on the last chunk, counting is by
// rune (multi-byte safe), and a giant single word still hard-splits rather
// than looping or overflowing.
func TestSplitForDelivery(t *testing.T) {
	t.Parallel()

	t.Run("within_cap_is_one_unchanged_chunk", func(t *testing.T) {
		t.Parallel()

		in := "short question"

		got := service.SplitForDelivery(in, 2000)
		if len(got) != 1 || got[0] != in {
			t.Errorf("SplitForDelivery = %q, want the input as a single chunk", got)
		}
	})

	t.Run("over_cap_splits_at_paragraph_boundaries", func(t *testing.T) {
		t.Parallel()

		para := strings.Repeat("word ", 150) // ~750 runes
		in := strings.Join([]string{para, para, para, para, para, para, para}, "\n\n")

		got := service.SplitForDelivery(in, 2000)
		if len(got) < 3 {
			t.Fatalf("a ~5k body at a 2000 cap should split into >= 3 chunks, got %d", len(got))
		}

		for i, chunk := range got {
			if n := utf8.RuneCountInString(chunk); n > 2000 {
				t.Errorf("chunk %d rune count = %d, want <= 2000", i, n)
			}

			if strings.HasPrefix(chunk, " ") || strings.HasSuffix(chunk, "\n") {
				t.Errorf("chunk %d has stray separator whitespace: %q", i, tail(chunk, 20))
			}

			// A paragraph-aligned cut never leaves a chunk ending mid-word:
			// every chunk ends with a complete word.
			if strings.HasSuffix(strings.TrimRight(chunk, ".!?"), "wor") {
				t.Errorf("chunk %d ends mid-word: %q", i, tail(chunk, 20))
			}
		}

		if rejoined := strings.Join(got, " "); utf8.RuneCountInString(rejoined) < utf8.RuneCountInString(in)-2*len(got) {
			t.Errorf("splitting must not drop content: rejoined %d runes vs input %d",
				utf8.RuneCountInString(rejoined), utf8.RuneCountInString(in))
		}
	})

	t.Run("footer_lands_only_on_last_chunk", func(t *testing.T) {
		t.Parallel()

		url := "https://app.orako.io/conversations/abc"
		body := service.FormatQuestion(strings.Repeat("Sentence body here. ", 300), "", url)

		got := service.SplitForDelivery(body, 2000)
		if len(got) < 2 {
			t.Fatalf("expected a split, got %d chunk(s)", len(got))
		}

		for i, chunk := range got[:len(got)-1] {
			if strings.Contains(chunk, url) {
				t.Errorf("chunk %d (non-last) must not carry the answer link", i)
			}
		}

		if !strings.Contains(got[len(got)-1], url) {
			t.Error("the LAST chunk must carry the answer link")
		}
	})

	t.Run("multibyte_counted_by_rune", func(t *testing.T) {
		t.Parallel()

		in := strings.Repeat("éé ", 1500) // 4500 runes, 7500 bytes

		for i, chunk := range service.SplitForDelivery(in, 2000) {
			if n := utf8.RuneCountInString(chunk); n > 2000 {
				t.Errorf("chunk %d rune count = %d, want <= 2000", i, n)
			}
		}
	})

	t.Run("giant_single_word_hard_splits", func(t *testing.T) {
		t.Parallel()

		got := service.SplitForDelivery(strings.Repeat("x", 5000), 2000)
		if len(got) != 3 {
			t.Fatalf("5000 unbreakable runes at cap 2000 = 3 chunks, got %d", len(got))
		}

		for i, chunk := range got {
			if n := utf8.RuneCountInString(chunk); n > 2000 {
				t.Errorf("chunk %d rune count = %d, want <= 2000", i, n)
			}
		}
	})
}

// TestSendSplit proves the sequential chunk sender: the LAST chunk's ref is
// returned, a first-chunk failure is an error (nothing delivered), and a
// later-chunk failure keeps the delivered prefix without erroring.
func TestSendSplit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("returns_last_ref", func(t *testing.T) {
		t.Parallel()

		var sent []string

		ref, err := service.SendSplit(ctx, []string{"a", "b", "c"}, func(_ context.Context, chunk string) (service.MessageRef, error) {
			sent = append(sent, chunk)
			return service.MessageRef{ChannelID: "ch", MessageID: chunk}, nil
		})
		if err != nil {
			t.Fatalf("SendSplit: %v", err)
		}

		if len(sent) != 3 || ref.MessageID != "c" {
			t.Errorf("sent %v, last ref %q; want all three chunks and ref of the last", sent, ref.MessageID)
		}
	})

	t.Run("first_chunk_failure_is_an_error", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("boom")

		_, err := service.SendSplit(ctx, []string{"a", "b"}, func(context.Context, string) (service.MessageRef, error) {
			return service.MessageRef{}, wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Errorf("err = %v, want %v", err, wantErr)
		}
	})

	t.Run("later_chunk_failure_keeps_prefix", func(t *testing.T) {
		t.Parallel()

		calls := 0

		ref, err := service.SendSplit(ctx, []string{"a", "b", "c"}, func(_ context.Context, chunk string) (service.MessageRef, error) {
			calls++
			if calls == 2 {
				return service.MessageRef{}, errors.New("hiccup")
			}

			return service.MessageRef{ChannelID: "ch", MessageID: chunk}, nil
		})
		if err != nil {
			t.Fatalf("a past-first failure must not error: %v", err)
		}

		if calls != 2 || ref.MessageID != "a" {
			t.Errorf("calls=%d ref=%q; want stop after the failed 2nd chunk, keeping the 1st chunk's ref", calls, ref.MessageID)
		}
	})
}

// tail returns the final n runes of s, for readable failure messages.
func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[len(r)-n:])
}
