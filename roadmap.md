# sketchlib-go Roadmap

This roadmap summarizes:

- Features already implemented in `sketchlib-go`
- Prioritized features planned for implementation

## Current Status (Already Implemented)

### Core Architecture

- [x] Hash-once ingestion model via `common.SketchInput` (`Bytes` + cached `Hash`)
- [x] Unified sketch contract via `common.Sketch`:
  - [x] `InsertWithHash`
  - [x] `QueryWithHash`
  - [x] `Merge`
  - [x] `TypeName`
- [x] Shared `QueryType` enum (frequency/sum/sum2/cardinality/quantile)

### Implemented Sketches

- [x] CountMinSketch
- [x] CountSketch
- [x] CocoSketch
- [x] HyperLogLog (HLL)
- [x] KLL
- [x] DDSketch

### Implemented Frameworks

- [x] HashLayer (single hash fan-out to multiple sketches)
- [x] UnivMon
- [x] HydraSketch
- [x] ExponentialHistogram
- [x] ElasticSketch

### Quality and Tooling

- [x] Unit tests across common modules, sketches, and frameworks
- [x] Benchmark suite in `benchmark/`

---

## Prioritized Roadmap

## P0 (High Priority, Next Cycle)

These items unlock immediate architecture and production-readiness improvements.

- [ ] Serialization/Deserialization Layer
  - [ ] Add standard encode/decode support for all core sketches and frameworks
  - [ ] Recommended formats: MessagePack first, optional JSON/Protobuf adapters
  - [ ] Goal: checkpointing, network transfer, cross-service reuse

- [ ] API Maturity and Compatibility Matrix
  - [ ] Document supported query types per sketch/framework
  - [ ] Introduce feature maturity labels: `stable`, `experimental`, `deprecated`
  - [ ] Goal: reduce integration ambiguity and runtime misuse

- [ ] Correctness and Regression Hardening
  - [ ] Add stricter correctness tests and merge invariants
  - [ ] Add distribution-based tests (uniform + Zipf-like inputs)
  - [ ] Goal: confidence in error bounds and behavior under skewed data

## P1 (Medium Priority, Core Capability Expansion)

These items close major feature gaps relative to richer sketch runtimes.

- [ ] Nitro Sampling Layer
  - [ ] Add streaming and batch-oriented Nitro-style wrappers for frequency sketches
  - [ ] Goal: improve throughput in very high-ingestion workloads

- [ ] Tumbling Window Framework
  - [ ] Add a reusable tumbling window wrapper for windowed sketch aggregation
  - [ ] Goal: simplify time-window analytics use cases

- [ ] Folded Sketch Variants
  - [ ] Implement memory-efficient folded variants:
    - [ ] FoldCMS
    - [ ] FoldCS
  - [ ] Goal: support sub-window aggregation with tighter memory budget

- [ ] Additional Sketches
  - [ ] Add:
    - [ ] KMV
    - [ ] UniformSampling
    - [ ] MicroScope
    - [ ] Locher
  - [ ] Goal: broaden algorithm coverage for specialized workloads

## P2 (Lower Priority, Advanced Architecture)

These items improve long-term extensibility and optimization.

- [ ] Orchestrator Layer
  - [ ] Build node-based orchestration for composition across HashLayer, EH, sampling, and sketch families
  - [ ] Goal: declarative data/control flow for multi-sketch pipelines

- [ ] Advanced Hashing Modes
  - [ ] Extend hashing strategy beyond current 64-bit packed flow
  - [ ] Add adaptive modes for larger row/column settings (e.g., 128-bit or row-wise fallback)
  - [ ] Goal: preserve fast-path behavior for larger sketch dimensions

- [ ] Multi-Variant HyperLogLog
  - [ ] Add multiple HLL estimator variants (classic + improved estimators)
  - [ ] Goal: provide accuracy/performance tradeoff options per use case

- [ ] Storage Abstraction for Matrix/Vector Backends
  - [ ] Introduce reusable storage interfaces to reduce duplicate logic across CMS/CS-like sketches
  - [ ] Goal: easier optimization and maintenance

---

## Delivery Milestones

### Milestone A
- [ ] P0 complete
- [ ] Output: stable API docs + serde support + stronger correctness baseline

### Milestone B
- [ ] P1 complete
- [ ] Output: expanded functionality for sampling/window/folding workloads

### Milestone C
- [ ] P2 complete
- [ ] Output: orchestrated architecture and advanced optimization path

