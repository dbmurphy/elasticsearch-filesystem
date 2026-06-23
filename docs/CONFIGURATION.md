# ESFS Configuration

ESFS resolves configuration from a JSON file (`$ESFS_CONFIG`, or
`~/.config/esfs/config.json`), then overlays environment variables, then CLI
flags. Durations use human strings (`"10s"`, `"500ms"`, `"1m"`). See
`config.example.json` (minimal) and `examples/config.full.json` (everything).

## Global settings

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `endpoint` | string | — (required) | Elasticsearch base URL. |
| `api_key_env` | string | — | Env var holding the API key (preferred over inlining). |
| `api_key` | string | — | Inline API key (use a chmod-0600 file only). |
| `ca_cert_file` | string | — | CA bundle for TLS verification. |
| `insecure` | bool | `false` | Disable TLS verification (test clusters only). |
| `visible_indices` | string[] | `[]` (all) | Allowlist of indices/aliases to expose. Empty exposes **all** visible indices. |
| `write_policy` | string | `safe-create-update` | `safe-create-update` \| `create-only` \| `upsert-new-files`. |
| `delete_sync` | bool | `false` | Allow document deletion on `unlink`/`rm`. |
| `grep_mode` | string | `auto` | Default search mode: `auto` \| `semantic` \| `lexical`. |
| `time_field` | string | — | Default date field for `--since` (see per-index override). |
| `timezone` | string | — | IANA tz for relative-time anchoring (default: client clock). |
| `request_timeout` | duration | `"10s"` | Per-request Elasticsearch timeout. |
| `concurrency` | int | `8` | Max in-flight ES requests per mount. |
| `cache_ttl` | duration | `"30s"` | Metadata/document/profile cache lifetime. |
| `redact_fields` | string[] | `[]` | Fields redacted in generated profiles (global). |
| `profile_opt_out` | string[] | `[]` | Indices for which `profile.md` is disabled (global). |
| `indices` | map | `{}` | Per-index overrides (below). |

## Per-index settings (`indices.<name>`)

Each entry overrides the global default for that index. This is how you control
**how each index searches** — the same mount can mix semantic and lexical
indices.

| Field | Type | Inherits | Purpose |
|-------|------|----------|---------|
| `time_field` | string | `time_field` | Date field used by `grep --since` for this index. |
| `search_fields` | string[] | all `text` fields | Restrict lexical search to these fields (better ranking). |
| `semantic_fields` | string[] | auto-detected `semantic_text` | Use these fields for semantic search. |
| `grep_mode` | string | `grep_mode` | `auto` \| `semantic` \| `lexical` for this index. |
| `redact_fields` | string[] | merged with global | Extra fields to redact in this index's profile. |
| `profile` | bool | `true` | Enable/disable `profile.md` for this index. |
| `write_policy` | string | `write_policy` | Per-index write policy. |
| `delete_sync` | bool | `delete_sync` | Per-index delete gating. |

### How search mode is decided

Precedence (highest first):

1. `grep --mode <m>` flag (or `ESFS_GREP_MODE`).
2. `indices.<name>.grep_mode`.
3. global `grep_mode` (default `auto`).

In `auto`/`semantic`, ESFS uses semantic search only when the index actually has
`semantic_text` fields (configured via `semantic_fields` or auto-detected);
otherwise it uses ranked lexical search. Forcing `semantic` on an index without
such fields returns a diagnostic (exit 2) rather than silently degrading. The
effective mode is printed on stderr (`esfs: grep mode=...`) and shown at the top
of each index's `profile.md`.

### `--since` time field resolution

`grep --since 7d` needs a `date`/`date_nanos` field. ESFS uses, in order:
`indices.<name>.time_field` → global `time_field` → a single unambiguous
auto-detected date field (`@timestamp`, `timestamp`, `created_at`,
`updated_at`). Ambiguous or missing fields return a diagnostic.

## Environment variables

Useful for Bash Tool mode / containers without a config file:

| Var | Maps to |
|-----|---------|
| `ESFS_CONFIG` | config file path |
| `ESFS_ENDPOINT` | `endpoint` |
| `ESFS_API_KEY_ENV` | `api_key_env` |
| `ESFS_CA_CERT` | `ca_cert_file` |
| `ESFS_INSECURE` | `insecure` |
| `ESFS_DELETE_SYNC` | `delete_sync` |
| `ESFS_TIME_FIELD` | `time_field` |
| `ESFS_TIMEOUT_MS` | `request_timeout` |
| `ESFS_MOUNT` | routed-command activation + mount roots |
| `ESFS_GREP_MODE` | `auto|semantic|lexical|exact` |
| `ESFS_STRICT_GREP` | `1` forces exact fallback for unsupported flags |

## Example rendered profiles

See `examples/profile.mount.md` and `examples/profile.index.md` for exactly what
`cat /esfs/profile.md` and `cat /esfs/<index>/profile.md` produce.
