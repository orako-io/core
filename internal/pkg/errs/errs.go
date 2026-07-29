// SPDX-License-Identifier: Apache-2.0

// Package errs defines the typed-error contract shared across Orako.
//
// Adapters return sentinel errors, the application layer wraps them into the
// typed errors declared here, and driving adapters render them. Each error type
// implements [Problem] so it can be projected to a transport-specific shape
// (for example an RFC-7807 problem document) without leaking internal details.
package errs

// Problem is the contract every user-facing error in Orako satisfies.
//
// It exposes a stable, machine-readable classification of a failure so that
// driving adapters can render it consistently regardless of the underlying
// cause.
type Problem interface {
	error
	// Slug returns a stable, machine-readable identifier for the problem.
	Slug() string
	// Status returns the transport-agnostic status code for the problem.
	Status() int
	// Detail returns a human-readable explanation of the problem.
	Detail() string
}
