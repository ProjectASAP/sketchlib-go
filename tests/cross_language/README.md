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
    run_test.sh     ← orchestrates build + run of both sides
    README.md       ← this file
  cmd/xtest_producer/
    main.go         ← Go binary that writes the 9 .pb files

sketchlib-rust/
  src/bin/
    xtest_consumer.rs ← Rust binary that reads and verifies those files
```

## Quick start

```bash
# From any directory inside the repo:
sketchlib-go/tests/cross_language/run_test.sh

# Or from this directory:
cd sketchlib-go/tests/cross_language
./run_test.sh
```

### Options

| Variable | Default | Effect |
|----------|---------|--------|
| `KEEP_TMP=1` | off | Keep the `.pb` output directory after the test |
| `TMP_DIR=/some/path` | auto | Write `.pb` files to a specific directory |
| `VERBOSE=1` | off | Show full build output |

Examples:

```bash
# Keep the protobuf files for inspection
KEEP_TMP=1 ./run_test.sh

# Use a fixed output directory
TMP_DIR=/tmp/xtest ./run_test.sh

# Verbose build output
VERBOSE=1 ./run_test.sh
```

## How it works

```
Go producer                  proto wire format           Rust consumer
─────────────────────────────────────────────────────────────────────
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

`proto/sketchlib.proto` is shared between both libraries. Go regenerates
`sketchlib-go/proto/sketchlibpb/sketchlib.pb.go` via `protoc-gen-go`.
Rust regenerates `sketchlib.v1.rs` at build time via `prost-build` in `build.rs`.

## Adding a new sketch

1. Implement `SerializePortable() (*pb.SketchEnvelope, error)` in the sketch's Go package.
2. Add a message and oneof arm to `proto/sketchlib.proto`.
3. Regenerate the Go and Rust proto bindings.
4. Add a producer section in `cmd/xtest_producer/main.go`.
5. Add a consumer section in `sketchlib-rust/src/bin/xtest_consumer.rs`.

## Running from CI

The script exits non-zero on any failure and is suitable for use as a CI step:

```yaml
# GitHub Actions example
- name: Cross-language integration test
  run: sketchlib-go/tests/cross_language/run_test.sh
```
