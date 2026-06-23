#!/usr/bin/env bash
# Host entrypoint for the Docker acceptance run. Brings up Elasticsearch + the
# ESFS runner, streams logs, and exits with the runner's acceptance result.
set -euo pipefail
cd "$(dirname "$0")"

COMPOSE="docker compose"
$COMPOSE version >/dev/null 2>&1 || COMPOSE="docker-compose"

cleanup() { $COMPOSE down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "ESFS acceptance: building and starting containers..."
$COMPOSE up --build --abort-on-container-exit --exit-code-from esfs
