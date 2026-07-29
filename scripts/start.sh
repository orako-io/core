#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Dev boot gate, invoked by reflex on every reload inside the orako_server
# container. The server only starts if lint and tests pass: lint -> test ->
# run with the race detector. A failure here blocks the boot by design.
set -e

echo "[start.sh] golangci-lint..."
golangci-lint run ./...

echo "[start.sh] go test -race..."
go test -race ./...

echo "[start.sh] go run -race ./cmd/orako-server ..."
exec env CGO_ENABLED=1 go run -race ./cmd/orako-server/
