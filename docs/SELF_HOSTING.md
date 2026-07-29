# Self-hosting Orako

Orako self-hosts as a single static Go binary + stock Postgres. The free
**Community edition** runs with no license and caps usage at **5 members, 1 org,
1 project**. Set a license key to raise the caps (see [Editions](#editions)).

After installation:

- [Connect an agent](CONNECT_AGENTS.md)
- [Configure Slack or Discord](INTEGRATIONS.md)
- [Operate the server](operations.md)
- [Back up and restore](BACKUP_RESTORE.md)

## Quickstart (Docker Compose)

```bash
git clone https://github.com/orako-io/core.git
cd core

# 1. Generate .env with strong random secrets + a first-admin login
bash scripts/bootstrap.sh https://orako.example.com you@example.com
#   → local test:  bash scripts/bootstrap.sh   (localhost + admin@example.com)
#   ↑ prints the generated admin password — SAVE IT, it is shown only once

# 2. Bring up Postgres + the server (migrations run automatically on boot)
docker compose up -d

# 3. Open the dashboard and sign in as the admin from step 1
open https://orako.example.com   # or http://localhost:8080
```

## Authentication (self-host)

In self-host, the server is its **own** identity provider (auth mode `local`): it
stores bcrypt password hashes and issues the session token — no Supabase, no
external IdP. The flow:

1. **First admin** is seeded once on boot from `ORAKO_ADMIN_EMAIL` /
   `ORAKO_ADMIN_PASSWORD` (`bootstrap.sh` fills these and prints the password).
   The seed is idempotent — after first boot it's a no-op, so leaving the values
   in `.env` is harmless.
2. **Invite teammates** from the dashboard. Each invitee gets an email link to
   set their own password. *Requires SMTP* (`ORAKO_SMTP_*`); without it the invite
   email cannot be sent.
3. **Forgot password** — the sign-in screen has a reset link that emails a
   set-new-password link (also needs SMTP). Uniform against email enumeration.

> SaaS (hosted Orako) uses Supabase OIDC instead (`ORAKO_AUTH_MODE=oidc`); that
> path is not needed — and the Supabase SDK is not bundled — in a self-host build.

### What `bootstrap.sh` writes

A `.env` (chmod 600) with random `POSTGRES_PASSWORD`, `ORAKO_AUTH_HS256_SECRET`,
and `ORAKO_ENCRYPTION_KEY`, `ORAKO_AUTH_MODE=local`, the seeded
`ORAKO_ADMIN_EMAIL` / `ORAKO_ADMIN_PASSWORD`, commented SMTP placeholders, and
your `ORAKO_BASE_URL`. **Never change `ORAKO_AUTH_HS256_SECRET` or
`ORAKO_ENCRYPTION_KEY` after first boot** — the former invalidates every session
and the latter makes encrypted integration credentials unreadable.

### Updating

```bash
git pull && docker compose up -d --build   # migrations run on boot; idempotent
```

## Editions

Editions resolve once at boot from your environment:

| Edition | Trigger | Limits |
|---|---|---|
| **Community** | no license (default) | 5 members / 1 org / 1 project |
| **Licensed** | valid license key (set in-app) | limits from the signed token |
| **Managed** | private deployment overlay | Entitlements managed by the operator |

Exceeding a Community cap returns a clear "add a license key" error. Verification
is **offline** (the signing public key is baked into the binary). To raise the
caps, obtain a signed key from the hosted licensing service and paste it in the
dashboard under **Settings → License** — it is stored in the DB and applies
**instantly, with no restart** (there is no license env var). The key auto-renews
each billing period; set `ORAKO_LICENSE_OFFLINE=true` for an air-gapped install.
The boot log prints the resolved edition and its limits.

## Search

Search is **built in** and needs no setup. It runs on Postgres full-text search
+ `pg_trgm` (a bundled Postgres contrib module) over conversation history — no
embedding model, no ONNX, no pgvector, no extra RAM, and no volume to mount.
Stock `postgres:16` (what `docker-compose.yml` pins) has everything it needs.

## Production notes

- **Reverse proxy required for TLS and abuse protection.** The server does not
  terminate TLS, rate-limit, or set CORS itself — front it with Caddy/Traefik/
  nginx. Only expose the proxy; keep Postgres on the internal network.
- **Pin the Postgres image.** `docker-compose.yml` uses stock `postgres:16`;
  for production pin to a specific patch or digest so a `docker compose pull`
  never lands a surprise Postgres major.
- **Back up the `orako_pgdata` volume.** It holds everything.
- Follow [Backup and restore](BACKUP_RESTORE.md) for verified PostgreSQL,
  attachment, and secret recovery.
- **Email (invites/reset)** needs SMTP (`ORAKO_SMTP_*`); without it those emails
  silently do not send. Attachments need S3-compatible storage (`ORAKO_S3_*`);
  without it, text still flows and uploads return a clear error.
## One-click PaaS (roadmap)

In-repo deploy templates (`render.yaml`, `.do/deploy.template.yaml`) are planned.
Most PaaS free tiers do not cover a Go service plus managed Postgres, so a small
VPS running the Compose stack is usually the cheapest production deployment.
