// SPDX-License-Identifier: Apache-2.0

// Package config loads Orako's runtime configuration from environment
// variables (12-factor), with development defaults.
package config

import (
	"cmp"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/orako-io/core/internal/pkg/errs"
	"github.com/orako-io/core/internal/pkg/license"
)

// Default configuration values used when an environment variable is unset.
const (
	defaultHTTPPort        = "8080"
	defaultLogLevel        = "info"
	defaultPostgresHost    = "localhost"
	defaultPostgresPort    = "5432"
	defaultPostgresUser    = "orako"
	defaultPostgresDB      = "orako"
	defaultPostgresSSLMode = "disable"
	defaultSMTPPort        = "587"
	defaultAuthMode        = "dev"
	defaultS3Region        = "us-east-1"
	defaultAttachmentMax   = 25 << 20 // 25 MiB (Discord free per-file limit)
)

// Config holds the resolved runtime configuration for an Orako process.
type Config struct {
	// HTTPPort is the port the server listens on.
	HTTPPort string
	// LogLevel is the minimum slog level to emit (debug, info, warn, error).
	LogLevel string
	// PostgresDSN is the app pool's connection string: a full URL
	// (ORAKO_DATABASE_URL / DATABASE_URL) or assembled from POSTGRES_* parts.
	PostgresDSN string
	// MigrateDSN is the migrations connection string (ORAKO_MIGRATE_DATABASE_URL,
	// default PostgresDSN). Must be a direct/session connection when the app pool
	// goes through a transaction pooler (e.g. Supabase).
	MigrateDSN string `exhaustruct:"optional"`
	// PGTransactionPooler (ORAKO_PG_TX_POOLER) makes pgx pooler-safe
	// (unnamed prepared statements) behind a transaction-mode pooler.
	PGTransactionPooler bool `exhaustruct:"optional"`

	// BetterStackToken enables the hosted Better Stack (Logtail) log sink when
	// set (ORAKO_BETTERSTACK_SOURCE_TOKEN); empty = stdout-only. Setting/unsetting
	// it is the swap switch for the vendor — the app never depends on it.
	BetterStackToken string `exhaustruct:"optional"`
	// BetterStackEndpoint is the source's ingesting host
	// (ORAKO_BETTERSTACK_INGEST_HOST, e.g. https://sXXXX.<region>.betterstackdata.com);
	// empty falls back to the library default. Only used with BetterStackToken.
	BetterStackEndpoint string `exhaustruct:"optional"`

	// SlackClientID is the Slack app's OAuth client ID (ORAKO_SLACK_CLIENT_ID).
	// Empty disables the OAuth install flow.
	SlackClientID string
	// SlackClientSecret is the Slack OAuth client secret (ORAKO_SLACK_CLIENT_SECRET).
	SlackClientSecret string
	// SlackSigningSecret verifies inbound Slack webhooks (ORAKO_SLACK_SIGNING_SECRET).
	SlackSigningSecret string
	// BaseURL is the server's externally reachable base URL (ORAKO_BASE_URL),
	// used to construct OAuth redirect URIs.
	BaseURL string
	// TrustedProxyCIDRs is the comma-separated allowlist of reverse-proxy
	// networks whose forwarding headers may define the client IP.
	TrustedProxyCIDRs string `exhaustruct:"optional"`

	// LicenseRefreshURL is where the background edition loop POSTs the instance's
	// current key to pull a freshly-minted one, so a renewal lifts the caps without
	// re-pasting. Defaults to Orako's baked hosted endpoint (license.DefaultRefreshURL)
	// so a paid self-host renews with no config; ORAKO_LICENSE_REFRESH_URL overrides
	// the target. Empty (via ORAKO_LICENSE_OFFLINE=true) means pure offline — the
	// loop only re-checks the local key's expiry and never contacts Orako.
	LicenseRefreshURL string `exhaustruct:"optional"`

	// SMTPHost is the SMTP server hostname (ORAKO_SMTP_HOST). Empty disables email.
	SMTPHost string `exhaustruct:"optional"`
	// SMTPPort is the SMTP server port (ORAKO_SMTP_PORT), default 587 (STARTTLS).
	SMTPPort string `exhaustruct:"optional"`
	// SMTPUsername is the SMTP auth username (ORAKO_SMTP_USERNAME).
	SMTPUsername string `exhaustruct:"optional"`
	// SMTPPassword is the SMTP auth password (ORAKO_SMTP_PASSWORD).
	SMTPPassword string `exhaustruct:"optional"`
	// SMTPFrom is the From header (ORAKO_SMTP_FROM), e.g. "Orako <no-reply@…>".
	SMTPFrom string `exhaustruct:"optional"`

	// S3 object storage for attachments (files/images on conversation
	// messages). ORAKO_S3_ENDPOINT empty disables attachments entirely (the
	// no-op blob store): uploads and inbound file ingestion degrade off with a
	// log line, like SMTP. Works with any S3-compatible API — the value
	// is the FULL base URL including scheme and any path:
	//   AWS       https://s3.<region>.amazonaws.com
	//   Supabase  https://<project_ref>.supabase.co/storage/v1/s3
	//   MinIO     http://localhost:9000
	S3Endpoint string `exhaustruct:"optional"`
	// S3Region is the bucket region (ORAKO_S3_REGION), default "us-east-1".
	S3Region string `exhaustruct:"optional"`
	// S3Bucket is the attachments bucket (ORAKO_S3_BUCKET).
	S3Bucket string `exhaustruct:"optional"`
	// S3AccessKey / S3SecretKey are the S3 credentials
	// (ORAKO_S3_ACCESS_KEY / ORAKO_S3_SECRET_KEY).
	S3AccessKey string `exhaustruct:"optional"`
	S3SecretKey string `exhaustruct:"optional"`
	// AttachmentMaxBytes caps a single upload (ORAKO_ATTACHMENT_MAX_BYTES),
	// default 25 MiB (Discord's free per-file limit).
	AttachmentMaxBytes int64 `exhaustruct:"optional"`

	// AuthMode selects caller authentication (ORAKO_AUTH_MODE): "dev" (default,
	// unsigned stub — LOCAL DEVELOPMENT ONLY), "oidc" (JWKS verification), or
	// "local" (HS256 shared secret). The JWT proves *who*; roles always come
	// from Orako's RBAC tables, never the token.
	AuthMode string `exhaustruct:"optional"`
	// AuthIssuer is the expected JWT issuer / OIDC discovery URL
	// (ORAKO_AUTH_ISSUER). Required for AuthMode "oidc".
	AuthIssuer string `exhaustruct:"optional"`
	// AuthAudience is the expected JWT audience (ORAKO_AUTH_AUDIENCE); enforced when set.
	AuthAudience string `exhaustruct:"optional"`
	// AuthHS256Secret is the shared secret for AuthMode "local" (ORAKO_AUTH_HS256_SECRET).
	AuthHS256Secret string `exhaustruct:"optional"`
	// AdminEmail / AdminPassword seed the first local-auth admin on boot (local
	// mode only): if no account exists yet, an owner account + org is created so
	// a fresh self-host has someone to log in as. ORAKO_ADMIN_EMAIL /
	// ORAKO_ADMIN_PASSWORD.
	AdminEmail    string `exhaustruct:"optional"`
	AdminPassword string `exhaustruct:"optional"`

	// SupabaseServiceKey is the Supabase service_role key
	// (ORAKO_SUPABASE_SERVICE_KEY). Optional; when set (with an OIDC issuer)
	// invitation emails carry a signed action link that authenticates the
	// recipient directly instead of the tokenless signup URL.
	SupabaseServiceKey string `exhaustruct:"optional"`

	// DebugEndpoints mounts the unauthenticated /debug/* introspection routes
	// (ORAKO_DEBUG_ENDPOINTS=1). Off by default so a deployed server exposes no
	// debug surface — even the low-value gateway counts are opt-in.
	DebugEndpoints bool `exhaustruct:"optional"`
	// TelegramWebhookSecret authenticates the generic Telegram inbound webhook:
	// it must equal the secret_token passed to Telegram's setWebhook, which
	// Telegram then echoes in X-Telegram-Bot-Api-Secret-Token on every update
	// (ORAKO_TELEGRAM_WEBHOOK_SECRET). Empty (the default) means Telegram inbound
	// is disabled — the route rejects every request rather than trusting an
	// unsigned payload.
	TelegramWebhookSecret string `exhaustruct:"optional"`
}

