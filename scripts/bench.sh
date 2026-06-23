#!/usr/bin/env bash
# ESFS benchmark harness.
#
# Phase 1 (always): ESFS-side overhead micro-benchmarks (no cluster, no mount).
# Phase 2 (optional): end-to-end mount benchmarks when a FUSE backend and an
# Elasticsearch endpoint are available. Records the environment metadata the
# performance plan requires (OS, backend, ES version, fixtures).
set -euo pipefail
cd "$(dirname "$0")/.."

echo "=== ESFS-side overhead (Go benchmarks) ==="
go test -run '^$' -bench . -benchmem ./internal/contract/ ./internal/escore/

if [ -z "${ES_ENDPOINT:-}" ]; then
  echo
  echo "ES_ENDPOINT not set: skipping end-to-end mount benchmarks."
  echo "To run them: ES_ENDPOINT=... ES_API_KEY=... ESFS_MOUNT=/esfs ./scripts/bench.sh"
  exit 0
fi

MOUNT="${ESFS_MOUNT:-/esfs}"
INDEX="${ESFS_INDEX:-conversations}"
echo
echo "=== Environment ==="
uname -a
go version
echo "ES_ENDPOINT=$ES_ENDPOINT INDEX=$INDEX MOUNT=$MOUNT"

export ESFS_ENDPOINT="$ES_ENDPOINT" ESFS_API_KEY_ENV="ES_API_KEY"
./bin/esfs mount --mount "$MOUNT" --index "$INDEX" &
MP=$!
trap './bin/esfs unmount "'"$MOUNT"'" 2>/dev/null || true; kill $MP 2>/dev/null || true' EXIT
sleep 2

echo "=== cat latency (p-ish via time, 20 reads) ==="
ID="$(/bin/ls "$MOUNT/$INDEX" | grep -v -E '^\.' | grep -v profile.md | head -1)"
if [ -n "$ID" ]; then
  time (for i in $(seq 1 20); do /bin/cat "$MOUNT/$INDEX/$ID" >/dev/null; done)
fi

echo "=== ls throughput ==="
time (/bin/ls "$MOUNT/$INDEX" | wc -l)

echo "=== find throughput ==="
time (/usr/bin/find "$MOUNT/$INDEX" | wc -l)

echo "benchmark complete"
