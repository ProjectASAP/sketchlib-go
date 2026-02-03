# `sketchlib-go`

`sketchlib-go` is a probabilistic sketch library written in Go, designed for
**high-throughput telemetry, streaming analytics, and OpenTelemetry-style aggregation**.

The library emphasizes a **clear separation between numeric sketch execution and semantic processing**, enabling:

* **Pre-hashed insertion (fast path)**
* **Single-hash multi-row derivation**
* **Zero-allocation hot paths**
* **Engine-style architecture (hashing outside sketches)**
* **Explicit separation between fast and slow paths**
* **Lazy and sampling-based Top-K optimization**

---

## Core Design Principles

This library follows several **strict, non-negotiable rules**:

1. **Hashes are computed exactly once**
2. **Sketches never perform hashing internally**
3. **All hot paths are allocation-free**
4. **Numeric sketches and semantic operators are decoupled**
5. **Top-K is a derived, key-aware structure**
6. **Slow paths are explicit and isolated**
7. **Optimizations are structural, not micro-optimizations**

---

## High-Level Execution Flow

```
User / OpenTelemetry
        ↓
common.FromXxx        (hash computed once)
        ↓
HashLayer.Insert
        ↓
Sketch.InsertWithHash   (FAST PATH: numeric execution only)
        ↓
(optional)
Semantic Layer          (SLOW PATH: Top-K, reporting, debugging)
```

> **Design invariant:**
> All high-throughput execution flows through `InsertWithHash`.

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

Hashing rules:

* `Hash64` based on `xxhash`
* No seeded hashing in hot paths
* **Hash computation is strictly forbidden inside sketches**

---

### 2. Core Sketches (`/sketches`)

This directory contains the probabilistic sketch implementations.

Implemented / planned sketches:

* Count-Min Sketch (CMS)
* Count Sketch (CS)
* HyperLogLog (HLL)
* KLL / Quantile Sketches

All sketches obey the same **execution contract**:

* ✅ Accept **precomputed hashes** via fast path
* ❌ Never compute hashes internally
* ❌ Never allocate memory in hot paths

#### Common Sketch Interface

```go
type Sketch interface {
	InsertWithHash(hash uint64)              // fast path
	QueryWithHash(q QueryType, hash uint64) (float64, error)
	Merge(other Sketch) error
	TypeName() string
}
```

This design enables:

* Predictable latency
* Safe fan-out insertion
* Easy integration into ingestion engines

---

### 3. Fast Path vs Slow Path

`sketchlib-go` explicitly distinguishes between **fast paths** and **slow paths**.

#### Fast Path (Execution Layer)

* Operates only on **pre-hashed input**
* Allocation-free
* Numeric only
* Target of all performance optimization

```go
sketch.InsertWithHash(hash)
```

#### Slow Path (Key-Aware / Semantic Layer)

Some operations inherently require **original keys**, for example:

* Top-K / Heavy Hitters
* Debugging and reporting
* Sampling-based analysis

These operations:

* Cannot rely on hashes alone
* Are explicitly isolated
* Delegate numeric work to fast paths

```go
// Slow path example
sketch.Insert(key)        // hashes internally, delegates
topk.Update(key, estimate)
```

> **Important:**
> Slow paths exist for correctness and semantics, not performance.

---

### 4. HashLayer (`/hashlayer`)

`HashLayer` is an **engine-level component**, not a user-facing façade.

Its sole responsibility:

> **Fan-out a single precomputed hash to multiple sketches**

```go
hl := hashlayer.New(cms, cs, hll)
hl.Insert(input) // hash computed once, reused everywhere
```

Design constraints:

* ❌ HashLayer does **not** compute hashes
* ❌ HashLayer does **not** execute queries
* ❌ HashLayer does **not** perform semantic logic
* ✅ Queries run **directly on sketches**

```go
freq, _ := cms.QueryWithHash(common.QueryFrequency, input.Hash)
```

This mirrors **engine-style ingestion pipelines** used in modern telemetry systems.

---

## Supported Sketches & Framework Capabilities

