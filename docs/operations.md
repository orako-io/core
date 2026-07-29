# Orako — Operations Runbook

Self-host orako on a single machine with Docker Compose. This document covers
dev vs prod, required environment variables, and the license verify seam.

---

## Dev stack vs prod stack

| | Dev | Prod |
|---|---|---|
| Compose file | `docker-compose.dev.yml` | `docker-compose.yml` |
| Server | Hot-reload via reflex behind lint/test gate | Compiled static binary |
| Task targets | `task up`, `task upb`, `task server` | `task prod-up`, `task prod-upb` |
| Search | Built-in (Postgres FTS + pg_trgm) | Built-in (Postgres FTS + pg_trgm) |
| Migrations | Auto on server boot | Auto on server boot |

---

## Quick start (dev)

```sh
# 1. Copy the example env file and fill in values
cp .env.example .env   # edit POSTGRES_PASSWORD at minimum

# 2. Start the dev stack (hot-reload)
task up
```

The dev stack starts stock `postgres:16`, then builds and runs the orako-server
with live reloading (events use a Postgres outbox — no message broker).
Migrations are applied on every boot via `sql.RunMigrations` (embedded
golang-migrate).

---

## Quick start (prod)

```sh
# 1. Set required environment variables
export POSTGRES_PASSWORD=changeme
export ORAKO_BASE_URL=https://orako.example.com   # needed for Slack OAuth

# 2. Start the production stack
task prod-up
```

On first boot the server applies embedded migrations (0001–0003), then serves.
On subsequent starts `RunMigrations` is a no-op.

---

## Search

Search is **built in** and needs no provisioning. It runs on Postgres full-text
search + `pg_trgm` (a bundled Postgres contrib module) over conversation
history. There is no embedding model, no ONNX Runtime, and no pgvector: stock
`postgres:16` (what `docker-compose.yml` pins) has everything it needs, so there
is nothing to download, mount, or keep in extra RAM.

---

## Required environment variables

Set these in a `.env` file (never commit `.env`) or as shell exports before
running `docker compose`.

### Postgres

| Variable | Default | Required |
|---|---|---|
| `POSTGRES_USER` | `orako` | No |
| `POSTGRES_PASSWORD` | — | **Yes** |
| `POSTGRES_DB` | `orako` | No |
| `POSTGRES_PORT` | `5432` | No |

### Orako server

| Variable | Default | Notes |
|---|---|---|
| `ORAKO_HTTP_PORT` | `8080` | Port inside container |
| `ORAKO_BASE_URL` | — | Required for Slack OAuth redirect URIs |

### Slack integration (optional)

| Variable | Notes |
|---|---|
| `ORAKO_SLACK_CLIENT_ID` | Slack app OAuth client ID |
| `ORAKO_SLACK_CLIENT_SECRET` | Slack app OAuth client secret |
| `ORAKO_SLACK_SIGNING_SECRET` | Slack request signing secret (v0 HMAC) |

When these are set, the OAuth install flow is mounted at:
- `GET /slack/oauth/install` — redirect to Slack
- `GET /slack/oauth/callback` — receives the OAuth code

Inbound events are received at `POST /slack/events/{projectID}`.

### License verify seam (optional)

The license key itself is **not** an env var — an org admin pastes it in the
dashboard under **Settings → License**, where it is stored in the DB
(`instance_license`) and applied at runtime (no restart). The env vars below
only tune renewal:

| Variable | Notes |
|---|---|
| `ORAKO_LICENSE_OFFLINE` | `true` = never contact Orako; the stored key is only re-checked for local expiry. |
| `ORAKO_LICENSE_REFRESH_URL` | Optional override of the baked hosted refresh endpoint. |

Paste a signed key in the dashboard to raise the caps — the signing public key is
baked into the binary, so nothing else is required. Absent a key, the server runs
the free Community edition. Verification is offline (Ed25519 signature + expiry
check); by default a licensed instance auto-renews its stored key each period
against Orako's hosted endpoint (only a licensed instance ever contacts it) and
persists the renewal, which `ORAKO_LICENSE_OFFLINE=true` disables for air-gapped
installs.

---

## Database migrations

Migrations are embedded in the binary (`sql/migrations/`) and applied
automatically at boot via `sql.RunMigrations` (golang-migrate + iofs). They run
on **stock Postgres** — the only extension used is `pg_trgm`, a bundled contrib
module enabled by a migration (no pgvector, no non-stock extension to install).

No separate migrate step is required. On a clean database the server creates
the schema from scratch; on an existing database only pending migrations run.

---

## Before a real production deploy

The following are NOT done yet and are required before shipping to real users:

1. **Secrets encryption**: credentials in `project_providers.credentials` are
   stored as plaintext JSONB. Encrypt at rest (envelope encryption via KMS)
   before GA.
2. **TLS**: the server currently runs plain HTTP/2 (h2c). Put a TLS-terminating
   reverse proxy (nginx, Caddy, Cloudflare Tunnel) in front.
3. **Slack app credentials**: register a real Slack app with the redirect URI
   pointing to `ORAKO_BASE_URL/slack/oauth/callback`.
4. **License issuance**: the control plane (not shipped) must sign license keys
   with the corresponding Ed25519 private key before distributing.
5. **Database backups**: configure Postgres WAL archiving or pg_dump for the
   `orako_pgdata` volume.
