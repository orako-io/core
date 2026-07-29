// SPDX-License-Identifier: AGPL-3.0-or-later

// Command orako-server is the Orako collaboration server: it loads config,
// connects to Postgres, wires the application composition root, and serves
// HTTP until interrupted.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/orako-io/core/internal/adapters/blobstore"
	"github.com/orako-io/core/internal/adapters/conversation"
	"github.com/orako-io/core/internal/adapters/identity"
	"github.com/orako-io/core/internal/adapters/integration"
	"github.com/orako-io/core/internal/adapters/invitelink"
	"github.com/orako-io/core/internal/adapters/licensing"
	"github.com/orako-io/core/internal/adapters/mail"
	"github.com/orako-io/core/internal/adapters/provider"
	"github.com/orako-io/core/internal/adapters/provider/discord"
	"github.com/orako-io/core/internal/application"
	"github.com/orako-io/core/internal/application/command"
	"github.com/orako-io/core/internal/application/query"
	"github.com/orako-io/core/internal/application/service"
	"github.com/orako-io/core/internal/infra/api"
	discordhttp "github.com/orako-io/core/internal/infra/api/discord"
	"github.com/orako-io/core/internal/infra/api/gatewaymgr"
	"github.com/orako-io/core/internal/infra/api/inbound"
	"github.com/orako-io/core/internal/infra/api/oauth"
	slackhttp "github.com/orako-io/core/internal/infra/api/slack"
	teamshttp "github.com/orako-io/core/internal/infra/api/teams"
	"github.com/orako-io/core/internal/infra/mcp"
	"github.com/orako-io/core/internal/infra/webui"
	"github.com/orako-io/core/internal/pkg/auth"
	"github.com/orako-io/core/internal/pkg/config"
	"github.com/orako-io/core/internal/pkg/edition"
	"github.com/orako-io/core/internal/pkg/license"
	"github.com/orako-io/core/internal/pkg/logger"
	pkgpostgres "github.com/orako-io/core/internal/pkg/postgres"
	"github.com/orako-io/core/internal/pkg/secretbox"
	"github.com/orako-io/core/internal/pkg/server"
	orakosql "github.com/orako-io/core/sql"
)

func main() {
	if err := run(); err != nil {
		slog.Error("orako-server exited", slog.Any("error", err))
		os.Exit(1)
	}
}

// run holds the full server lifecycle so that deferred cleanup runs before
// main translates a failure into a non-zero exit code.
//
//nolint:funlen,gocyclo // linear composition-root wiring; splitting it would only scatter the lifecycle
func run() error {
	conf, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	log := logger.NewWithBetterStack(os.Stdout, conf.LogLevel, logger.BetterStack{Token: conf.BetterStackToken, Endpoint: conf.BetterStackEndpoint})
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var connectOpts []pkgpostgres.Option
	if conf.PGTransactionPooler {
		connectOpts = append(connectOpts, pkgpostgres.WithTransactionPoolerCompat())
	}

	pool, err := pkgpostgres.Connect(ctx, conf.PostgresDSN, connectOpts...)
	if err != nil {
		return err
	}

	defer pool.Close()

	log.InfoContext(ctx, "connected to postgres")

	if err = orakosql.RunMigrations(conf.MigrateDSN); err != nil {
		return err
	}

	if err = runOverlayMigrations(conf.MigrateDSN); err != nil {
		return err
	}

	log.InfoContext(ctx, "database migrations applied")

	credCipher, err := secretbox.NewFromEnv()
	if err != nil {
		return fmt.Errorf("building credential cipher: %w", err)
	}

	switch {
	case credCipher.Enabled():
		log.InfoContext(ctx, "integration credential encryption enabled")
	case conf.AuthMode != "dev":
		return fmt.Errorf("%s is required outside development mode", secretbox.EnvKey)
	default:
		log.WarnContext(ctx, "integration credential encryption disabled in development")
	}

	providerStore := integration.NewProjectProviderStore(pool, credCipher)
	memberStore := identity.NewMemberStore(pool)
	providerMessageStore := integration.NewProviderMessageStore(pool)
	projectStore := identity.NewProjectStore(pool)

	orgProviderStore := integration.NewOrgProviderStore(pool, credCipher)
	orgProviderLoader := integration.NewOrgResolvingProviderLoader(projectStore, orgProviderStore)
	joinTokenStore := identity.NewJoinTokenStore(pool)

	reg, err := provider.New(nil, provider.Deps{
		Members:          memberStore,
		Conversations:    conversation.NewStore(pool),
		Ledger:           providerMessageStore,
		Webhooks:         conversation.NewSurfaceStore(pool),
		DashboardBaseURL: conf.BaseURL,
	}, orgProviderLoader)
	if err != nil {
		return err
	}

	if err = reg.HydrateFrom(ctx, providerStore); err != nil {
		log.WarnContext(ctx, "failed to hydrate provider registry from DB; providers will load on demand via read-through",
			slog.Any("error", err))
	} else {
		log.InfoContext(ctx, "provider registry hydrated from DB")
	}

	mailer := selectMailer(conf, log)

	blobs := selectBlobStore(ctx, conf, log)

	inviteLinks := selectInviteLinks(ctx, conf, log)

	licenseStore := licensing.NewLicenseStore(pool)

	managedEdition := overlayManaged()
	ed := resolveEdition(ctx, licenseStore, managedEdition, log)

	liveEdition := edition.NewLive(ed)

	saasGate, saasOrgHook := buildOverlayExtensions(pool)

	app, err := application.New(pool, reg, mailer, inviteLinks, blobs, credCipher, conf.BaseURL, liveEdition, saasGate, saasOrgHook, log)
	if err != nil {
		return err
	}

	defer func() {
		if cerr := app.Close(); cerr != nil {
			log.ErrorContext(ctx, "closing application", slog.Any("error", cerr))
		}
	}()

	runtimeCtx, stopRuntime := context.WithCancel(ctx)

	var runtimeWorkers sync.WaitGroup

	defer func() {
		stopRuntime()
		runtimeWorkers.Wait()
	}()

	discordGateway, gatewaySupervisor := setupDiscordGateway(runtimeCtx, reg, providerStore, orgProviderStore, memberStore, providerMessageStore, conversation.NewSurfaceStore(pool), app, conf, log)

	defer func() {
		discordGateway.Close()

		if cerr := gatewaySupervisor.Close(); cerr != nil {
			log.ErrorContext(ctx, "closing discord gateway supervisor", slog.Any("error", cerr))
		}
	}()

	teamsWebhook := setupTeamsWebhook(runtimeCtx, reg, providerStore, app, log)

	routerErr := make(chan error, 1)

	runtimeWorkers.Go(func() {
		routerRunErr := app.RunEvents(runtimeCtx)
		if routerRunErr == nil && runtimeCtx.Err() == nil {
			routerRunErr = errors.New("event router stopped unexpectedly")
		}

		routerErr <- routerRunErr

		stopRuntime()
	})

	select {
	case routerRunErr := <-routerErr:
		if routerRunErr == nil {
			return errors.New("event router stopped before becoming ready")
		}

		return routerRunErr
	case <-app.Running():
		log.InfoContext(runtimeCtx, "event router ready")
	case <-runtimeCtx.Done():
		return runtimeCtx.Err()
	}

	runtimeWorkers.Go(func() {
		app.Escalation.Run(runtimeCtx)
	})

	runtimeWorkers.Go(func() {
		refreshEditionLoop(runtimeCtx, liveEdition, licenseStore, conf, managedEdition, log)
	})

	authenticator, err := buildAuthenticator(runtimeCtx, conf, pool, log)
	if err != nil {
		return err
	}

	humanAuth := api.NewHumanAuthenticatorAdapter(authenticator, memberStore, memberStore, identity.NewAccountStore(pool))

	oauthServer := buildOAuthServer(conf, pool, humanAuth, log)
	if oauthServer != nil {
		authenticator = wrapWithMCPTokens(pool, oauthServer.ResourceURL(), authenticator, log)
	}

	mcpHTTPServer := buildMCPHTTPServer(app, pool, oauthServer, conf.AttachmentMaxBytes, log)

	mcpConnections := oauth.NewStore(pool)

	events := api.NewEventsSSEHandler(authenticator, app.Events.Subscriber(), identity.NewProjectStore(pool), log)

	overlayHandler := buildOverlayRoutes(runtimeCtx, conf.BaseURL, pool, authenticator, mailer, log)

	localAuth, err := setupLocalAuth(runtimeCtx, conf, pool, mailer, app, log)
	if err != nil {
		return err
	}

	serverErr := server.Run(runtimeCtx, log, ":"+conf.HTTPPort, buildMux(runtimeCtx, app, reg, projectStore, memberStore, conversation.NewStore(pool).WithBlobDeleter(blobs), orgProviderLoader, providerStore, joinTokenStore, buildActiveOrgScoper(pool), teamsWebhook, authenticator, humanAuth, events, oauthServer, mcpHTTPServer, mcpConnections, gatewaySupervisor, overlayHandler, localAuth, liveEdition, licenseStore, managedEdition, conf, log))

	stopRuntime()

	routerRunErr := <-routerErr

	return errors.Join(serverErr, routerRunErr)
}