| Sketch / Framework | Type      | Aggregation Operations / Statistical Queries Supported                                                                       | Notes                                                                                                                      |
| ------------------ | --------- | ---------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| **CountMinSketch** | Sketch    | • Frequency (Point Query using Min)<br>• L1 Norm (Sum of counts)<br>• L2 Norm (Euclidean Norm)<br>• Sum (Min of sum columns) | Uses a conservative update strategy (never underestimates).                                                                |
| **CountSketch**    | Sketch    | • Frequency (Point Query using Median)<br>• L2 Norm (Euclidean Norm)<br>• Heavy Hitters / Top-K                              | Uses a median-based estimation to handle noise from collisions and tracks heavy hitters via a heap.                        |
| **CocoSketch**     | Sketch    | • Frequency (Flexible Aggregation)<br>• Supports: Raw, Sum, Median, Max                                                      | Allows selecting the aggregation strategy dynamically at query time.                                                       |
| **HyperLogLog**    | Sketch    | • Cardinality (Distinct Count)                                                                                               | Specialized for counting unique items using probabilistic registers.                                                       |
| **KLLSketch**      | Sketch    | • Quantile Estimation<br>• Rank Estimation<br>• CDF (Cumulative Distribution Function)<br>• Total Count (Stream length)      | Stores a compact distribution summary to answer percentile and rank queries.                                               |
| **UnivMon**        | Framework | • Entropy<br>• Cardinality<br>• Heavy Hitters / Top-K<br>• L2 Norm (Intermediate)                                            | “Universal Monitor” framework that layers multiple `CountSketchUniv` instances to compute complex statistics like entropy. |
| **HashLayer**      | Framework | • Dispatcher (Vector Result)                                                                                                 | Acts as a wrapper to broadcast queries to multiple sketches and return a vector of results (e.g., `[]float64`).            |

---

## Benchmarks (`/benchmark`)

Benchmarks validate that:

* Hashing is completely removed from hot paths
* Pre-hashed insertion provides measurable speedups
* No allocations occur during insert or query
* Slow paths are clearly isolated
* Accuracy is preserved across fast and slow paths

Representative results:

```
InsertWithHash               ~23 ns/op    0 allocs/op
Insert (slow path)           ~270 ns/op   1 alloc/op
Speedup (fast path)          ~10×
```

Benchmarks include:

* Fast path only
* Slow path only
* End-to-end (hash + fast path)
* Accuracy verification outside timed sections

---

## Sketch Characteristics

### Count-Min Sketch (CMS)

Used for approximate frequency estimation with one-sided error.

* Single-hash multi-row derivation
* Power-of-two width (bit masking)
* No underestimation guarantee
* Zero allocations in hot paths

---

### Count Sketch (CS)

Used for unbiased frequency estimation with signed updates.

* Pre-hashed fast path
* Median-of-rows estimator
* **Top-K handled externally (key-aware slow path)**

---

### HyperLogLog (HLL)

Used for approximate cardinality estimation.

* **Fast-path–only sketch**
* Fully hash-based (no key semantics)
* Slow path exists only as a wrapper for compatibility

---

### KLL Quantile Sketch

Used for approximate quantile estimation.

* Mergeable summaries
* Probabilistic rank guarantees
* Sublinear memory usage

---

## Implementation Status

### Status Legend

* ✅ Done
* 🟡 In Progress / Partial
* ❌ To Do

| Sketch            | Correctness | Performance | Current Optimization                                                 | Next Optimization                      |
| ----------------- | ----------- | ----------- | -------------------------------------------------------------------- | -------------------------------------- |
| Count-Min Sketch  | ✅           | ✅           | Prehashed insert<br>Single-hash derivation<br>No allocation          | Sharded CMS<br>Cache-line–aware layout |
| Count Sketch      | ✅           | ✅           | Prehashed insert<br>Median-of-rows estimator<br>Lazy Top-K threshold | Faster sign-bit derivation             |
| HyperLogLog       | ✅           | ✅           | Fast-path–only insert<br>Hash-based execution<br>No key semantics    | Register layout optimization           |
| KLL Quantile      | ✅           | ✅           | Prehashed insert<br>Merge correctness                                | Fixed buffer<br>Lazy compaction        |
| **UnivMon**       | 🟡          | 🟡          | Layered CountSketch execution                                        | Memory tuning<br>Query fusion          |
| **HydraSketch**   | ❌           | ❌           | —                                                                    | Design optimization                     |
| **ElasticSketch** | ❌           | ❌           | —                                                                    | Design optimization      |
