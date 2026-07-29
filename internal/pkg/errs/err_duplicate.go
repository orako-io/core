// SPDX-License-Identifier: Apache-2.0

package errs

// statusDuplicate is the transport-agnostic status for a uniqueness conflict.
const statusDuplicate = 409

// DuplicateError reports that an entity already exists and cannot be created
// again.
type DuplicateError struct {
	// Resource names the kind of entity that already exists.
	Resource string
	// Field, when known, names the conflicting attribute (e.g. "Discord user
	// ID") so the user knows WHAT is already taken — "already exists: member"
	// alone does not say whether it is the email, a chat binding, or the git
	// handle.
	Field string `exhaustruct:"optional"`
}

// Error implements the error interface.
func (e DuplicateError) Error() string {
	if e.Field != "" {
		return "already exists: " + e.Resource + " — this " + e.Field + " is already used by another " + e.Resource
	}

	return "already exists: " + e.Resource
}

// Slug returns the stable identifier for a duplicate failure.
func (e DuplicateError) Slug() string {
	return "duplicate"
}

// Status returns the transport-agnostic status for a duplicate failure.
func (e DuplicateError) Status() int {
	return statusDuplicate
}

// Detail returns a human-readable explanation of the failure.
func (e DuplicateError) Detail() string {
	return "the resource already exists"
}
