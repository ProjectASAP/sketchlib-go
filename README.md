# `sketchlib-go`

`sketchlib-go` is a probabilistic sketch library written in Go, designed for
**high-throughput telemetry, streaming analytics, and OpenTelemetry-style aggregation**.

The library is built around a **clear separation between numeric sketching and semantic processing**, enabling:

* **Prehashed insertion**
* **Single-hash multi-row derivation**
* **Zero-allocation hot paths**
* **Engine-style architecture (hashing outside sketches)**
* **Lazy and sampling-based Top-K optimization**

---

## Core Design Principles

This library follows several **strict, non-negotiable rules**:

1. **Hashes are computed exactly once**
2. **Sketches never perform hashing**
3. **Hot paths are allocation-free**
4. **Numeric sketches and semantic operators are decoupled**
5. **Top-K is a derived structure, not a counting primitive**
6. **Optimizations are structural, not micro-optimizations**

High-level flow:

```
User / OpenTelemetry
        ↓
common.FromXxx   (hash computed once)
        ↓
HashLayer.Insert
        ↓
Sketch.InsertWithHash   (fast path, numeric only)
        ↓
(optional)
Semantic Layer (Top-K, reporting, debugging)
```

---

## Repository Structure

### 1. Common Building Blocks (`/common`)

The `common` package provides shared primitives used by all sketches.

#### SketchInput

`SketchInput` is a normalized input container that:

* Stores a canonical byte representation of the input
* Computes and caches a **64-bit hash exactly once**
* Allows the same hash to be reused across multiple sketches

```go
input := common.FromString("user_id")
hash := input.Hash // computed once, reused everywhere
```

Hashing utilities:

* `Hash64` based on `xxhash`
* No seeded hashing in hot paths
* Hash computation is **strictly forbidden inside sketches**

---

### 2. Core Sketches (`/sketches`)

This directory contains the probabilistic sketch implementations.

Implemented / planned sketches:

* Count-Min Sketch (CMS)
* Count Sketch (CS)
* HyperLogLog (HLL)
* KLL / Quantile Sketches

All sketches obey the same **strict contract**:

* ✅ Accept **precomputed hashes**
* ❌ Never compute hashes internally
* ❌ Never allocate memory in hot paths

Common sketch interface:

```go
type Sketch interface {
	InsertWithHash(hash uint64)
	QueryWithHash(q QueryType, hash uint64) (float64, error)
	Merge(other Sketch) error
	TypeName() string
}
```

This design enables:

* Predictable latency
* Easy fan-out insertion
* Safe reuse in high-throughput engines

---

### 3. HashLayer (`/hashlayer`)

`HashLayer` is an **engine-level component**, not a user-facing façade.

Its sole responsibility is:

> **Fan-out insertion of a precomputed hash to multiple sketches**

```go
hl := hashlayer.New(cms, cs, hll)
hl.Insert(input) // hash computed once, reused by all sketches
```

Design constraints:

* ❌ HashLayer does **not** compute hashes
* ❌ HashLayer does **not** execute queries
* ❌ HashLayer does **not** perform semantic logic
* ✅ Queries are executed **directly on sketches**

Example query:

```go
freq, _ := cms.QueryWithHash(common.QueryFrequency, input.Hash)
```

This mirrors the **engine-style HashLayer** used in the Rust implementation and modern telemetry pipelines.

---

### 4. Benchmarks (`/benchmark`)

Benchmarks validate that:

* Hashing is completely removed from hot paths
* Prehashed insertion provides measurable speedups
* No allocations occur during insert or query
* Slow paths (string-based APIs, Top-K) are clearly separated

Representative results:

```
InsertWithHash               ~23 ns/op   0 allocs/op
UpdateString (end-to-end)    ~270 ns/op  1 alloc/op
Speedup (fast path)          ~10×
```

Pipeline benchmarks further demonstrate the benefit of reusing a single hash across multiple sketches.

---

## Sketch Characteristics

### Count-Min Sketch (CMS)

Used for approximate frequency estimation with one-sided error.

Key characteristics:

* Single-hash multi-row derivation
* Power-of-two width (bit masking instead of modulo)
* No underestimation guarantee
* Zero allocations in hot paths

---

### Count Sketch (CS)

Used for unbiased frequency estimation with signed updates.

Current optimizations:

* Prehashed insert path
* Median-of-rows estimator

Planned optimizations:

* Faster sign-bit derivation
* Optimize Top-K path in UnivMon

---

### KLL Quantile Sketch

Used for approximate quantile and distribution estimation.

Key characteristics:

* Mergeable summaries
* Probabilistic rank error bounds
* Sublinear memory usage

Planned optimizations:

* Fixed-size buffers
* Improved merge efficiency

---

## Implementation Status

### Status Legend

* ✅ Done
* 🟡 In Progress / Partial
* ❌ To Do

### Sketch Status Table

| Sketch           | Correctness | Performance | Current Optimization                                        | Next Optimization                         |
| ---------------- | ----------- | ----------- | ----------------------------------------------------------- | ----------------------------------------- |
| Count-Min Sketch | ✅           | ✅           | Prehashed insert<br>Single-hash derivation<br>No allocation | Sharded CMS<br>Cache-line layout          |
| Count Sketch     | ✅          | ✅          | Prehashed insert<br>Lazy Top-K threshold                    | Sign-bit derivation<br>Power-of-two width |
| HyperLogLog      | 🟡          | 🟡          | Prehashed insert path                                       | Bias correction<br>Register optimization  |
| KLL Quantile     | ✅          | ✅          | Prehashed insert<br>Merge correctness                       | Fixed buffer<br>Lazy compaction           |


