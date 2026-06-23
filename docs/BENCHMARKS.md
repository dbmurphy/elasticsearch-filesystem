# ESFS Benchmarks

ESFS performance has two layers. Measure them separately because cluster latency
dominates and would otherwise hide ESFS-side overhead.

## 1. ESFS-side overhead (no cluster, no mount)

Run:

```sh
make bench
# or
go test -run '^$' -bench . -benchmem ./internal/contract/ ./internal/escore/
```

These isolate the code ESFS adds on top of Elasticsearch: path encoding/decoding,
query planning, and streaming orchestration. Representative results on Apple
M-series (arm64):

| benchmark            | ns/op  | notes                              |
|----------------------|--------|------------------------------------|
| EncodeDecodeSimple   | ~60    | simple IDs stay verbatim           |
| EncodeDecodeEscaped  | ~425   | percent-escaped IDs                |
| ParsePath            | ~780   | full path classification           |
| PlanQuery            | ~835   | grep + --since -> ES DSL           |
| ListStream (10k)     | ~2.4ms | ~240 ns/doc (~4M docs/sec)         |
| SearchStream (10k)   | ~3.2ms | substring fake matcher             |

The list/search throughput is far above the plan's gates (1,000 paths/sec Linux,
500/sec macOS) because those gates are network-bound; ESFS-side cost is not the
bottleneck.

## 2. End-to-end mount benchmarks (FUSE backend + Elasticsearch)

Run on a host with fuse3 (Linux) or macFUSE/FUSE-T (macOS) and a reachable
cluster:

```sh
ES_ENDPOINT=https://localhost:9200 ES_API_KEY=... ESFS_MOUNT=/esfs ./scripts/bench.sh
```

The harness records environment metadata (OS, mount backend, ES version) and
times `cat`, `ls`, and `find` against a live mount.

### Performance gates (from the plan)

Measured as ESFS overhead over the equivalent direct ES API call, plus
end-to-end first-result latency:

- `cat <id>`: p95 overhead ≤ 50ms (Linux) / ≤ 100ms (macOS).
- write flush: p95 overhead ≤ 100ms (Linux) / ≤ 200ms (macOS), excluding ES
  indexing latency.
- `ls`/`find`: first result p95 ≤ 300ms after ES responds; ≥ 1,000 paths/sec
  (Linux) / 500/sec (macOS) sustained.
- routed `grep` (incl. `--since`): first result p95 ≤ 500ms (Linux) /
  ≤ 750ms (macOS); range filters run in ES filter context.
- `profile.md`: p95 ≤ 1s cached / ≤ 5s cold.
- steady-state daemon memory < 512MB for a 1M-doc traversal (streaming + bounded
  caches).

## Backend comparison (macOS)

The plan requires choosing one macOS backend that passes parity + gates before
release. Compare with the same harness under each backend:

- macFUSE FSKit (kextless; mount under `/Volumes`),
- FUSE-T (kextless; NFS/SMB-backed),
- macFUSE kernel backend (opt-in; best performance/compat).

Record the backend in the harness output and select per measured latency,
install friction, and correctness.
