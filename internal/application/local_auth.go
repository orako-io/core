// SPDX-License-Identifier: AGPL-3.0-or-later

package application

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/pkg/postgres"
)

// LocalAuthHandlers groups the self-hosted authentication use cases.
type LocalAuthHandlers struct {
	Login        command.LoginHandler
	AcceptInvite command.AcceptInviteHandler
	Reset        command.ResetHandler
}

// LocalAuthConfig configures self-hosted authentication.
type LocalAuthConfig struct {
	AdminEmail    string
	AdminPassword string
	Secret        string
	Issuer        string
	Audience      string
	BaseURL       string
	SessionTTL    time.Duration
	ResetTTL      time.Duration
}

// NewLocalAuthHandlers seeds the initial admin and wires local authentication.
func NewLocalAuthHandlers(
	ctx context.Context,
	pool *pgxpool.Pool,
	mailer service.Mailer,
	createOrganization func(context.Context, string, uuid.UUID) error,
	cfg LocalAuthConfig,
	logger *slog.Logger,
) (LocalAuthHandlers, bool, error) {
	accounts := identity.NewAccountStore(pool)
	txor := postgres.NewTransactor(pool)

	created, err := command.SeedAdmin(ctx, accounts, txor, createOrganization, cfg.AdminEmail, cfg.AdminPassword)
	if err != nil {
		return LocalAuthHandlers{}, false, err
	}

	return LocalAuthHandlers{
		Login:        command.NewLoginHandler(accounts, cfg.Secret, cfg.Issuer, cfg.Audience, cfg.SessionTTL),
		AcceptInvite: command.NewAcceptInviteHandler(accounts, txor, cfg.Secret),
		Reset:        command.NewResetHandler(accounts, mailer, cfg.Secret, cfg.BaseURL, cfg.ResetTTL, logger),
	}, created, nil
}