// setupDiscordGateway builds the Discord gateway (handler lifecycle) and its
// supervisor (session lifecycle), syncs the supervisor against every
// already-configured discord project (boot-time, independent of registry
// hydration timing), and installs the registry's register hook so a later
// ConfigureProvider call opens/replaces a session too. The caller owns
// shutdown (Gateway.Close then Supervisor.Close) via defer.
func setupDiscordGateway(
	ctx context.Context,
	reg *provider.Registry,
	providerStore *integration.ProjectProviderStore,
	orgProviderStore *integration.OrgProviderStore,
	memberStore *identity.MemberStore,
	providerMessageStore *integration.ProviderMessageStore,
	surfaceStore *conversation.SurfaceStore,
	app *application.App,
	conf config.Config,
	log *slog.Logger,
) (*discord.Gateway, *gatewaymgr.Supervisor) {
	discordGateway := discord.NewGateway(memberStore, providerMessageStore, surfaceStore, app.Commands.FollowUp, buildInboundIngestor(app, conf, log), log)
	sup := gatewaymgr.NewSupervisor(gatewaymgr.NewDiscordSessionFactory(discordGateway), log)

	if err := sup.SyncFromStore(ctx, providerStore); err != nil {
		log.WarnContext(ctx, "discord gateway: project-level boot sync failed", slog.Any("error", err))
	}

	if err := sup.SyncFromStore(ctx, orgProviderRowLoader{store: orgProviderStore}); err != nil {
		log.WarnContext(ctx, "discord gateway: org-level boot sync failed; sessions will open on the next ConfigureProvider call",
			slog.Any("error", err))
	} else {
		log.InfoContext(ctx, "discord gateway sessions synced from org providers")
	}

	reg.SetRegisterHook(sup.RegisterFromMap)

	return discordGateway, sup
}

// orgProviderRowLoader adapts the org-level provider store to the supervisor's
// boot-sync loader: org connections have no single project, so the OrgID keys
// the session's reference slot (the session itself is keyed by bot token, one
// per distinct token, so this stays consistent with the per-project register
// hook — both reference the same token-keyed session).
type orgProviderRowLoader struct {
	store *integration.OrgProviderStore
}

func (l orgProviderRowLoader) LoadAllProviders(ctx context.Context) ([]service.ProviderRow, error) {
	rows, err := l.store.LoadAllProviders(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]service.ProviderRow, len(rows))
	for i, r := range rows {
		out[i] = service.ProviderRow{ProjectID: r.OrgID, Kind: r.Kind, Credentials: r.Credentials}
	}

	return out, nil
}

