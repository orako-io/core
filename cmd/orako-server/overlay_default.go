//go:build !saas

// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/infra/api"
)

func overlayManaged() bool { return false }

func buildOverlayRoutes(_ context.Context, _ string, _ *pgxpool.Pool, _ api.Authenticator, _ service.Mailer, _ *slog.Logger) overlayRoutes {
	return nil
}

// buildOverlayExtensions returns no SaaS extensions in the community build: no
// billing seat gate and no org-created (trial) hook.
func buildOverlayExtensions(_ *pgxpool.Pool) (command.SeatGate, command.OrgCreatedHook) {
	return nil, nil
}

// runOverlayMigrations is a no-op in the community build: there is no billing schema.
func runOverlayMigrations(_ string) error { return nil }
