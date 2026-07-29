// SPDX-License-Identifier: AGPL-3.0-or-later

// Package model holds Orako's domain entities and their business rules.
//
// Post-pivot (2026-07-01) the bounded context is async agent<->human
// conversations. It is the innermost layer: it depends
// on nothing in the project except internal/pkg/errs. It must never import
// adapters, infra, command, query, or gen packages.
//
// Entities are constructed through NewX validators and mutated only through
// methods that re-check their invariants.
package model
