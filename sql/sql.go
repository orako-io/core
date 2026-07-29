// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sql embeds Orako's database migrations and applies them at boot.
//
// Migrations under migrations/ are the schema of record; sqlc reads the same
// directory to generate the typed query layer. RunMigrations is idempotent and
// runs automatically when the server starts, so there is no manual migrate step
// in development.
package sql

import (
	stdsql "database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver.
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations applies every pending migration to the database at dsn.
//
// It opens its own short-lived database/sql connection (golang-migrate is built
// on database/sql, not pgxpool) and drives the embedded migration set forward.
// A no-op run — when the schema is already current — is not an error.
func RunMigrations(dsn string) error {
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("loading embedded migrations: %w", err)
	}

	db, err := stdsql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("opening migration connection: %w", err)
	}

	defer func() { _ = db.Close() }()

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("building migration driver: %w", err)
	}

	migrator, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("building migrator: %w", err)
	}

	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("applying migrations: %w", err)
	}

	return nil
}
