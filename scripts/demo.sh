#!/usr/bin/env bash
# ESFS first-demo script (mirrors the plan's parity loop). Requires a reachable
# Elasticsearch and a FUSE backend (fuse3 on Linux, macFUSE/FUSE-T on macOS).
#
#   ES_ENDPOINT=https://localhost:9200 ES_API_KEY=... ./scripts/demo.sh
#
set -euo pipefail

MOUNT="${ESFS_MOUNT:-/esfs}"
INDEX="${ESFS_INDEX:-conversations}"
ENDPOINT="${ES_ENDPOINT:?set ES_ENDPOINT}"
BIN="$(cd "$(dirname "$0")/.."/bin && pwd)"

export ESFS_ENDPOINT="$ENDPOINT"
export ESFS_API_KEY_ENV="ES_API_KEY"

echo "== mounting $INDEX at $MOUNT =="
"$BIN/esfs" mount --mount "$MOUNT" --index "$INDEX" &
MOUNT_PID=$!
trap '"$BIN/esfs" unmount "$MOUNT" 2>/dev/null || true; kill $MOUNT_PID 2>/dev/null || true' EXIT
sleep 2

echo "== ls $MOUNT =="
/bin/ls "$MOUNT" || true

echo "== read profile =="
/bin/cat "$MOUNT/profile.md" || true
/bin/cat "$MOUNT/$INDEX/profile.md" || true

FIRST_ID="$(/bin/ls "$MOUNT/$INDEX" | grep -v -E '^(profile\.md|\.sync\.json|\.mapping\.json|\.fields\.json)$' | head -1 || true)"
if [ -n "${FIRST_ID:-}" ]; then
  echo "== cat $MOUNT/$INDEX/$FIRST_ID =="
  /bin/cat "$MOUNT/$INDEX/$FIRST_ID" || true
fi

echo "== sync status =="
/bin/cat "$MOUNT/$INDEX/.sync.json" || true

echo "== activate routed commands =="
eval "$("$BIN/esfs" env --mount "$MOUNT")"

echo "== routed ls / find / grep =="
cd "$MOUNT/$INDEX"
ls | head
find . | head
grep refund || true
grep "red shoes" --since 7d || true

echo "== exact system grep still available =="
/usr/bin/grep --version | head -1 || true

echo "== doctor =="
"$BIN/esfs" doctor || true

echo "demo complete"