// SupabaseBaseURL derives the Supabase project base URL from the OIDC issuer
// (https://<project>.supabase.co/auth/v1 → https://<project>.supabase.co).
// Empty when no issuer is configured or it is not a Supabase issuer shape.
func (c Config) SupabaseBaseURL() string {
	issuer := strings.TrimRight(c.AuthIssuer, "/")
	base := strings.TrimSuffix(issuer, "/auth/v1")

	if base == issuer || base == "" {
		return ""
	}

	return base
}

// Load resolves configuration from getenv (an [os.Getenv]-shaped function),
// applying defaults for unset keys.
func Load(getenv func(string) string) (Config, error) {
	// A full connection URL takes precedence over assembled POSTGRES_* parts.
	dsn := orDefault(getenv("ORAKO_DATABASE_URL"), getenv("DATABASE_URL"))
	if dsn == "" {
		host := orDefault(getenv("POSTGRES_HOST"), defaultPostgresHost)
		port := orDefault(getenv("POSTGRES_PORT"), defaultPostgresPort)
		user := orDefault(getenv("POSTGRES_USER"), defaultPostgresUser)
		password := getenv("POSTGRES_PASSWORD")
		dbName := orDefault(getenv("POSTGRES_DB"), defaultPostgresDB)
		sslmode := orDefault(getenv("POSTGRES_SSLMODE"), defaultPostgresSSLMode)
		dsn = postgresDSN(host, port, user, password, dbName, sslmode)
	}

	// License refresh defaults to Orako's baked hosted endpoint, so a paid
	// self-host renews transparently with no config. ORAKO_LICENSE_OFFLINE=true
	// forces pure offline (air-gapped: never contacts Orako); an explicit
	// ORAKO_LICENSE_REFRESH_URL overrides the target. Only a licensed instance
	// ever calls it — a Community (keyless) instance never phones home.
	licenseRefreshURL := cmp.Or(getenv("ORAKO_LICENSE_REFRESH_URL"), license.DefaultRefreshURL)
	if isTrue(getenv("ORAKO_LICENSE_OFFLINE")) {
		licenseRefreshURL = ""
	}

	conf := Config{
		HTTPPort:              orDefault(getenv("ORAKO_HTTP_PORT"), defaultHTTPPort),
		LogLevel:              orDefault(getenv("LOG_LEVEL"), defaultLogLevel),
		PostgresDSN:           dsn,
		MigrateDSN:            orDefault(getenv("ORAKO_MIGRATE_DATABASE_URL"), dsn),
		PGTransactionPooler:   isTrue(getenv("ORAKO_PG_TX_POOLER")),
		BetterStackToken:      getenv("ORAKO_BETTERSTACK_SOURCE_TOKEN"),
		BetterStackEndpoint:   getenv("ORAKO_BETTERSTACK_INGEST_HOST"),
		SlackClientID:         getenv("ORAKO_SLACK_CLIENT_ID"),
		SlackClientSecret:     getenv("ORAKO_SLACK_CLIENT_SECRET"),
		SlackSigningSecret:    getenv("ORAKO_SLACK_SIGNING_SECRET"),
		BaseURL:               getenv("ORAKO_BASE_URL"),
		TrustedProxyCIDRs:     getenv("ORAKO_TRUSTED_PROXY_CIDRS"),
		LicenseRefreshURL:     licenseRefreshURL,
		SMTPHost:              getenv("ORAKO_SMTP_HOST"),
		SMTPPort:              orDefault(getenv("ORAKO_SMTP_PORT"), defaultSMTPPort),
		SMTPUsername:          getenv("ORAKO_SMTP_USERNAME"),
		SMTPPassword:          getenv("ORAKO_SMTP_PASSWORD"),
		SMTPFrom:              getenv("ORAKO_SMTP_FROM"),
		AuthMode:              orDefault(getenv("ORAKO_AUTH_MODE"), defaultAuthMode),
		AuthIssuer:            getenv("ORAKO_AUTH_ISSUER"),
		AuthAudience:          getenv("ORAKO_AUTH_AUDIENCE"),
		AuthHS256Secret:       getenv("ORAKO_AUTH_HS256_SECRET"),
		AdminEmail:            getenv("ORAKO_ADMIN_EMAIL"),
		AdminPassword:         getenv("ORAKO_ADMIN_PASSWORD"),
		SupabaseServiceKey:    getenv("ORAKO_SUPABASE_SERVICE_KEY"),
		TelegramWebhookSecret: getenv("ORAKO_TELEGRAM_WEBHOOK_SECRET"),
		DebugEndpoints:        isTrue(getenv("ORAKO_DEBUG_ENDPOINTS")),
		S3Endpoint:            getenv("ORAKO_S3_ENDPOINT"),
		S3Region:              orDefault(getenv("ORAKO_S3_REGION"), defaultS3Region),
		S3Bucket:              getenv("ORAKO_S3_BUCKET"),
		S3AccessKey:           getenv("ORAKO_S3_ACCESS_KEY"),
		S3SecretKey:           getenv("ORAKO_S3_SECRET_KEY"),
		AttachmentMaxBytes:    int64OrDefault(getenv("ORAKO_ATTACHMENT_MAX_BYTES"), defaultAttachmentMax),
	}

	if conf.PostgresDSN == "" {
		return Config{}, errs.InvalidError{Field: "POSTGRES_*", Reason: "incomplete Postgres configuration"}
	}

	return conf, nil
}

// postgresDSN assembles a libpq connection string from its components.
func postgresDSN(host, port, user, password, dbName, sslmode string) string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s/%s?sslmode=%s",
		user, password, net.JoinHostPort(host, port), dbName, sslmode,
	)
}

// isTrue reports whether an env value is a truthy flag ("1", "true", "yes").
// int64OrDefault parses v as an int64, falling back to def on empty/invalid.
func int64OrDefault(v string, def int64) int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 {
		return n
	}

	return def
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// orDefault returns value when non-empty, otherwise fallback.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
