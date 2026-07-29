// SPDX-License-Identifier: AGPL-3.0-or-later

package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/orako-io/core/internal/application/domain/model"
	"github.com/orako-io/core/internal/pkg/errs"
)

// TestLengthCaps proves the rune-length caps reject over-length free text on
// each constructor and on the context validator, and accept text at the cap.
func TestLengthCaps(t *testing.T) {
	t.Parallel()

	t.Run("question over cap rejected", func(t *testing.T) {
		t.Parallel()

		_, err := model.NewConversation(uuid.New(), uuid.New(), uuid.New(), strings.Repeat("q", model.MaxQuestionRunes+1))

		var inv errs.InvalidError
		if !errors.As(err, &inv) {
			t.Fatalf("want InvalidError, got %v", err)
		}
	})

	t.Run("question at cap accepted", func(t *testing.T) {
		t.Parallel()

		if _, err := model.NewConversation(uuid.New(), uuid.New(), uuid.New(), strings.Repeat("q", model.MaxQuestionRunes)); err != nil {
			t.Fatalf("at-cap question rejected: %v", err)
		}
	})

	t.Run("message body over cap rejected", func(t *testing.T) {
		t.Parallel()

		_, err := model.NewMessage(uuid.New(), uuid.New(), uuid.New(), model.MessageRoleFollowUp, strings.Repeat("m", model.MaxMessageRunes+1), model.MessageSourceHuman)

		var inv errs.InvalidError
		if !errors.As(err, &inv) {
			t.Fatalf("want InvalidError, got %v", err)
		}
	})

	t.Run("context over cap rejected", func(t *testing.T) {
		t.Parallel()

		var inv errs.InvalidError
		if err := model.ValidateContextLen(strings.Repeat("c", model.MaxContextRunes+1)); !errors.As(err, &inv) {
			t.Fatalf("want InvalidError, got %v", err)
		}

		if err := model.ValidateContextLen(strings.Repeat("c", model.MaxContextRunes)); err != nil {
			t.Fatalf("at-cap context rejected: %v", err)
		}
	})
}
