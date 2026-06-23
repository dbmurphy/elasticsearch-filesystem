# ESFS Contract Reference

This is the human-readable companion to the contract implemented in
`internal/contract`. The code is the source of truth; this doc summarizes it.

## Path model

The mountpoint (`<m>`) is arbitrary (`/esfs`, `/mnt/esfs`, …) and set via
`--mount`. A single mount binds to a whole cluster and exposes **all visible
indices** as top-level directories:

```
<m>/                          mount root: ALL visible indices/aliases + virtual files
<m>/<index>/                  index directory: documents + virtual files
<m>/<index>/<id>              one document, rendered as pretty JSON
<m>/profile.md                mount-scope digest (virtual, read-only)
<m>/.sync.json                mount-scope sync status (virtual, read-only)
<m>/<index>/profile.md        index-scope digest (virtual, read-only)
<m>/<index>/.sync.json        index-scope sync status (virtual, read-only)
<m>/<index>/.mapping.json     optional mapping diagnostic (virtual)
<m>/<index>/.fields.json      optional field-caps diagnostic (virtual)
```

- One mount = many indices. Every visible index/alias in the cluster appears as
  a directory; `<m>/<index>/<id>` resolves any of them.
- The exposed set defaults to all visible indices; `--index a,b,c` (or
  `visible_indices` in config) narrows it to an allowlist.
- System/hidden indices (leading `.`) are excluded by default.
- Root-scope traversal (`find <m>`, `grep -r foo <m>`) spans every visible
  index, searched in turn — not a cross-index join.

## Filename encoding (`pathenc.go`)

- Safe IDs are used verbatim.
- Unsafe IDs (containing `/`, control bytes, reserved names, or a leading `@`)
  use a reversible `@e=<percent>` encoding.
- IDs that would exceed `NAME_MAX` use a deterministic `@h=<base32(sha256)>`
  short name; `esfs path <index> <id>` is the canonical resolver and the daemon
  keeps an in-memory reverse map populated during listing/reads.
- Reserved virtual names (`profile.md`, `.sync.json`, `.mapping.json`,
  `.fields.json`, `.esfs-info`) never shadow a document; a colliding ID is
  exposed through its encoded form.

## Read / write / delete

- Read: `cat` renders `_source` as pretty JSON; metadata-capable reads capture
  `_seq_no`/`_primary_term`/`_version` for OCC and cache validation.
- Write: whole-document replacement of `_source`. Default policy
  `safe-create-update` — update existing paths, create-only for absent paths;
  `O_EXCL` forces create-only; `upsert-new-files` is opt-in per profile.
- Editor saves: truncate-write-flush and open-edit-save supported; invalid
  intermediate JSON is never flushed; rename to a different ID is unsupported.
- Delete: gated; disabled by default, enabled per profile/mount.
- Alias writes: allowed only when the alias has exactly one concrete write index.

## Error mapping (`errors.go`)

| condition                         | errno         |
|-----------------------------------|---------------|
| missing document/index/path       | ENOENT        |
| authn/authz failure               | EACCES        |
| read-only / delete disabled       | EROFS         |
| unsupported operation             | EPERM         |
| invalid JSON on flush             | EINVAL        |
| version conflict (OCC)            | EAGAIN        |
| ES timeout/unavailable/shard fail | EIO           |
| name too long / unencodable       | ENAMETOOLONG  |

CLI/shim exit codes follow grep: `0` match, `1` no match, `2` error.

## Routed command contract (`routing`, `cmdshim`)

- Activation is explicit via `ESFS_MOUNT` (set by `eval "$(esfs env)"` or Bash
  Tool mode). Unactivated shells pass through to system tools.
- `ls`/`find`/`grep` route only for paths inside a mount root; non-ESFS operands
  and absolute system-tool paths (`/usr/bin/grep`, …) are never routed.
- `grep` is ranked Elasticsearch search; semantic only when the index has
  `semantic_text` fields, otherwise labeled ranked lexical, otherwise a
  diagnostic. `--since DUR` compiles to a date `range` filter against the
  configured or uniquely-inferred date field.
- `--exact`, `ESFS_GREP_MODE=exact`, `ESFS_STRICT_GREP=1`, or unsupported flags
  trigger byte-level fallback over rendered documents.

## No Elasticsearch-side setup

v1 performs no mapping changes, index templates, ingest pipelines, stored
scripts, hidden indices, reindexing, saved searches, or ES-stored digests.
Profiles are generated and cached locally. Only normal document read/write/delete
APIs and transient PIT search contexts are used.
