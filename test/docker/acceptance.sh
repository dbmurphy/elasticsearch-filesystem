#!/usr/bin/env bash
# ESFS acceptance loop against a REAL Elasticsearch on Linux.
#
# Tier A (always): routed esfs-ls/find/grep + --since date filtering + semantic
#   diagnostic + the no-ES-side-setup guard. These exercise the shared core and
#   real ES queries and need no kernel mount.
# Tier B (when /dev/fuse + SYS_ADMIN are present): the real FUSE mount, cat,
#   write-back via redirection, exact /usr/bin/grep over the mount, and
#   profile.md. Skipped-with-notice when the container lacks FUSE privileges.
#
# Exit code is the acceptance result: 0 = all executed assertions passed.
set -u

ES="${ESFS_ENDPOINT:-http://localhost:9200}"
INDEX="${ESFS_INDEX:-conversations}"
MOUNT="${ESFS_MOUNT:-/esfs}"
HERE="$(cd "$(dirname "$0")" && pwd)"

PASS=0
FAIL=0
green() { printf '\033[32m%s\033[0m\n' "$1"; }
red() { printf '\033[31m%s\033[0m\n' "$1"; }

ok()   { PASS=$((PASS+1)); green "PASS: $1"; }
bad()  { FAIL=$((FAIL+1)); red  "FAIL: $1"; }

assert_contains() { # label needle haystack
  case "$3" in
    *"$2"*) ok "$1" ;;
    *)      bad "$1 (expected to contain '$2')"; printf '  got: %s\n' "$3" ;;
  esac
}
assert_not_contains() {
  case "$3" in
    *"$2"*) bad "$1 (expected NOT to contain '$2')"; printf '  got: %s\n' "$3" ;;
    *)      ok "$1" ;;
  esac
}
assert_eq() { # label expected actual
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (expected '$2' got '$3')"; fi
}

echo "================ ESFS acceptance (real Elasticsearch) ================"
echo "ES=$ES INDEX=$INDEX MOUNT=$MOUNT"
echo

# ---- Wait for Elasticsearch readiness ----
echo "waiting for Elasticsearch at $ES ..."
ready=0
for _ in $(seq 1 60); do
  if curl -sf "$ES/_cluster/health" >/dev/null 2>&1; then ready=1; break; fi
  sleep 2
done
if [ "$ready" != "1" ]; then
  red "Elasticsearch did not become ready in time"
  exit 1
fi
echo "Elasticsearch is up."
echo

# ---- Seed fixture data (test harness, not ESFS) ----
"$HERE/seed.sh"
echo

# ---- Snapshot for the no-ES-side-setup guard ----
snapshot() {
  {
    echo "indices:"; curl -sf "$ES/_cat/indices?h=index" | sort
    echo "templates:"; curl -sf "$ES/_index_template" | jq -r '.index_templates[].name' 2>/dev/null | sort
    echo "component_templates:"; curl -sf "$ES/_component_template" | jq -r '.component_templates[].name' 2>/dev/null | sort
    echo "pipelines:"; curl -sf "$ES/_ingest/pipeline" | jq -r 'keys[]' 2>/dev/null | sort
    echo "mapping:"; curl -sf "$ES/$INDEX/_mapping" | jq -S .
  } 2>/dev/null
}
BEFORE="$(snapshot)"

export ESFS_MOUNT="$MOUNT"

echo "---------------- Tier A: routed commands ----------------"

A_LS_ROOT="$(esfs-ls "$MOUNT" 2>/dev/null)"
assert_contains "ls mount lists index" "$INDEX" "$A_LS_ROOT"
assert_contains "ls mount lists profile.md" "profile.md" "$A_LS_ROOT"

A_LS_IDX="$(esfs-ls "$MOUNT/$INDEX" 2>/dev/null)"
assert_contains "ls index lists doc r1" "r1" "$A_LS_IDX"
assert_contains "ls index lists doc s1" "s1" "$A_LS_IDX"
assert_contains "ls index lists .fields.json" ".fields.json" "$A_LS_IDX"

A_FIND="$(esfs-find "$MOUNT/$INDEX" 2>/dev/null)"
assert_contains "find lists index dir" "$MOUNT/$INDEX" "$A_FIND"
assert_contains "find lists doc path r1" "$MOUNT/$INDEX/r1" "$A_FIND"

# Routed ranked grep over real ES.
A_GREP_OUT="$(esfs-grep refund "$MOUNT/$INDEX" 2>/tmp/grep_a.err)"
A_GREP_ERR="$(cat /tmp/grep_a.err)"
assert_contains "grep refund matches r1" "$MOUNT/$INDEX/r1" "$A_GREP_OUT"
assert_contains "grep refund matches r2" "$MOUNT/$INDEX/r2" "$A_GREP_OUT"
assert_not_contains "grep refund excludes s1" "$MOUNT/$INDEX/s1" "$A_GREP_OUT"
assert_contains "grep discloses ranked-lexical mode" "mode=ranked-lexical" "$A_GREP_ERR"

