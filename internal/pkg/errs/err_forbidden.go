// SPDX-License-Identifier: Apache-2.0

package errs

// statusForbidden is the transport-agnostic status for an authorization denial.
const statusForbidden = 403

// ForbiddenError reports that the caller is authenticated but not permitted to
// perform the action or view the resource. The transport maps it to a
// permission-denied status; it must never carry the identity of the resource
// (that would leak existence) beyond the action name.
type ForbiddenError struct {
	// Action names the operation the caller was denied (e.g. "view conversation").
	Action string
}

// Error implements the error interface.
func (e ForbiddenError) Error() string {
	return "forbidden: " + e.Action
}

// Slug returns the stable identifier for an authorization failure.
func (e ForbiddenError) Slug() string {
	return "forbidden"
}

// Status returns the transport-agnostic status for an authorization failure.
func (e ForbiddenError) Status() int {
	return statusForbidden
}

// Detail returns a human-readable explanation of the failure.
func (e ForbiddenError) Detail() string {
	return "the caller is not permitted to perform this action"
}