// setupTeamsWebhook builds the Teams inbound webhook handler. Discovery
// (fetching the Bot Framework's OpenID metadata) is a network call; a
// failure here is logged and returns nil rather than failing server startup
// — buildMux simply does not mount the route in that case.
func setupTeamsWebhook(
	ctx context.Context,
	reg *provider.Registry,
	providerStore *integration.ProjectProviderStore,
	app *application.App,
	log *slog.Logger,
) *teamshttp.WebhookHandler {
	teamsWebhook, err := teamshttp.NewWebhookHandler(ctx, "", reg, providerStore, app.Commands.FollowUp, log)
	if err != nil {
		log.WarnContext(ctx, "teams webhook unavailable: Bot Framework OpenID metadata discovery failed; the /teams/activities route will not be mounted",
			slog.Any("error", err))

		return nil
	}

	return teamsWebhook
}

// buildAuthenticator selects the auth strategy from config: "dev" (unsigned
// stub, local development only), "local" (HS256 shared secret), or "oidc"
// (issuer JWKS). JWT modes resolve the caller's role from Orako's RBAC tables,
// never the token.
// requireJWTIssuerAudience hardens the signed auth modes, failing closed: both
// the issuer (the trust anchor) and the audience are mandatory. An empty
// audience would disable the aud check, so any validly-signed token from the
// issuer — including one minted for a different app sharing the signing key —
// would be accepted; refusing to boot without it is safer than that.
func requireJWTIssuerAudience(conf config.Config) error {
	if conf.AuthIssuer == "" {
		return errors.New("ORAKO_AUTH_ISSUER is required for auth mode oidc/local")
	}

	if conf.AuthAudience == "" {
		return errors.New("ORAKO_AUTH_AUDIENCE is required for auth mode oidc/local: without it the JWT audience check is disabled and any validly-signed token from the issuer is accepted")
	}

	return nil
}

func buildAuthenticator(ctx context.Context, conf config.Config, pool *pgxpool.Pool, log *slog.Logger) (api.Authenticator, error) {
	switch conf.AuthMode {
	case "dev", "":
		// Fail closed by default (M4): dev accepts UNSIGNED tokens that let any
		// caller assert org-admin of any org, so it must be opted into EXPLICITLY —
		// not merely inferred from an unset ORAKO_BASE_URL. A deployment that
		// forgets ORAKO_AUTH_MODE now refuses to boot instead of silently serving
		// the bypass. ORAKO_BASE_URL being set is an additional hard stop.
		if conf.BaseURL != "" {
			return nil, errors.New("ORAKO_AUTH_MODE is dev/unset but ORAKO_BASE_URL is set: dev mode accepts UNSIGNED tokens and must not run in a deployed environment — set ORAKO_AUTH_MODE=oidc or local")
		}

		if os.Getenv("ORAKO_ALLOW_INSECURE_DEV_AUTH") != "1" {
			return nil, errors.New("ORAKO_AUTH_MODE is dev/unset: the dev stub accepts UNSIGNED tokens (any caller can assert org-admin). Set ORAKO_AUTH_MODE=oidc or local for a real deployment, or ORAKO_ALLOW_INSECURE_DEV_AUTH=1 to explicitly allow the insecure dev stub for local development")
		}

		log.WarnContext(ctx, "AUTH MODE = dev: accepting UNSIGNED stub tokens — never use this in a deployed environment")

		return api.DevAuthenticator{}, nil

	case "local":
		if err := requireJWTIssuerAudience(conf); err != nil {
			return nil, err
		}

		verifier, err := auth.NewHS256Verifier(conf.AuthHS256Secret, conf.AuthIssuer, conf.AuthAudience)
		if err != nil {
			return nil, err
		}

		log.InfoContext(ctx, "auth mode = local (HS256 shared secret)")

		return api.NewJWTAuthenticator(verifier, principalResolver(pool)), nil

	case "oidc":
		if err := requireJWTIssuerAudience(conf); err != nil {
			return nil, err
		}

		verifier, err := auth.NewOIDCVerifier(ctx, conf.AuthIssuer, conf.AuthAudience)
		if err != nil {
			return nil, err
		}

		log.InfoContext(ctx, "auth mode = oidc", slog.String("issuer", conf.AuthIssuer))

		return api.NewJWTAuthenticator(verifier, principalResolver(pool)), nil

	default:
		return nil, fmt.Errorf("unknown ORAKO_AUTH_MODE %q (want dev, local, or oidc)", conf.AuthMode)
	}
}

// buildActiveOrgScoper builds the multi-org switch: honoring the Orako-Org-Id
// header to move the caller's active org among those they participate in. One
// store serves both the project-in-org lookup and the org-member role check.
func buildActiveOrgScoper(pool *pgxpool.Pool) api.ActiveOrgScoper {
	orgStore := identity.NewOrganizationStore(pool)

	return api.NewDBActiveOrgScoper(orgStore, orgStore, identity.NewProjectStore(pool))
}

// principalResolver builds the DB-backed resolver that maps a verified identity
// to a Orako member and their table-defined role.
func principalResolver(pool *pgxpool.Pool) api.PrincipalResolver {
	// One member store satisfies both reader ports (by email + by account id).
	members := identity.NewMemberStore(pool)

	return api.NewDBPrincipalResolver(
		identity.NewAccountStore(pool),
		members,
		members,
		identity.NewProjectStore(pool),
		identity.NewOrganizationStore(pool),
	)
}

// buildInboundIngestor builds the shared inbound-attachment ingestor used by
// every inbound transport (the generic webhook and the Discord gateway): it
// downloads a human reply's photos/documents and stores them through the same
// upload path an agent uses. Bounded by ORAKO_ATTACHMENT_MAX_BYTES. When object
// storage is off, UploadAttachment rejects and ingestion degrades to text-only.
func buildInboundIngestor(app *application.App, conf config.Config, log *slog.Logger) *inbound.Ingestor {
	// Guarded client: inbound attachment URLs are provider-derived and untrusted,
	// so block SSRF to private/link-local addresses at dial time.
	return inbound.NewIngestor(app.Commands.UploadAttachment, inbound.NewGuardedClient(http.DefaultClient), conf.AttachmentMaxBytes, log)
}

