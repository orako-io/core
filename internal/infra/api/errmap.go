// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"

	"connectrpc.com/connect"

	"github.com/orako-io/core/internal/pkg/errs"
)

// toConnectError translates a domain error to a connect.Error with the
// appropriate status code. Unknown errors become CodeInternal.
//
// Mapping table:
//
//	errs.InvalidError    → CodeInvalidArgument
//	errs.NotFoundError   → CodeNotFound
//	errs.ForbiddenError  → CodePermissionDenied
//	errs.DuplicateError  → CodeAlreadyExists
//	errs.ConflictError   → CodeFailedPrecondition
//	errs.InternalError   → CodeInternal
//	everything else      → CodeInternal
func toConnectError(err error) error {
	if err == nil {
		return nil
	}

	var inv errs.InvalidError
	if errors.As(err, &inv) {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	var nf errs.NotFoundError
	if errors.As(err, &nf) {
		return connect.NewError(connect.CodeNotFound, err)
	}

	var forb errs.ForbiddenError
	if errors.As(err, &forb) {
		return connect.NewError(connect.CodePermissionDenied, err)
	}

	var dup errs.DuplicateError
	if errors.As(err, &dup) {
		return connect.NewError(connect.CodeAlreadyExists, err)
	}

	var con errs.ConflictError
	if errors.As(err, &con) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}

	var intern errs.InternalError
	if errors.As(err, &intern) {
		return connect.NewError(connect.CodeInternal, errors.New(intern.Detail()))
	}

	return connect.NewError(connect.CodeInternal, err)
}
