// SPDX-License-Identifier: Apache-2.0

package errs

// statusInternal is the transport-agnostic status for an unexpected failure.
const statusInternal = 500

// InternalError wraps an unexpected failure that is not attributable to caller
// input.
//
// It carries the underlying cause for logging while exposing a generic detail
// to the caller.
type InternalError struct {
	// Err is the wrapped underlying cause.
	Err error
}

// Error implements the error interface.
func (e InternalError) Error() string {
	if e.Err == nil {
		return "internal error"
	}

	return "internal error: " + e.Err.Error()
}

// Unwrap exposes the wrapped cause for use with errors.Is and errors.As.
func (e InternalError) Unwrap() error {
	return e.Err
}

// Slug returns the stable identifier for an internal failure.
func (e InternalError) Slug() string {
	return "internal_error"
}

// Status returns the transport-agnostic status for an internal failure.
func (e InternalError) Status() int {
	return statusInternal
}

// Detail returns a generic, caller-safe explanation of the failure.
func (e InternalError) Detail() string {
	return "an unexpected error occurred"
}