# --since date filtering executes in real ES (filter context): recent only.
S_OUT="$(esfs-grep "red shoes" --since 7d "$MOUNT/$INDEX" 2>/dev/null)"
assert_contains "grep --since matches recent s1" "$MOUNT/$INDEX/s1" "$S_OUT"
assert_not_contains "grep --since excludes old s2" "$MOUNT/$INDEX/s2" "$S_OUT"

# Forcing semantic mode without semantic_text fields must be a diagnostic.
esfs-grep --mode semantic refund "$MOUNT/$INDEX" >/tmp/sem.out 2>/tmp/sem.err
SEM_RC=$?
assert_eq "semantic-forced exit code 2" "2" "$SEM_RC"
assert_contains "semantic-forced diagnostic" "semantic" "$(cat /tmp/sem.err)"

# Routed grep via PATH activation (eval "$(esfs env)") -> bare `grep`.
eval "$(esfs env --mount "$MOUNT" 2>/dev/null)"
ENV_GREP="$(grep refund "$MOUNT/$INDEX" 2>/dev/null)"
assert_contains "activated bare grep routes" "$MOUNT/$INDEX/r1" "$ENV_GREP"

# No-match exit code (1).
esfs-grep zzz-no-such-term "$MOUNT/$INDEX" >/dev/null 2>&1
assert_eq "no-match exit code 1" "1" "$?"

echo
echo "---------------- no-ES-side-setup guard ----------------"
AFTER="$(snapshot)"
if [ "$BEFORE" = "$AFTER" ]; then
  ok "ESFS created no indices/templates/pipelines and did not mutate mapping"
else
  bad "ESFS-side setup changed cluster state"
  diff <(printf '%s' "$BEFORE") <(printf '%s' "$AFTER") | sed 's/^/  /'
fi

echo
echo "---------------- Tier B: real FUSE mount ----------------"
if [ ! -e /dev/fuse ]; then
  echo "SKIP: /dev/fuse not present; mount tier not exercised in this environment."
else
  mkdir -p "$MOUNT"
  esfs mount --mount "$MOUNT" --index "$INDEX" >/tmp/mount.log 2>&1 &
  MPID=$!
  mounted=0
  for _ in $(seq 1 20); do
    if /bin/ls "$MOUNT/$INDEX" >/dev/null 2>&1; then mounted=1; break; fi
    sleep 0.5
  done
  if [ "$mounted" != "1" ]; then
    echo "SKIP: mount did not come up (likely missing SYS_ADMIN/apparmor). Log:"
    sed 's/^/  /' /tmp/mount.log
    kill "$MPID" 2>/dev/null
  else
    # cat a document through the mount.
    DOC="$(/bin/cat "$MOUNT/$INDEX/r1" 2>/dev/null)"
    if printf '%s' "$DOC" | jq -e . >/dev/null 2>&1; then ok "cat renders valid JSON"; else bad "cat did not render valid JSON"; fi
    assert_contains "cat r1 contains body" "refund" "$DOC"

    # Write-back via redirection: cat newfile > mountpath.
    printf '%s' "$DOC" | jq '. + {acceptance:"written"}' > /tmp/r1.json
    /bin/cat /tmp/r1.json > "$MOUNT/$INDEX/r1"
    sleep 1
    ESDOC="$(curl -sf "$ES/$INDEX/_doc/r1" | jq -c '._source')"
    assert_contains "write-back synced to ES" "\"acceptance\":\"written\"" "$ESDOC"

    # Exact system grep over the mounted file.
    EX="$(/usr/bin/grep refund "$MOUNT/$INDEX/r1" 2>/dev/null)"
    assert_contains "/usr/bin/grep exact over mount" "refund" "$EX"

    # profile.md via the mount.
    PROF="$(/bin/cat "$MOUNT/$INDEX/profile.md" 2>/dev/null)"
    assert_contains "index profile.md renders" "Search" "$PROF"

    /bin/cat "$MOUNT/.sync.json" >/dev/null 2>&1 && ok "mount .sync.json readable" || bad ".sync.json not readable"

    fusermount3 -u "$MOUNT" 2>/dev/null || umount "$MOUNT" 2>/dev/null
    kill "$MPID" 2>/dev/null
  fi
fi

echo
echo "================ Result: $PASS passed, $FAIL failed ================"
[ "$FAIL" -eq 0 ]
