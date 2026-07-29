// SPDX-License-Identifier: Apache-2.0

package errs

// statusConflict is the transport-agnostic status for a concurrency conflict.
const statusConflict = 409

// ConflictError reports a concurrency conflict that the caller may retry.
// It carries no wrapped cause because the retry advice is the actionable
// detail; the underlying driver error must be logged by the adapter before
// it is translated.
type ConflictError struct{}

// Error implements the error interface.
func (e ConflictError) Error() string {
	return "conflict: concurrent write, retry the request"
}

// Slug returns the stable identifier for a conflict failure.
func (e ConflictError) Slug() string {
	return "conflict"
}

// Status returns the transport-agnostic status for a conflict failure.
func (e ConflictError) Status() int {
	return statusConflict
}

// Detail returns a human-readable explanation of the failure.
func (e ConflictError) Detail() string {
	return "a concurrency conflict occurred; retry the request"
}