// selectBlobStore builds the attachment byte store. When ORAKO_S3_ENDPOINT is
// unset, or the S3 connection fails, it returns the no-op store: attachment
// upload and inbound file ingestion degrade off with a log line (same posture
// as SMTP), and the rest of the server is unaffected.
func selectBlobStore(ctx context.Context, conf config.Config, log *slog.Logger) service.BlobStore {
	if conf.S3Endpoint == "" || conf.S3Bucket == "" {
		log.Info("S3 object storage not configured; attachments disabled (using no-op blob store)")

		return blobstore.Noop{}
	}

	store, err := blobstore.New(ctx, blobstore.Config{
		Endpoint:  conf.S3Endpoint,
		Region:    conf.S3Region,
		Bucket:    conf.S3Bucket,
		AccessKey: conf.S3AccessKey,
		SecretKey: conf.S3SecretKey,
	})
	if err != nil {
		log.Warn("S3 object storage misconfigured; attachments disabled (using no-op blob store)",
			slog.Any("error", err))

		return blobstore.Noop{}
	}

	log.Info("S3 object storage configured; attachments enabled",
		slog.String("bucket", conf.S3Bucket))

	return store
}

// selectMailer builds the transactional email adapter. When ORAKO_SMTP_HOST is
// unset (or the config is incomplete) it returns a no-op mailer so the server
// runs without email; notifications are logged instead of sent.
// resolveEdition determines the product edition at boot and logs it. The license
// key comes from the DB (instance_license), set in the dashboard — NOT an env
// var. A malformed/expired license does not unlock anything: Resolve returns the
// Community edition plus an error, logged as a warning. The resolved limits are
// enforced only under the Community edition (see application.buildMemberGate).
func resolveEdition(ctx context.Context, store *licensing.LicenseStore, managed bool, log *slog.Logger) edition.Edition {
	// Fail-safe: a DB read error at boot must NEVER crash or hard-gate the server.
	// Get returns "" on error, so we fall through to Community with a warning.
	key, _, _, _, err := store.Get(ctx)
	if err != nil {
		log.WarnContext(ctx, "could not read the stored license key; running as community edition", slog.String("error", err.Error()))
	}

	ed, err := edition.Resolve(managed, key, license.DefaultPublicKeyB64)
	if err != nil {
		log.WarnContext(ctx, "license verification failed; running as community edition", slog.String("error", err.Error()))
	}

	log.InfoContext(ctx, "edition resolved",
		slog.String("edition", ed.Kind.String()),
		slog.Int("max_members", ed.Limits.MaxMembers),
		slog.Int("max_orgs", ed.Limits.MaxOrgs),
		slog.Int("max_projects", ed.Limits.MaxProjects),
	)

	return ed
}

// editionRefreshInterval is how often the self-host edition loop re-resolves the
// license. A grace period (see billing.MintForSubscription) covers the window
// between a renewal and the next tick, so hours — not minutes — is fine and keeps
// the refresh endpoint from being polled hard.
const editionRefreshInterval = 6 * time.Hour

// refreshEditionLoop re-resolves the edition on a ticker and Stores the result in
// live, so the gates and /api/edition reflect a runtime license change without a
// restart. The current key is sourced from the DB (instance_license) each tick.
// It is a no-op in a managed deployment: that edition is owned by the private
// overlay, not an offline key. When conf.LicenseRefreshURL is set it first
// tries to pull a freshly-minted key (a renewal lifts the caps) and PERSISTS it
// back to the store so the renewal survives a restart; otherwise it just
// re-evaluates the stored key's expiry (a lapse re-applies the caps).
func refreshEditionLoop(ctx context.Context, live *edition.Live, store *licensing.LicenseStore, conf config.Config, managed bool, log *slog.Logger) {
	if managed {
		return
	}

	ticker := time.NewTicker(editionRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshEditionOnce(ctx, live, store, conf, log)
		}
	}
}

// refreshEditionOnce runs one refresh tick: read the stored key, optionally
// renew+persist it, re-resolve the edition, and Store it live. All failures are
// non-fatal (fail-safe): the current edition is kept and a warning logged.
func refreshEditionOnce(ctx context.Context, live *edition.Live, store *licensing.LicenseStore, conf config.Config, log *slog.Logger) {
	key, _, _, found, err := store.Get(ctx)
	if err != nil {
		log.WarnContext(ctx, "could not read the stored license key; keeping the current edition", slog.String("error", err.Error()))

		return
	}

	if !found || key == "" {
		return // Community: no key to refresh.
	}

	key = maybeRenewLicense(ctx, store, conf, key, log)

	ed, err := edition.Resolve(false, key, license.DefaultPublicKeyB64)
	if err != nil {
		log.WarnContext(ctx, "edition re-resolve failed; reverting to community", slog.String("error", err.Error()))
	}

	prev := live.Current()
	live.Store(ed)

	if ed.Kind != prev.Kind {
		log.InfoContext(ctx, "edition changed at runtime",
			slog.String("from", prev.Kind.String()),
			slog.String("to", ed.Kind.String()),
		)
	}
}

// maybeRenewLicense POSTs the current key to the refresh endpoint and, on a fresh
// key, PERSISTS it to the store (setBy=nil: no human setter for an auto-renewal)
// so the renewal survives a restart. It returns the (possibly updated) key; on
// any failure it returns the input key unchanged — the key's own expiry governs.
// A no-op when no refresh URL is configured (ORAKO_LICENSE_OFFLINE).
func maybeRenewLicense(ctx context.Context, store *licensing.LicenseStore, conf config.Config, key string, log *slog.Logger) string {
	if conf.LicenseRefreshURL == "" {
		return key
	}

	fresh, err := fetchLicenseKey(ctx, conf.LicenseRefreshURL, key)
	if err != nil {
		log.WarnContext(ctx, "license refresh fetch failed; keeping current key", slog.String("error", err.Error()))

		return key
	}

	if fresh == "" || fresh == key {
		return key
	}

	if err := store.Set(ctx, fresh, uuid.Nil); err != nil {
		log.WarnContext(ctx, "persisting the refreshed license failed; keeping current key", slog.String("error", err.Error()))

		return key
	}

	log.InfoContext(ctx, "license auto-renewed from refresh endpoint")

	return fresh
}

