// SPDX-License-Identifier: AGPL-3.0-or-later

package query

import (
	"errors"

	adaptererr "github.com/orako-io/core/internal/adapters/errors"
	"github.com/orako-io/core/internal/pkg/errs"
)

func translateReadError(err error, resource string) error {
	if errors.Is(err, adaptererr.ErrNotFound) {
		return errs.NotFoundError{Resource: resource}
	}

	return errs.InternalError{Err: err}
}
