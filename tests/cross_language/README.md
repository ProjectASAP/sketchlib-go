# Cross-Language Integration Test

Verifies that all nine sketch types serialise correctly from **sketchlib-go** (Go producer)
and deserialise correctly in **sketchlib-rust** (Rust consumer) using the shared
protobuf wire format defined in `proto/sketchlib.proto`.

## What is tested

| File | Sketch type | Query checked |
|------|-------------|---------------|
| `countmin.pb` | CountMin (3 × 512, float64) | `freq("item:42") ≥ 101` |
| `kll.pb` | KLL quantile sketch (k=200) | `p50 ≈ 5000`, `p99 ≈ 9900` |
| `ddsketch.pb` | DDSketch (alpha=0.01) | `p50 ≈ 5000`, `p99 ≈ 9900` |
| `hll.pb` | HyperLogLog (DataFusion, p=14) | `cardinality ≈ 50 000` |
| `countsketch.pb` | CountSketch (3 × 512, float64) | `freq("cs:hot") ≥ 200` |
| `coco.pb` | CocoSketch (d=5, width=128) | `estimate("coco:hot") ≥ 500` |
| `elastic.pb` | ElasticSketch (64 heavy buckets) | `estimate("elephant") ≥ 900` |
| `univmon.pb` | UnivMon (8 layers, CS 3 × 2048) | `g-sum cardinality ∈ [1 000, 15 000]` |
| `hydra.pb` | HydraSketch (4 × 4 CM cells) | `freq("hydra:42") ≥ 51` |

## Directory layout

```
sketchlib-go/
  tests/cross_language/
    run_test.sh          ← orchestrates both phases
    producer_test.go     ← Go test that writes the 9 .pb files (no binary)
    README.md            ← this file

sketchlib-rust/
  tests/
    xtest_consumer.rs    ← Rust integration test that reads and verifies those files (no binary)
```

Neither side produces a standalone binary. The producer runs via `go test` and the
consumer runs via `cargo test`.

## Quick start

```bash
# From any directory inside the repo:
./tests/cross_language/run_test.sh

# Or from the sketchlib-go directory:
sketchlib-go/tests/cross_language/run_test.sh
```

### Options

| Variable | Default | Effect |
|----------|---------|--------|
| `KEEP_TMP=1` | off | Keep the `.pb` output directory after the test |
| `TMP_DIR=/some/path` | auto | Write `.pb` files to a specific directory |
| `VERBOSE=1` | off | Show full build and test output |

Examples:

```bash
# Keep the protobuf files for inspection
KEEP_TMP=1 ./run_test.sh

# Use a fixed output directory
TMP_DIR=/tmp/xtest ./run_test.sh

# Verbose output
VERBOSE=1 ./run_test.sh
```

## Running each phase independently

**Go producer only:**

```bash
cd sketchlib-go
XTEST_DIR=/tmp/mytest go test -v -run TestXtestProducer ./tests/cross_language/
```

**Rust consumer only** (requires `.pb` files to already exist):

```bash
cd sketchlib-rust
XTEST_DIR=/tmp/mytest cargo test --test xtest_consumer -- --nocapture
```

## How it works

```
Go producer (go test)            proto wire format           Rust consumer (cargo test)
──────────────────────────────────────────────────────────────────────────────────────
CountMinSketch.SerializePortable()  →  countmin.pb  →  decode + min-freq query
KLL.SerializePortable()             →  kll.pb       →  decode + quantile query
DDSketch.SerializePortable()        →  ddsketch.pb  →  decode + quantile query
HyperLogLog.SerializePortable()     →  hll.pb       →  decode + DataFusion estimate
CountSketch.SerializePortable()     →  countsketch.pb → decode + median signed freq
CocoSketch.SerializePortable()      →  coco.pb      →  decode + DeriveIndex lookup
ElasticSketch.SerializePortable()   →  elastic.pb   →  decode + heavy/light query
UnivSketch.SerializePortable()      →  univmon.pb   →  decode + g-sum cardinality
Hydra.SerializePortable()           →  hydra.pb     →  decode + xorshift route + CM min
```

### Hash compatibility

Both libraries use **xxHash3 64-bit with seed** (Go: `zeebo/xxh3`, Rust: `twox-hash XxHash3_64`)
with an identical 20-entry seed table. The canonical seed index is 5 (`0x6a09e667`).

Seed roles:

| Seed index | Value | Used by |
|------------|-------|---------|
| 0 | `0xcafe3553` | `Hash64` — CountMin, CountSketch, CocoSketch, UnivMon heap |
| 5 | `0x6a09e667` | `CanonicalHashSeed` — HLL insert, ElasticSketch |
| 6 | `0xbb67ae85` | `defaultHydraSeed` — Hydra cell routing |

### Protobuf schema

Each sketch type has its own `.proto` file under `proto/<sketch>/`. The envelope
`proto/sketchlib.proto` wraps all types via a `oneof`. Go regenerates per-package
`.pb.go` files via `protoc-gen-go`. Rust regenerates `sketchlib.v1.rs` at build
time via `prost-build` in `build.rs`.

## Adding a new sketch

1. Implement `SerializePortable() (*envpb.SketchEnvelope, error)` in the sketch's Go package.
2. Add a `.proto` file under `sketchlib-go/proto/<sketch>/` and a `oneof` arm in `proto/sketchlib.proto`.
3. Regenerate the Go and Rust proto bindings.
4. Add a producer section in `sketchlib-go/tests/cross_language/producer_test.go`.
5. Add a consumer section in `sketchlib-rust/tests/xtest_consumer.rs`.

## Running from CI

The script exits non-zero on any failure and is suitable for use as a CI step:

```yaml
# GitHub Actions example
- name: Cross-language integration test
  run: ./tests/cross_language/run_test.sh
```