// fetchLicenseKey POSTs the instance's current (possibly-expired) key to the SaaS
// refresh endpoint, which verifies the signature, looks up the customer's active
// subscription, and re-mints. It returns the new key, "" if the endpoint has no
// active subscription for this instance (204 — the key is left to expire), or an
// error on transport/HTTP failure.
func fetchLicenseKey(ctx context.Context, url, currentKey string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	reqBody, _ := json.Marshal(struct {
		Key string `json:"key"`
	}{Key: currentKey})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return "", nil // No active subscription: let the current key expire.
	case http.StatusOK:
		var out struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return "", fmt.Errorf("decode refresh response: %w", err)
		}

		return out.Key, nil
	default:
		return "", fmt.Errorf("license refresh: unexpected status %d", resp.StatusCode)
	}
}

// localSessionTTL is the lifetime of a self-host local-login session token.
// There is no refresh flow yet, so it is generous; shorten once refresh lands.
const localSessionTTL = 7 * 24 * time.Hour

// inviteTTL is how long a self-host invite link stays valid.
const inviteTTL = 7 * 24 * time.Hour

// resetTTL is how long a self-host password-reset link stays valid. Shorter than
// an invite: a reset is a security-sensitive, immediate action.
const resetTTL = time.Hour

// setupLocalAuth wires the self-host email+password auth. It returns a nil
// handler (nothing mounted) unless ORAKO_AUTH_MODE=local, and seeds the first
// admin from ORAKO_ADMIN_EMAIL/PASSWORD on boot.
func setupLocalAuth(ctx context.Context, conf config.Config, pool *pgxpool.Pool, mailer service.Mailer, app *application.App, log *slog.Logger) (*api.LocalAuthHandler, error) {
	if conf.AuthMode != "local" {
		return nil, nil
	}

	accounts := identity.NewAccountStore(pool)

	createOrg := func(ctx context.Context, orgName string, ownerID uuid.UUID) error {
		_, err := app.Commands.CreateOrganization.Handle(ctx, command.CreateOrganizationCommand{
			Name:             orgName,
			CreatorAccountID: ownerID,
		})

		return err
	}

	created, err := command.SeedAdmin(ctx, accounts, createOrg, conf.AdminEmail, conf.AdminPassword)
	if err != nil {
		return nil, err
	}

	if created {
		log.InfoContext(ctx, "seeded first local-auth admin from ORAKO_ADMIN_EMAIL")
	}

	login := command.NewLoginHandler(accounts, conf.AuthHS256Secret, conf.AuthIssuer, conf.AuthAudience, localSessionTTL)
	accept := command.NewAcceptInviteHandler(accounts, conf.AuthHS256Secret)
	reset := command.NewResetHandler(accounts, mailer, conf.AuthHS256Secret, conf.BaseURL, resetTTL, log)

	return api.NewLocalAuthHandler(login, accept, reset), nil
}

func selectMailer(conf config.Config, log *slog.Logger) service.Mailer {
	if conf.SMTPHost == "" {
		log.Info("SMTP not configured; email notifications disabled (using no-op mailer)")

		return mail.NewNoop(log)
	}

	mailer, err := mail.NewSMTP(mail.Config{
		Host:     conf.SMTPHost,
		Port:     conf.SMTPPort,
		Username: conf.SMTPUsername,
		Password: conf.SMTPPassword,
		From:     conf.SMTPFrom,
	})
	if err != nil {
		log.Warn("SMTP misconfigured; falling back to no-op mailer", slog.Any("error", err))

		return mail.NewNoop(log)
	}

	log.Info("SMTP mailer initialised", slog.String("smtp_host", conf.SMTPHost))

	return mailer
}

// buildOAuthServer constructs Orako's thin MCP Authorization Server, gated on
// ORAKO_BASE_URL: absolute issuer/resource URLs can't be built without one, so
// a deployment that hasn't set it simply runs without the remote MCP OAuth
// surface (nil disables route mounting and mcp-token authentication).
//
// In dev auth mode there is no real upstream login to reuse, so the
// dashboard SPA's /authorize page (AuthorizePage.tsx) renders an explanatory
// "not available" page client-side instead of the consent screen, and the
// authenticated approve endpoint rejects the same way server-side (defense
// in depth) — the sanctioned dev-mode posture (document as unsupported,
// never fake an identity, never crash). Discovery, DCR, and the token
// endpoint stay available in every mode.
func buildOAuthServer(conf config.Config, pool *pgxpool.Pool, humanAuth api.HumanAuthenticatorAdapter, log *slog.Logger) *oauth.Server {
	if conf.BaseURL == "" {
		log.Info("ORAKO_BASE_URL not set; remote MCP OAuth (thin authorization server) is disabled")

		return nil
	}

	var unsupported string

	if conf.AuthMode == "dev" || conf.AuthMode == "" {
		unsupported = "This server runs in ORAKO_AUTH_MODE=dev, which has no real sign-in to reuse for remote MCP OAuth. " +
			"Set ORAKO_AUTH_MODE to oidc or local to enable remote MCP connections."
	}

	srv := oauth.NewServer(conf.BaseURL, oauth.NewStore(pool), humanAuth, unsupported)

	log.Info("remote MCP OAuth authorization server mounted", slog.String("resource", srv.ResourceURL()))

	return srv
}

// wrapWithMCPTokens layers Orako-issued `mcp_at_…` bearer support (minted by
// the thin OAuth AS) over base: an mcp_at_ token authenticates the same as
// every other caller, resolving CallerIdentity fresh from the RBAC tables.
func wrapWithMCPTokens(pool *pgxpool.Pool, resourceURL string, base api.Authenticator, log *slog.Logger) api.Authenticator {
	return api.NewMCPTokenAuthenticator(
		oauth.NewStore(pool),
		identity.NewMemberStore(pool),
		identity.NewProjectStore(pool),
		identity.NewOrganizationStore(pool),
		resourceURL,
		base,
		log,
	)
}

