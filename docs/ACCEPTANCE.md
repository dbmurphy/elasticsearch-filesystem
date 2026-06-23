# ESFS Acceptance Tests (Linux + real Elasticsearch via Docker)

This is the repeatable, documentable end-to-end validation required by the
plan's Parity Definition of Done. It runs on Linux against a **real**
Elasticsearch node with seeded data.

## Run

```sh
make acceptance
# or
./test/docker/run.sh
```

This brings up two containers via `test/docker/docker-compose.yml`:

- `elasticsearch` — single-node ES 8.15 (security disabled for the test).
- `esfs` — a Linux runner that builds ESFS, seeds data, and runs the parity
  acceptance loop. Its exit code is the result (`0` = pass), so it is CI-ready
  (`--exit-code-from esfs`).

Requirements: a working Docker engine with `docker compose`. For the FUSE mount
tier, the runner is granted `SYS_ADMIN`, `/dev/fuse`, and
`apparmor:unconfined`.

## What it seeds

`test/docker/seed.sh` creates the `conversations` index with a mapping
(`body:text`, `@timestamp:date`, `status:keyword`) and bulk-inserts a known
fixture: refund/red-shoes documents split between "now" and "30 days ago" so the
`--since` date filter is verifiable.

> The harness creates the index and documents with `curl` — this is **test
> fixture setup**, not ESFS. ESFS performs no Elasticsearch-side setup; the
> no-setup guard below proves it.

## What it asserts

Tier A — routed commands against real ES (no kernel mount needed):

- `esfs-ls /esfs` lists the index and virtual files.
- `esfs-ls /esfs/conversations` lists documents (`r1`, `s1`, …) and diagnostics.
- `esfs-find /esfs/conversations` streams document paths.
- `esfs-grep refund` returns the refund docs, excludes others, and discloses
  `mode=lexical`.
- `esfs-grep "red shoes" --since 7d` returns the recent doc and **excludes** the
  30-day-old one — proving the date `range` filter runs in Elasticsearch filter
  context, not client-side.
- `esfs-grep --mode semantic` without `semantic_text` fields exits `2` with a
  diagnostic.
- `eval "$(esfs env)"` activation makes bare `grep` route.
- No-match returns exit code `1`.

No-ES-side-setup guard:

- Snapshots indices, index/component templates, ingest pipelines, and the index
  mapping before and after all ESFS operations and asserts they are unchanged.

Tier B — real FUSE mount (when `/dev/fuse` + privileges are available):

- `cat /esfs/conversations/r1` renders valid JSON.
- `cat new.json > /esfs/conversations/r1` (redirection) syncs the change back to
  Elasticsearch (verified via `curl`).
- `/usr/bin/grep refund /esfs/conversations/r1` performs exact byte-level grep
  over the mounted document.
- `cat /esfs/conversations/profile.md` renders the index digest.

If the container lacks FUSE privileges, Tier B is **skipped with a notice**
(not failed); Tier A still fully validates routing, search, `--since`, and the
no-setup guarantee against real Elasticsearch.

## Local (no Docker)

On a host with a FUSE backend and a reachable cluster you can run the same loop
directly:

```sh
ES_ENDPOINT=https://localhost:9200 ES_API_KEY=... ./scripts/demo.sh
```
