# Backup and restore

A complete Orako backup contains three parts:

1. the PostgreSQL database;
2. attachment objects, when S3-compatible storage is enabled;
3. stable deployment secrets and configuration.

The database contains organizations, members, projects, conversations, search
data, licenses, and encrypted provider credentials. Attachment bytes live
outside PostgreSQL.

## Back up PostgreSQL

From the directory containing `docker-compose.yml`:

```bash
mkdir -p backups
docker compose exec -T orako_postgres \
  sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
  > "backups/orako-$(date -u +%Y%m%dT%H%M%SZ).dump"
```

Check that the dump is non-empty and periodically test it by restoring into a
separate PostgreSQL instance. A backup that has never been restored is
unverified.

For larger installations, use managed PostgreSQL snapshots or continuous WAL
archiving in addition to logical dumps.

## Back up attachments

When `ORAKO_S3_ENDPOINT` is configured, back up the entire
`ORAKO_S3_BUCKET`. Use the storage provider's versioning, replication, or
snapshot feature. Database rows contain storage keys, so the database dump and
bucket snapshot should be taken in the same backup window.

No object-storage backup is needed when attachments are disabled.

## Back up stable secrets

Store the deployment configuration in a secret manager, not in the database
dump. The restore requires the original:

- `ORAKO_ENCRYPTION_KEY`;
- `ORAKO_AUTH_HS256_SECRET`;
- PostgreSQL credentials;
- SMTP and object-storage credentials;
- provider and reverse-proxy configuration not stored in Orako.

Changing `ORAKO_ENCRYPTION_KEY` loses access to encrypted provider credentials.
Changing `ORAKO_AUTH_HS256_SECRET` invalidates existing local sessions.

## Restore PostgreSQL

Restoring replaces the current database. Stop Orako first and keep a copy of
the current database until verification succeeds.

```bash
docker compose stop orako_server

docker compose exec -T orako_postgres sh -c \
  'dropdb -U "$POSTGRES_USER" --if-exists "$POSTGRES_DB" &&
   createdb -U "$POSTGRES_USER" "$POSTGRES_DB"'

docker compose exec -T orako_postgres sh -c \
  'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner --no-privileges' \
  < backups/orako-YYYYMMDDTHHMMSSZ.dump

docker compose start orako_server
```

Restore the attachment bucket to the matching snapshot and start the server
with the same stable secrets.

## Verify the restore

```bash
curl --fail https://orako.example.com/healthz
curl --fail https://orako.example.com/readyz
```

Then verify:

- an admin can sign in;
- projects and members are present;
- conversation history is searchable;
- an old attachment can be downloaded;
- Slack or Discord can send a test message;
- the edition shown under Settings is correct.

## Retention

Keep multiple recovery points and at least one copy outside the production
host. Encrypt backups, restrict access, and define retention according to the
conversation data your organization stores in Orako.
