// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"strings"
	"testing"

	"github.com/orako-io/core/internal/pkg/config"
	"github.com/orako-io/core/internal/pkg/license"
)

func TestLoadAppliesDefaults(t *testing.T) {
	t.Parallel()

	conf, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if conf.HTTPPort != "8080" {
		t.Errorf("HTTPPort = %q, want 8080", conf.HTTPPort)
	}

	if !strings.Contains(conf.PostgresDSN, "localhost:5432") {
		t.Errorf("PostgresDSN = %q, want it to contain localhost:5432", conf.PostgresDSN)
	}
}

func TestLoadReadsEnv(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"ORAKO_HTTP_PORT":   "9090",
		"LOG_LEVEL":         "debug",
		"POSTGRES_HOST":     "orako_postgres",
		"POSTGRES_PORT":     "5433",
		"POSTGRES_USER":     "app",
		"POSTGRES_PASSWORD": "secret",
		"POSTGRES_DB":       "appdb",
	}

	conf, err := config.Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if conf.HTTPPort != "9090" {
		t.Errorf("HTTPPort = %q, want 9090", conf.HTTPPort)
	}

	if conf.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", conf.LogLevel)
	}

	if !strings.Contains(conf.PostgresDSN, "app:secret@orako_postgres:5433/appdb") {
		t.Errorf("PostgresDSN = %q, missing expected credentials/host", conf.PostgresDSN)
	}
}

func TestLoad_LicenseRefreshURL(t *testing.T) {
	t.Parallel()

	// Default: the baked hosted endpoint, so a paid self-host renews with no config.
	def, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if def.LicenseRefreshURL != license.DefaultRefreshURL {
		t.Errorf("default LicenseRefreshURL = %q, want the baked %q", def.LicenseRefreshURL, license.DefaultRefreshURL)
	}

	// Offline opt-out: air-gapped installs never contact Orako.
	env := map[string]string{"ORAKO_LICENSE_OFFLINE": "true"}
	off, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if off.LicenseRefreshURL != "" {
		t.Errorf("offline LicenseRefreshURL = %q, want empty", off.LicenseRefreshURL)
	}

	// Explicit override wins (and is not disabled unless OFFLINE is set).
	env = map[string]string{"ORAKO_LICENSE_REFRESH_URL": "https://self.example/refresh"}
	ovr, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ovr.LicenseRefreshURL != "https://self.example/refresh" {
		t.Errorf("override LicenseRefreshURL = %q, want the explicit URL", ovr.LicenseRefreshURL)
	}
}

func TestLoad_DatabaseURLOverride(t *testing.T) {
	t.Parallel()

	full := "postgres://u:p@db.supabase.co:6543/postgres?sslmode=require"
	env := map[string]string{
		"ORAKO_DATABASE_URL": full,
		"POSTGRES_HOST":      "ignored",
		"ORAKO_PG_TX_POOLER": "true",
	}

	conf, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if conf.PostgresDSN != full {
		t.Errorf("PostgresDSN = %q, want the full URL override", conf.PostgresDSN)
	}

	// Migrate DSN defaults to the app DSN when not overridden.
	if conf.MigrateDSN != full {
		t.Errorf("MigrateDSN = %q, want it to default to the app DSN", conf.MigrateDSN)
	}

	if !conf.PGTransactionPooler {
		t.Error("PGTransactionPooler = false, want true")
	}
}

func TestLoad_SeparateMigrateDSN(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"ORAKO_DATABASE_URL":         "postgres://u:p@pooler:6543/postgres",
		"ORAKO_MIGRATE_DATABASE_URL": "postgres://u:p@direct:5432/postgres",
	}

	conf, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !strings.Contains(conf.PostgresDSN, "pooler:6543") {
		t.Errorf("PostgresDSN = %q, want the pooler endpoint", conf.PostgresDSN)
	}

	if !strings.Contains(conf.MigrateDSN, "direct:5432") {
		t.Errorf("MigrateDSN = %q, want the direct/session endpoint", conf.MigrateDSN)
	}
}

func TestLoad_SSLModeFromEnv(t *testing.T) {
	t.Parallel()

	env := map[string]string{"POSTGRES_SSLMODE": "require"}

	conf, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !strings.Contains(conf.PostgresDSN, "sslmode=require") {
		t.Errorf("PostgresDSN = %q, want sslmode=require", conf.PostgresDSN)
	}
}
