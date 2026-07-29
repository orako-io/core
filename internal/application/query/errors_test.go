// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"errors"
	"testing"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/pkg/errs"
)

func TestTranslateReadError(t *testing.T) {
	t.Parallel()

	var notFound errs.NotFoundError
	if err := translateReadError(adaptererr.ErrNotFound, "member"); !errors.As(err, &notFound) {
		t.Fatalf("not found: got %T, want errs.NotFoundError", err)
	}

	var internal errs.InternalError
	if err := translateReadError(errors.New("database unavailable"), "member"); !errors.As(err, &internal) {
		t.Fatalf("internal: got %T, want errs.InternalError", err)
	}
}
