# Orako operations runbook

This runbook covers a production-style Docker Compose deployment of Orako Core.
Start with [Self-hosting](SELF_HOSTING.md), then use this document for runtime
configuration, monitoring, updates, and migration recovery.

## Development and production stacks

| | Development | Production |
|---|---|---|
| Compose file | `docker-compose.dev.yml` | `docker-compose.yml` |
| Server | Hot reload with a test/lint boot gate | Compiled static binary |
| Start | `task up` | `docker compose up -d --build` |
| Database | Stock PostgreSQL 16 | Stock PostgreSQL 16 |
| Search | PostgreSQL FTS + `pg_trgm` | PostgreSQL FTS + `pg_trgm` |
| Migrations | Automatic on server boot | Automatic on server boot |

Never deploy with `ORAKO_AUTH_MODE=dev`. Development authentication accepts
unsigned identities and the server refuses to start with it when
`ORAKO_BASE_URL` is configured.

## Required production configuration

Run `scripts/bootstrap.sh` to generate the required local-auth and encryption
secrets. For an external database, set a full `ORAKO_DATABASE_URL` instead of
the individual `POSTGRES_*` values.

| Variable | Required | Purpose |
|---|---:|---|
| `POSTGRES_PASSWORD` | Yes with Compose | PostgreSQL password |
| `ORAKO_BASE_URL` | Yes | Public HTTPS origin and OAuth resource URL |
| `ORAKO_ENCRYPTION_KEY` | Yes | Encrypts provider credentials at rest |
| `ORAKO_AUTH_MODE` | Yes | Use `local` for the bundled self-hosted identity provider |
| `ORAKO_AUTH_HS256_SECRET` | Yes in local mode | Signs dashboard sessions |
| `ORAKO_AUTH_ISSUER` | Yes in local mode | Expected token issuer, normally `ORAKO_BASE_URL` |
| `ORAKO_AUTH_AUDIENCE` | Yes in local mode | Expected dashboard token audience |
| `ORAKO_ADMIN_EMAIL` | First boot | Seeds the first local administrator |
| `ORAKO_ADMIN_PASSWORD` | First boot | Seeds the first local administrator |

Keep `ORAKO_ENCRYPTION_KEY` and `ORAKO_AUTH_HS256_SECRET` stable after first
boot. Store them in a secret manager and include them in the backup plan.

## Optional configuration

### Email

Invitations and password reset require:

- `ORAKO_SMTP_HOST`
- `ORAKO_SMTP_PORT` (default `587`)
- `ORAKO_SMTP_USERNAME`
- `ORAKO_SMTP_PASSWORD`
- `ORAKO_SMTP_FROM`

Without a valid SMTP configuration, the server continues to run but no email is
sent.

### Attachments

Attachments require an S3-compatible bucket:

- `ORAKO_S3_ENDPOINT`
- `ORAKO_S3_REGION`
- `ORAKO_S3_BUCKET`
- `ORAKO_S3_ACCESS_KEY`
- `ORAKO_S3_SECRET_KEY`
- `ORAKO_ATTACHMENT_MAX_BYTES` (optional, default 25 MiB)

Without object storage, text conversations still work and attachment uploads
return a clear error.

### Proxy and observability

- `ORAKO_TRUSTED_PROXY_CIDRS` allows forwarding headers only from listed proxy
  networks.
- `LOG_LEVEL` controls structured stdout logging.
- `ORAKO_BETTERSTACK_SOURCE_TOKEN` and
  `ORAKO_BETTERSTACK_INGEST_HOST` enable the optional hosted log sink.
- `ORAKO_DEBUG_ENDPOINTS=1` exposes unauthenticated diagnostics and should stay
  disabled unless the surrounding network restricts access.

See [Messaging integrations](INTEGRATIONS.md) for provider-specific setup.

## Reverse proxy

Terminate TLS and rate-limit at Caddy, Traefik, nginx, or another trusted edge.
Only expose the proxy publicly. Keep PostgreSQL on an internal network; remove
the host `5432` port mapping when remote database access is unnecessary.

Forward WebSocket/SSE-compatible HTTP connections without response buffering.
The dashboard, Connect-RPC, MCP, OAuth, and provider webhooks all share the same
public origin.

## Health checks

```bash
curl --fail https://orako.example.com/healthz
curl --fail https://orako.example.com/readyz
```

- `/healthz` confirms the process is running.
- `/readyz` confirms dependencies required for serving traffic are ready.

Use `/readyz` for load-balancer readiness and deployment verification.

## Database migrations

SQL migrations are embedded in the binary and applied automatically at boot.
There is no separate production migration command. The server runs every
pending migration in order and then starts serving.

Before an upgrade:

1. read the release notes;
2. create a verified database backup;
3. preserve the currently deployed image or commit for rollback;
4. deploy one Orako server against the database until migrations complete.

### Dirty migration recovery

If startup reports `Dirty database version N`, do not blindly mark the
migration clean. A migration began and did not finish.

1. Stop every Orako server using that database.
2. Back up the database in its current state.
3. Open `sql/migrations/NNNN_*.up.sql` for the reported version.
4. Inspect which statements completed and repair or roll them back.
5. Only after the schema matches the end of that migration, clear the dirty
   flag with a compatible `golang-migrate` client.
6. Restart one server and verify `/readyz` before restoring normal traffic.

If the partial state is unclear, restore the pre-upgrade backup instead of
forcing a version.

## Update

```bash
git pull --ff-only
docker compose build --pull orako_server
docker compose up -d
docker compose logs -f --tail=200 orako_server
```

Confirm `/readyz`, sign in, search history, and send a provider test. Keep the
previous image and backup until these checks pass.

## Backup and restore

PostgreSQL contains the application state. Attachment bytes live in the
configured object store. Both plus the stable secrets are required for a
complete recovery.

Follow [Backup and restore](BACKUP_RESTORE.md) and test the restore process on a
separate database regularly.

## License operation

The license key is stored in the database and managed under
**Settings → License**. It is verified locally and applies without a restart.

`ORAKO_LICENSE_OFFLINE=true` disables automatic refresh for an air-gapped
deployment. A Community instance without a license does not contact the license
service.