// buildMCPHTTPServer constructs the server-hosted MCP-over-HTTP resource
// (phase 3): the same tool set as the CLI's stdio server, running in-process
// against the application layer. nil alongside oauthServer (see
// buildOAuthServer's ORAKO_BASE_URL gate) — no base URL means no absolute
// resource/PRM URLs to mint tokens against or challenge clients with.
//
// Its MCPTokenAuthenticator is wired with mcp.RejectingAuthenticator as
// the fallback — deliberately NOT the dev/jwt chain wrapWithMCPTokens uses
// for the dashboard Connect-RPC surface. A raw Supabase JWT must never
// authenticate at /mcp; only mcp_at_ tokens issued by Orako's own thin OAuth
// AS do. This is the confused-deputy guard the MCP Authorization spec
// requires, proven by
// api.TestMCPTokenRawJWTRejectedWithRejectingFallback.
func buildMCPHTTPServer(app *application.App, pool *pgxpool.Pool, oauthServer *oauth.Server, maxAttachmentBytes int64, log *slog.Logger) *mcp.Server {
	if oauthServer == nil {
		return nil
	}

	mcpAuth := api.NewMCPTokenAuthenticator(
		oauth.NewStore(pool),
		identity.NewMemberStore(pool),
		identity.NewProjectStore(pool),
		identity.NewOrganizationStore(pool),
		oauthServer.ResourceURL(),
		mcp.RejectingAuthenticator{},
		log,
	)

	return mcp.NewServer(
		app.Queries.SearchHistory,
		app.Queries.HistoryVocabulary,
		app.Queries.ListExperts,
		app.Commands.Ask,
		app.Queries.GetConversation,
		app.Commands.FollowUp,
		app.Commands.UploadAttachment,
		app.Commands.ResolveConversation,
		app.Queries.ListProjects,
		app.Queries.ListConversations,
		app.Commands.AddParticipant,
		app.Commands.CreateKnowledgeEntry,
		app.Commands.SuggestKnowledgeEntry,
		maxAttachmentBytes,
		mcpAuth,
		oauthServer.PRMURL(),
		log,
	)
}

// selectInviteLinks builds the Supabase admin action-link generator when both
// the service key and a Supabase-shaped OIDC issuer are configured. nil is a
// valid result: invitation emails then carry the tokenless signup link.
func selectInviteLinks(ctx context.Context, conf config.Config, log *slog.Logger) service.InviteLinkGenerator {
	if conf.AuthMode == "local" {
		log.InfoContext(ctx, "invitation links use self-host invite tokens (local auth)")

		return invitelink.NewLocal(conf.AuthHS256Secret, inviteTTL)
	}

	base := conf.SupabaseBaseURL()

	if conf.SupabaseServiceKey == "" || base == "" {
		log.InfoContext(ctx, "supabase admin not configured; invitation emails use the tokenless signup link")

		return nil
	}

	log.InfoContext(ctx, "invitation action links enabled (supabase admin)", slog.String("supabase_url", base))

	return invitelink.NewSupabase(base, conf.SupabaseServiceKey)
}

// buildMux constructs the HTTP mux: Connect-RPC service (with auth
// interceptor), inbound webhooks, Slack handlers, and the embedded dashboard.
//
// maxRequestBodyBytes caps every inbound request body. The largest legitimate
// body is an ask (question ≤10K chars + a ≤100K-char context packet
// ≈ a few hundred KB with markup); 4 MiB leaves generous headroom while closing
// the unbounded-body resource-exhaustion vector on the authenticated MCP/RPC
// surface. Provider webhooks additionally cap themselves tighter (1 MiB).
const maxRequestBodyBytes int64 = 4 << 20

// maxBodyBytes wraps each request body in http.MaxBytesReader so an over-size
// body is rejected with 413 instead of being read into memory in full.
func maxBodyBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// securityHeaders sets baseline response security headers on every route (M5).
// HSTS forces HTTPS (defeats SSL stripping); nosniff blocks MIME sniffing;
// frame-ancestors 'none' + X-Frame-Options block clickjacking of the dashboard
// (which holds the bearer token). The CSP is deliberately permissive on
// connect/img (Supabase auth, S3 presigned URLs, data URIs) to avoid breaking the
// SPA while still pinning the framing and base-uri.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: https:; font-src 'self' data:; connect-src 'self' https:; " +
		"frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

