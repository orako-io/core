# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Production multi-stage Dockerfile for orako-server.
#
# ── What ships in the image ─────────────────────────────────────────────────────
# A single static Go binary. The dashboard (go:embed) and the schema migrations
# (embedded golang-migrate, applied on boot) are compiled in. History search is
# built-in — Postgres FTS + pg_trgm (a bundled contrib module) — so there is NO
# model to download or mount, NO ONNX Runtime, and NO pgvector: the image needs
# only a Postgres connection to run.
#
# ── Build invariants ───────────────────────────────────────────────────────────
# - CGO_ENABLED=0: nothing links C anymore, so the binary is fully static.
# - GOWORK=off is set so a stray go.work in a parent directory does not hijack
#   module resolution.
#
# ── Building ───────────────────────────────────────────────────────────────────
#   docker build -f cmd/orako-server/Dockerfile -t orako-server:latest .
#
# Multi-platform (requires Docker Buildx):
#   docker buildx build --platform linux/amd64,linux/arm64 ...

# ── Stage 0: Dashboard build ──────────────────────────────────────────────────
# The Vite dashboard is embedded in the binary (go:embed). Auth mode is baked
# at build time (the server has no discovery endpoint): `local` by default —
# the self-host email+password login that POSTs /auth/login, matching the
# server's default local auth mode. The SaaS image passes
# `--build-arg VITE_ORAKO_AUTH_MODE=oidc` (+ the Supabase publishable pair).
# The insecure `dev` stub UI (unsigned bearer tokens) is only for the source
# hot-reload flow (docker-compose.dev.yml), never a built prod image. Vite's
# outDir already targets internal/infra/webui/dist.
FROM node:22-bookworm-slim AS webbuilder

ARG VITE_ORAKO_AUTH_MODE=local
ARG VITE_SUPABASE_URL=
ARG VITE_SUPABASE_PUBLISHABLE_KEY=

WORKDIR /web

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
COPY pack/ /pack/

ENV VITE_ORAKO_AUTH_MODE=${VITE_ORAKO_AUTH_MODE} \
    VITE_SUPABASE_URL=${VITE_SUPABASE_URL} \
    VITE_SUPABASE_PUBLISHABLE_KEY=${VITE_SUPABASE_PUBLISHABLE_KEY}

RUN npm run build

# ── Stage 1: Build ────────────────────────────────────────────────────────────
# golang:1.26-bookworm tracks the latest 1.26 patch so the binary's stdlib
# carries the crypto/tls fix (GO-2026-5856, fixed in 1.26.5) rather than the
# vulnerable 1.25 stdlib.
FROM golang:1.26-bookworm AS builder

# Go build tags. The community image builds with no tags (default). The SaaS
# image passes BUILD_TAGS="saas" AND provides the private internal/saas overlay
# in the build context (see the orako-io SaaS overlay repo). The public build
# never needs the overlay: without the saas tag, no billing code is compiled.
ARG BUILD_TAGS=

WORKDIR /src

# Prime the module cache (layer is cached unless go.mod/go.sum change).
COPY go.mod go.sum ./
RUN GOWORK=off go mod download

COPY . .

# Embedded dashboard: replace the committed dist with the stage-0 build so the
# image always serves a UI matching the requested auth mode.
COPY --from=webbuilder /internal/infra/webui/dist internal/infra/webui/dist

# Build orako-server as a fully static binary (CGO_ENABLED=0): no C toolchain,
# no shared libraries, no ONNX Runtime.
RUN CGO_ENABLED=0 GOWORK=off go build \
        -tags "${BUILD_TAGS}" \
        -ldflags="-s -w" \
        -o /bin/orako-server \
        ./cmd/orako-server

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM debian:bookworm-slim AS runtime

# CA certificates for outbound TLS (Slack/SMTP/S3). Nothing else is
# needed — the binary is static and search has no native dependencies.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# orako-server binary.
COPY --from=builder /bin/orako-server /usr/local/bin/orako-server

# Run as a non-root system user (defense in depth: a process compromise has no
# container-root privileges). The server binds a non-privileged port (8080), so
# no elevated access is needed.
RUN useradd --system --uid 10001 --user-group --home-dir /home/orako --create-home orako
USER orako

# Override POSTGRES_*/ORAKO_* for your deployment.
ENV ORAKO_HTTP_PORT=8080

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/orako-server"]
