// SPDX-License-Identifier: AGPL-3.0-or-later

// Package repository declares the write-side repository ports (interfaces)
// that driven adapters implement.
//
// Post-pivot (2026-07-01) the ports model projects, members, conversations,
// messages, and connection presence. The room- and
// collision-era ports have been removed.
//
// Ports are owned by the domain; their implementations live under
// internal/adapters. This package depends only on
// internal/application/domain/model.
package repository