//nolint:funlen // straight-line HTTP wiring: routes + middleware, no branching to extract
func buildMux(
	ctx context.Context,
	app *application.App,
	reg *provider.Registry,
	projects *identity.ProjectStore,
	members *identity.MemberStore,
	conversations *conversation.Store,
	providerCreds *integration.OrgResolvingProviderLoader,
	providerStore *integration.ProjectProviderStore,
	joinTokens *identity.JoinTokenStore,
	activeOrg api.ActiveOrgScoper,
	teamsWebhook *teamshttp.WebhookHandler,
	authenticator api.Authenticator,
	humanAuth api.HumanAuthenticatorAdapter,
	events http.Handler,
	oauthServer *oauth.Server,
	mcpHTTPServer *mcp.Server,
	mcpConnections *oauth.Store,
	gatewaySupervisor *gatewaymgr.Supervisor,
	overlayHandler overlayRoutes,
	localAuth *api.LocalAuthHandler,
	live *edition.Live,
	licenseStore *licensing.LicenseStore,
	managedEdition bool,
	conf config.Config,
	log *slog.Logger,
) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(trustedProxyRealIP(conf.TrustedProxyCIDRs, log))
	r.Use(middleware.Recoverer) // a panic in any handler returns 500 instead of crashing the server
	r.Use(securityHeaders)      // M5: HSTS, nosniff, frame-ancestors, referrer/permissions policy
	r.Use(maxBodyBytes(maxRequestBodyBytes))
	r.Use(slogRequestLogger(log))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ctx.Err() != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)

			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	licenseHandler := api.NewLicenseHandler(live, licenseStore, authenticator, license.DefaultPublicKeyB64, managedEdition, log)
	licenseHandler.RegisterRoutes(r)

	onboardingHandler := api.NewOnboardingHandler(members, authenticator, log)
	onboardingHandler.RegisterRoutes(r)

	if conf.DebugEndpoints {
		r.Get("/debug/gateway", func(w http.ResponseWriter, _ *http.Request) {
			sessions, projects := gatewaySupervisor.Status()

			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"discord_sessions":%d,"projects":%d}`, sessions, projects)
		})
	}

	machineTokens := api.NewMachineTokenGateway(mcpConnections, conf.BaseURL+"/mcp")
	createMachineToken := command.MustNewCreateMachineTokenHandler(machineTokens, projects)
	listMachineTokens := query.MustNewListMachineTokensHandler(machineTokens)
	revokeMachineToken := command.MustNewRevokeMachineTokenHandler(mcpConnections)

	svc := api.NewServer(
		app.Queries.ListExperts,
		app.Commands.Ask,
		app.Queries.GetConversation,
		app.Commands.FollowUp,
		app.Commands.ResolveConversation,
		app.Commands.DismissConversation,
		app.Queries.ListProjects,
		app.Commands.RenameProject,
		app.Commands.SetProjectArchived,
		app.Commands.DeleteProject,
		app.Commands.DeleteConversation,
		app.Queries.ListProjectsDetailed,
		app.Queries.ListConversations,
		app.Queries.SearchHistory,
		app.Queries.HistoryStatusCounts,
		app.Queries.ListKnowledgeEntries,
		app.Commands.CreateKnowledgeEntry,
		app.Commands.UpdateKnowledgeEntry,
		app.Commands.MarkKnowledgeStale,
		app.Commands.RevalidateKnowledgeEntry,
		app.Queries.ListPendingKnowledge,
		app.Commands.ApproveKnowledgeEntry,
		app.Commands.DismissKnowledgeEntry,
		app.Commands.PromoteConversationToKnowledge,
		app.Queries.GetDashboardMetrics,
		app.Queries.ListInbox,
		app.Queries.GetMember,
		app.Commands.UpdateMember,
		app.Queries.ListMembers,
		app.Queries.GetOrgMember,
		app.Commands.SetMemberAvailability,
		app.Commands.SetMemberActivation,
		app.Commands.SetOrgAdmin,
		app.Queries.ListConnectedChannels,
		providerCreds,
		app.Queries.GetProviderAlertChannels,
		mcpConnections,
		mcpConnections,
		app.Commands.Heartbeat,
		app.Commands.CreateOrganization,
		app.Commands.CreateProject,
		app.Commands.AddMember,
		app.Commands.InviteMembers,
		app.Commands.AssignRole,
		app.Commands.SetOwnExpertise,
		app.Commands.RemoveMember,
		app.Commands.ConfigureProvider,
		app.Commands.SyncChatBindings,
		app.Commands.DisconnectProvider,
		app.Commands.SendProviderTest,
		app.Queries.GetOrganizationSettings,
		app.Commands.UpdateOrganizationSettings,
		app.Commands.RenameOrganization,
		app.Commands.DeleteOrganization,
		app.Queries.GetOrganization,
		app.Queries.ListOrganizations,
		app.Commands.GenerateJoinCode,
		app.Queries.GetJoinCode,
		app.Commands.RevokeJoinCode,
		createMachineToken,
		listMachineTokens,
		revokeMachineToken,
		projects,
		conversations,
		log,
	)

	path, handler := svc.Handler(connect.WithInterceptors(api.NewAuthInterceptor(authenticator, activeOrg)))
	r.Handle(path+"*", handler)

	webhookHandler := api.NewWebhookHandler(
		reg,
		app.Commands.FollowUp,
		conf.TelegramWebhookSecret,
		buildInboundIngestor(app, conf, log),
		log,
	)
	webhookHandler.RegisterRoutes(r)

	if teamsWebhook != nil {
		teamsWebhook.RegisterRoutes(r)
	}

	slackWebhook := slackhttp.NewWebhookHandler(providerCreds, conf.SlackSigningSecret, reg, app.Commands.FollowUp, log)
	slackWebhook.RegisterRoutes(r)

	if oauthServer != nil {
		r.Group(func(r chi.Router) {
			r.Use(newIPRateLimiter(ctx, 5, 20).middleware)
			oauthServer.RegisterRoutes(r)
		})
	}

	if mcpHTTPServer != nil {
		r.Handle("/mcp", mcpHTTPServer.Handler())
	}

	joinHandler := api.NewJoinHandler(humanAuth, app.Commands.RedeemJoinToken, projects, app.Queries.GetOrganization, joinTokens, log)

	r.Group(func(r chi.Router) {
		r.Use(newIPRateLimiter(ctx, 5, 20).middleware)
		joinHandler.RegisterRoutes(r)
	})

	communityInvites := api.NewCommunityInvitesHandler(authenticator, providerCreds, log)
	communityInvites.RegisterRoutes(r)

	if conf.SlackClientID != "" && conf.SlackClientSecret != "" {
		oauthCfg := slackhttp.OAuthConfig{
			ClientID:             conf.SlackClientID,
			ClientSecret:         conf.SlackClientSecret,
			SigningSecret:        conf.SlackSigningSecret,
			BaseURL:              conf.BaseURL,
			InstallAuthenticator: &slackOAuthAuthAdapter{auth: authenticator},
			InstallAuthorizer:    &slackOAuthAuthorizerAdapter{projects: projects},
		}
		oauthHandler := slackhttp.NewOAuthHandler(oauthCfg, &oauthConfiguratorAdapter{
			handler:  app.Commands.ConfigureProvider,
			projects: projects,
		})
		oauthHandler.RegisterRoutes(r)
	}

	if conf.BaseURL != "" {
		discordOAuth := discordhttp.NewOAuthHandler(
			discordhttp.OAuthConfig{BaseURL: conf.BaseURL},
			&discordOAuthCredsAdapter{loader: providerCreds, providerStore: providerStore},
			&discordOAuthAuthAdapter{auth: authenticator},
			&discordBinderAdapter{handler: app.Commands.BindMemberChannel},
		)
		discordOAuth.RegisterRoutes(r)
	}

	if localAuth != nil {
		r.Group(func(r chi.Router) {
			r.Use(newIPRateLimiter(ctx, 1, 8).middleware)
			localAuth.RegisterRoutes(r)
		})
	}

	if overlayHandler != nil {
		overlayHandler.RegisterRoutes(r)
	}

	r.Get("/events/stream", events.ServeHTTP)

	r.Handle("/*", webui.Handler())

	return r
}

func slogRequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			next.ServeHTTP(ww, r)

			log.InfoContext(r.Context(), "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}

type oauthConfiguratorAdapter struct {
	handler  command.ConfigureProviderHandler
	projects *identity.ProjectStore
}

func (a *oauthConfiguratorAdapter) Configure(ctx context.Context, projectID uuid.UUID, kind string, creds map[string]string) error {
	project, err := a.projects.ByID(ctx, projectID)
	if err != nil {
		return err
	}

	_, err = a.handler.Handle(ctx, command.ConfigureProviderCommand{
		ProjectID:   projectID,
		OrgID:       project.OrgID,
		Kind:        kind,
		Credentials: creds,
	})

	return err
}

// discordOAuthCredsAdapter resolves a project's org Discord OAuth creds: the
// client id is derived from the stored bot token, the secret is the optional
// stored client_secret credential. It also exposes the raw bot token + the
// project's anchor (alert) channel so the OAuth callback can best-effort add the
// bound member to the org's Discord guild.
type discordOAuthCredsAdapter struct {
	loader        *integration.OrgResolvingProviderLoader
	providerStore *integration.ProjectProviderStore
}

func (a *discordOAuthCredsAdapter) DiscordOAuthCreds(ctx context.Context, projectID uuid.UUID) (string, string, error) {
	raw, err := a.loader.LoadProvider(ctx, projectID, "discord")
	if err != nil {
		return "", "", err
	}

	var creds struct {
		BotToken     string `json:"bot_token"`
		ClientSecret string `json:"client_secret"`
	}

	if err := json.Unmarshal(raw, &creds); err != nil {
		return "", "", err
	}

	return discordhttp.DeriveClientID(creds.BotToken), creds.ClientSecret, nil
}

// GuildJoinContext returns the org's Discord bot token and the project's
// configured Discord alert channel (the anchor the guild is derived from). A
// missing token or channel yields empty strings so the callback skips the
// auto-join — never an error that would fail the bind.
func (a *discordOAuthCredsAdapter) GuildJoinContext(ctx context.Context, projectID uuid.UUID) (string, string, error) {
	raw, err := a.loader.LoadProvider(ctx, projectID, "discord")
	if err != nil {
		return "", "", err
	}

	var creds struct {
		BotToken string `json:"bot_token"`
	}

	if err := json.Unmarshal(raw, &creds); err != nil {
		return "", "", err
	}

	// Anchor channel = the project's configured Discord alert channel; the guild
	// is looked up from it (GET /channels/{id}). No channel → no auto-join.
	anchorChannelID := ""

	if providers, perr := a.providerStore.ConfiguredProvidersWithAlertChannel(ctx, projectID); perr == nil {
		for _, p := range providers {
			if p.Kind == "discord" && len(p.AlertChannelIDs) > 0 {
				anchorChannelID = p.AlertChannelIDs[0]

				break
			}
		}
	}

	return creds.BotToken, anchorChannelID, nil
}

// discordOAuthAuthAdapter adapts the transport Authenticator to the Discord
// OAuth handler's member-resolving Authenticator.
type discordOAuthAuthAdapter struct {
	auth api.Authenticator
}

func (a *discordOAuthAuthAdapter) Authenticate(ctx context.Context, header string) (uuid.UUID, uuid.UUID, error) {
	id, err := a.auth.Authenticate(ctx, header)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}

	return id.MemberID, id.ProjectID, nil
}

type slackOAuthAuthAdapter struct {
	auth api.Authenticator
}

func (a *slackOAuthAuthAdapter) Authenticate(
	ctx context.Context,
	header string,
) (slackhttp.InstallPrincipal, error) {
	id, err := a.auth.Authenticate(ctx, header)
	if err != nil {
		return slackhttp.InstallPrincipal{}, err
	}

	return slackhttp.InstallPrincipal{
		AccountID:  id.AccountID,
		MemberID:   id.MemberID,
		OrgID:      id.OrgID,
		IsOrgAdmin: id.IsOrgAdmin,
	}, nil
}

type slackOAuthAuthorizerAdapter struct {
	projects *identity.ProjectStore
}

func (a *slackOAuthAuthorizerAdapter) Authorize(
	ctx context.Context,
	principal slackhttp.InstallPrincipal,
	requestedProjectID uuid.UUID,
) (slackhttp.InstallAuthorization, error) {
	if !principal.IsOrgAdmin {
		return slackhttp.InstallAuthorization{}, errors.New("slack install requires org admin")
	}

	project, err := a.projects.ByID(ctx, requestedProjectID)
	if err != nil {
		return slackhttp.InstallAuthorization{}, fmt.Errorf("loading Slack install project: %w", err)
	}

	if project.OrgID == uuid.Nil || project.OrgID != principal.OrgID {
		return slackhttp.InstallAuthorization{}, errors.New("slack install project is outside the active organization")
	}

	return slackhttp.InstallAuthorization{
		MemberID:  principal.MemberID,
		OrgID:     principal.OrgID,
		ProjectID: requestedProjectID,
	}, nil
}

// discordBinderAdapter satisfies the Discord OAuth handler's Binder via the
// BindMemberChannel command.
type discordBinderAdapter struct {
	handler command.BindMemberChannelHandler
}

func (a *discordBinderAdapter) BindDiscord(ctx context.Context, memberID uuid.UUID, discordUserID string) error {
	return a.handler.Handle(ctx, command.BindMemberChannelCommand{MemberID: memberID, Channel: "discord", ExternalID: discordUserID})
}
