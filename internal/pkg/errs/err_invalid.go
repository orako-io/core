// SPDX-License-Identifier: Apache-2.0

package errs

import "fmt"

// statusInvalid is the transport-agnostic status for a validation failure.
const statusInvalid = 400

// InvalidError reports that a value failed an invariant check.
//
// Domain constructors and mutations return it when an input violates a business
// rule, naming the offending field and the reason it was rejected.
type InvalidError struct {
	// Field is the name of the value that failed validation.
	Field string
	// Reason explains why the value was rejected.
	Reason string
}

// Error implements the error interface.
func (e InvalidError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Reason)
}

// Slug returns the stable identifier for a validation failure.
func (e InvalidError) Slug() string {
	return "invalid_input"
}

// Status returns the transport-agnostic status for a validation failure.
func (e InvalidError) Status() int {
	return statusInvalid
}

// Detail returns a human-readable explanation of the failure.
func (e InvalidError) Detail() string {
	return e.Error()
}
