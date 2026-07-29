// SPDX-License-Identifier: Apache-2.0

package errs

// statusNotFound is the transport-agnostic status for a missing resource.
const statusNotFound = 404

// NotFoundError reports that a requested resource does not exist.
type NotFoundError struct {
	// Resource names the kind of entity that was not found.
	Resource string
}

// Error implements the error interface.
func (e NotFoundError) Error() string {
	return "not found: " + e.Resource
}

// Slug returns the stable identifier for a not-found failure.
func (e NotFoundError) Slug() string {
	return "not_found"
}

// Status returns the transport-agnostic status for a not-found failure.
func (e NotFoundError) Status() int {
	return statusNotFound
}

// Detail returns a human-readable explanation of the failure.
func (e NotFoundError) Detail() string {
	return "the requested resource was not found"
}
