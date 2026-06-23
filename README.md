# ESFS — Elasticsearch as a filesystem

[![CI](https://github.com/dbmurphy/elasticsearch-filesystem/actions/workflows/ci.yml/badge.svg)](https://github.com/dbmurphy/elasticsearch-filesystem/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/dbmurphy/elasticsearch-filesystem?sort=semver)](https://github.com/dbmurphy/elasticsearch-filesystem/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

ESFS mounts Elasticsearch as an SMFS-style filesystem and adds shell/Bash-Tool
command routing so familiar tools work against indexed data:

```sh
cat /esfs/conversations/<id>            # read a document
cp updated.json /esfs/conversations/<id> # write it back to Elasticsearch
ls /esfs/conversations                  # stream document names
find /esfs                              # walk indices + documents
grep "red shoes" --since 7d /esfs/conversations   # ranked ES search
```

It is "Unix, but indexed": the **mount** handles all file I/O (read, write,
delete, editors, redirection), and a thin **routing** layer makes `ls`, `find`,
and `grep` Elasticsearch-aware while leaving the real system tools available.

## One mount, many indices

A single ESFS mount exposes **every visible index in the cluster** as a
top-level directory — you do not mount one index at a time:

```
/mnt/esfs/                     # mount root (any path; /esfs and /mnt/esfs both fine)
├── conversations/            # one directory per index/alias in the cluster
│   ├── <doc-id>
│   └── profile.md
├── orders/
│   └── <doc-id>
├── logs-2026.06/
│   └── <doc-id>
└── profile.md                # cluster-scope digest
```

- `cat /mnt/esfs/<index>/<doc>` works for **any** index in the cluster.
- `ls /mnt/esfs` lists all indices; `find /mnt/esfs` and `grep -r foo /mnt/esfs`
  span every visible index (no cross-index joins — each is searched in turn).
- System/hidden indices (leading `.`) are excluded by default.
- Restrict the set with an allowlist when you want fewer: `--index a,b,c` (or
  `visible_indices` in config). The default is **all visible indices**.
- The mountpoint is arbitrary: `--mount /mnt/esfs`, `--mount /esfs`, etc.

```sh
# Whole cluster under one mount:
esfs mount --mount /mnt/esfs

# Just two indices:
esfs mount --mount /mnt/esfs --index conversations,orders
```

## Two surfaces

| Surface | What it does | Commands |
|---------|-------------|----------|
| **FUSE mount** | Every file operation goes directly to Elasticsearch | `cat`, `>`, `cp`, `vim`, `rm`, editors |
| **Routed commands** | Reinterprets discovery and search intent as ES queries | `ls`, `find`, `grep` (when `esfs env` is active) |

`cat`/`cp`/`rm`/editors are **not** shimmed — the mount handles them natively.

## Install

Requires Go 1.26+ to build and a FUSE backend at runtime:

- Linux: `fuse3` / `fusermount3`
- macOS: FUSE-T (kextless, preferred) or macFUSE

Pick one:

```sh
# Homebrew (macOS), via the tap:
brew install --cask dbmurphy/tap/esfs

# Go toolchain (Linux + macOS):
go install github.com/dbmurphy/elasticsearch-filesystem/cmd/esfs@latest
go install github.com/dbmurphy/elasticsearch-filesystem/cmd/esfsd@latest
go install github.com/dbmurphy/elasticsearch-filesystem/cmd/esfs-grep@latest
go install github.com/dbmurphy/elasticsearch-filesystem/cmd/esfs-ls@latest
go install github.com/dbmurphy/elasticsearch-filesystem/cmd/esfs-find@latest

# Prebuilt binaries: download a tarball from the Releases page, or build:
make build            # -> ./bin/{esfs,esfsd,esfs-grep,esfs-ls,esfs-find}
make install          # -> /usr/local/bin (PREFIX overridable)
```

Full step-by-step (FUSE backend, binaries/Homebrew/source, services,
verification, uninstall): **[docs/INSTALL.md](docs/INSTALL.md)**.

Cross-compiled release tarballs for linux/darwin amd64/arm64:

```sh
make cross            # -> dist/
```

Homebrew formula template and service units are in `packaging/`.

## Configure

Config resolves from `$ESFS_CONFIG` (or `~/.config/esfs/config.json`), then env
overrides, then CLI flags. Durations are human strings (`"10s"`). See
`config.example.json` (minimal), `examples/config.full.json` (everything), and
**[docs/CONFIGURATION.md](docs/CONFIGURATION.md)** for the full reference.

```sh
export ESFS_ENDPOINT=https://localhost:9200
export ES_API_KEY=...          # referenced via api_key_env
```

Credentials come from env first, then OS keychain/Secret Service, then a
chmod-0600 config — never baked into units.

### Per-index search config

One mount spans many indices, and each can search differently. Configure how an
index maps to semantic vs lexical search, which fields to search, and its time
field — under `indices.<name>`:

```json
{
  "endpoint": "https://localhost:9200",
  "indices": {
    "conversations": {
      "time_field": "@timestamp",
      "search_fields": ["subject", "body"],
      "semantic_fields": ["body_semantic"],
      "grep_mode": "auto"
    },
    "logs-app": { "grep_mode": "lexical", "profile": false }
  }
}
```

Anything omitted inherits the global default. Each index's effective behavior is
shown at the top of its `profile.md` (see `examples/profile.index.md`).

## Use

```sh
# Mount the whole cluster (all visible indices) at /mnt/esfs (Ctrl-C to unmount):
esfs mount --mount /mnt/esfs

# Browse across indices:
ls /mnt/esfs                       # every index in the cluster
ls /mnt/esfs/conversations         # documents in one index
cat /mnt/esfs/orders/<id>          # a document from a different index

# In another shell, activate routed ls/find/grep:
eval "$(esfs env --mount /mnt/esfs)"
cd /mnt/esfs/conversations
ls
grep refund
grep "red shoes" --since 7d
grep -r refund /mnt/esfs            # search across all indices in the mount

# Exact system tools remain available:
/usr/bin/grep refund <id>

# Diagnose everything:
esfs doctor
```

Run the full parity loop end to end with `scripts/demo.sh`.

## Search semantics

`grep` inside ESFS is ranked Elasticsearch search:

- **Semantic** when the index has `semantic_text` fields (ES does query-time
  inference; no setup required).
- **Ranked lexical** (`simple_query_string`) otherwise — clearly labeled.
- **Diagnostic** if you force `--mode semantic` without semantic fields, or use
  `--since` without a resolvable date field.
- **Exact** byte-level fallback via `--exact`, `ESFS_GREP_MODE=exact`,
  `ESFS_STRICT_GREP=1`, or any unsupported flag.

Exit codes follow grep: `0` match, `1` no match, `2` error.

## Safety

- Writes are whole-document; default policy `safe-create-update` with optimistic
  concurrency. Invalid JSON is never synced.
- Delete is disabled by default (`--delete-sync` to enable).
- **No Elasticsearch-side setup**: no mappings, templates, pipelines, scripts,
  helper indices, saved searches, or stored digests. Profiles are local-only.

## How it works

```mermaid
flowchart TD
  userShell[Your shell / agent] --> esfsShell[ESFS shell integration<br/>eval &quot;$(esfs env)&quot;]
  userShell --> posixTools[cat / cp / vim / rm<br/>normal file I/O]
  esfsShell --> routedGrep[Routed ls / find / grep<br/>Elasticsearch search]
  posixTools --> fuseMount[FUSE mount]
  routedGrep --> esfsd
  fuseMount --> esfsd[esfsd — sync daemon<br/>read · write · delete · cache]
  esfsd --> queryCore[Shared query core<br/>ES client · PIT · field caps · planner]
  queryCore --> profiles[profile.md<br/>local digests]
  queryCore --> caches[Bounded caches<br/>docs · metadata · pages]
  queryCore --> elastic[(Elasticsearch)]
```

| Package | Role |
|---------|------|
| `cmd/esfs` | Control CLI — mount / env / path / doctor / grep |
| `cmd/esfsd` | Mount daemon for service managers (systemd, launchd) |
| `cmd/esfs-{grep,ls,find}` | Routed command shims |
| `internal/contract` | Path model, encoding, error ↔ errno mapping |
| `internal/escore` | ES client, catalog, doc store, query planner, PIT caches |
| `internal/vfs` | All filesystem semantics — unit-tested without a kernel mount |
| `internal/fusefs` | Thin `go-fuse` adapter over the VFS |
| `internal/cmdshim` | Routed ls/find/grep, exact fallback, passthrough |
| `internal/profile` | Local-only `profile.md` generation |
| `internal/cli` | `esfs` subcommands |

Docs:
- [docs/INSTALL.md](docs/INSTALL.md) — install (FUSE backend, binaries, services, verify, uninstall).
- [docs/CONFIGURATION.md](docs/CONFIGURATION.md) — full config reference (global + per-index).
- [docs/CONTRACTS.md](docs/CONTRACTS.md) — filesystem/path/error contract.
- [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) — Linux + real-ES Docker acceptance.
- [docs/BENCHMARKS.md](docs/BENCHMARKS.md) — performance methodology and gates.
- `examples/` — full config and rendered `profile.md` examples.

## Develop

```sh
make test        # unit + integration tests (no cluster/mount needed)
make vet
make bench       # ESFS-side overhead benchmarks
make acceptance  # Docker: Linux + real Elasticsearch end-to-end parity loop
```

## Releases & contributing

Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
(`feat`, `fix`, `docs`, `perf`, …); a PR check enforces this. On merge to `main`,
[release-please](https://github.com/googleapis/release-please) maintains a
release PR that bumps the version and updates `CHANGELOG.md`. Merging it tags the
release, and GoReleaser builds cross-platform binaries, checksums, and the
Homebrew formula automatically.

- `feat:` → minor, `fix:` → patch, `feat!:`/`BREAKING CHANGE:` → major.
- CI (`make test`, `vet`, build, and a real-Elasticsearch integration job) runs
  on every PR.

`make acceptance` is the repeatable, documentable end-to-end validation on Linux
against a real Elasticsearch node with seeded data (mount, `cat`, write-back,
`ls`/`find`/`grep`, `--since`, exact `/usr/bin/grep`, `profile.md`, and the
no-ES-side-setup guard). See `docs/ACCEPTANCE.md`.
