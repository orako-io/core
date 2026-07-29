// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/orako-io/core/internal/pkg/errs"
)

func TestToConnectErrorMasksInternalCause(t *testing.T) {
	t.Parallel()

	got := toConnectError(errs.InternalError{Err: errors.New("relation oauth_tokens does not exist")})

	if connect.CodeOf(got) != connect.CodeInternal {
		t.Fatalf("code = %v, want CodeInternal", connect.CodeOf(got))
	}

	if strings.Contains(got.Error(), "oauth_tokens") {
		t.Fatalf("internal cause leaked to Connect client: %v", got)
	}

	if !strings.Contains(got.Error(), "an unexpected error occurred") {
		t.Fatalf("safe detail missing from Connect error: %v", got)
	}
}
