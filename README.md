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

* ❌ Does **not** compute hashes
* ❌ Does **not** execute queries
* ❌ Does **not** perform semantic logic
* ✅ Queries run **directly on sketches**

---

## Supported Sketches & Framework Capabilities

| Sketch / Framework       | Type      | Aggregation Operations / Statistical Queries Supported                                                                       | Notes                                                                           |
| ------------------------ | --------- | ---------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| **CountMinSketch**       | Sketch    | • Frequency (Point Query using Min)<br>• L1 Norm (Sum of counts)<br>• L2 Norm (Euclidean Norm)<br>• Sum (Min of sum columns) | Uses a conservative update strategy (never underestimates).                     |
| **CountSketch**          | Sketch    | • Frequency (Point Query using Median)<br>• L2 Norm (Euclidean Norm)<br>• Heavy Hitters / Top-K                              | Median-based estimation to handle collision noise; Top-K tracked via heap.      |
| **CocoSketch**           | Sketch    | • Frequency (Flexible Aggregation)<br>• Supports: Raw, Sum, Median, Max                                                      | Aggregation strategy selectable at query time.                                  |
| **HyperLogLog**          | Sketch    | • Cardinality (Distinct Count)                                                                                               | Specialized probabilistic sketch for unique counting.                           |
| **KLLSketch**            | Sketch    | • Quantile Estimation<br>• Rank Estimation<br>• CDF<br>• Total Count                                                         | Compact summary for percentile and rank queries.                                |
| **ExponentialHistogram** | Sketch    | • Bucket-based Distribution<br>• Quantile Estimation<br>• Cumulative Bucket Counts<br>• Scale Adjustment                     | OpenTelemetry-compatible exponential bucketing with adaptive precision.         |
| **UnivMon**              | Framework | • Entropy<br>• Cardinality<br>• Heavy Hitters / Top-K<br>• L2 Norm (Intermediate)<br>• Frequency Estimation                  | Universal Monitor using layered CountSketchUniv for multi-metric queries.       |
| **HydraSketch**          | Framework | • Adaptive Sketch Selection<br>• Multi-sketch Composition<br>• Unified Query Interface<br>• Dynamic Strategy Switching       | Adaptive framework that selects optimal sketches based on workload and data.    |
| **HashLayer**            | Framework | • Dispatcher (Vector Result)                                                                                                 | Broadcasts inserts and queries to multiple sketches and returns vector results. |

---

## Sketch Characteristics (Framework & Advanced Sketches)

### Exponential Histogram (EH)

**Design & Execution**

* Implements **exponential bucket boundaries** as defined in OpenTelemetry metrics
* Buckets are organized by scale, enabling logarithmic growth in range
* Pre-hashed fast path ensures **zero allocation** during insertion
* Bucket index and scale are derived directly from the hash and value

**Supported Queries**

* Bucket-based distribution representation
* Quantile estimation over exponential buckets
* Cumulative bucket counts
* Dynamic scale inspection and adjustment

**Architectural Notes**

* Numeric-only sketch with no key semantics
* Designed for high-throughput telemetry ingestion
* Can be merged efficiently across shards and time windows
* Acts as a foundational building block for histogram-style metrics

**Typical Use Cases**

* Latency and duration distributions
* Size and magnitude metrics with large value variance
* Drop-in replacement for OpenTelemetry exponential histograms

---

### UnivMon

A **multi-layer sketch framework** for computing diverse statistical metrics from a shared structure.

**Design & Execution**

* Built on a **layered CountSketch architecture**
* Each layer captures different frequency scales
* Uses shared pre-hashed insertion across all layers
* Numeric execution is fully decoupled from semantic queries

**Supported Metrics**

* Frequency estimation
* Cardinality estimation
* Entropy computation
* Heavy hitters / Top-K detection
* Intermediate L2 norm computation for derived metrics

**Architectural Notes**

* Avoids duplicating sketches per metric
* Enables efficient multi-metric queries from a single ingestion stream
* Error bounds are controlled by the number of layers and sketch width
* Designed as a *framework*, not a standalone sketch

**Typical Use Cases**

* Network traffic analysis
* Telemetry pipelines requiring entropy and heavy hitters
* Streaming analytics with mixed statistical queries

---

### HydraSketch

An **adaptive multi-sketch composition framework** designed for heterogeneous and evolving query workloads.

**Design Philosophy**

* No single sketch is optimal for all data distributions or queries
* Sketch selection should adapt dynamically to workload characteristics

**Design & Execution**

* Composes multiple sketches under a unified interface
* Uses pre-hashed insertion to fan-out data efficiently
* Supports **runtime strategy switching** at query time
* Separates cost modeling, execution, and query semantics

**Core Capabilities**

* Adaptive sketch selection based on data and query patterns
* Unified query interface across heterogeneous sketches
* Dynamic strategy switching without reinsertion
* Cost–benefit optimization for performance vs accuracy trade-offs

**Architectural Notes**

* Generalizes concepts from UnivMon and ElasticSketch
* Treats sketches as interchangeable execution units
* Designed as a research-oriented framework for adaptive sketching

**Typical Use Cases**

* Mixed workloads (frequency + cardinality + quantiles)
* Systems with evolving query distributions
* Experimentation with adaptive sketch strategies

---

## Implementation Status

### Status Legend

* ✅ Done
* 🟡 In Progress / Partial
* ❌ To Do

| Sketch                | Correctness | Performance | Current Optimization                                                 | Next Optimization                      |
| --------------------- | ----------- | ----------- | -------------------------------------------------------------------- | -------------------------------------- |
| Count-Min Sketch      | ✅           | ✅           | Prehashed insert<br>Single-hash derivation<br>No allocation          | Sharded CMS<br>Cache-line–aware layout |
| Count Sketch          | ✅           | ✅           | Prehashed insert<br>Median-of-rows estimator<br>Lazy Top-K threshold | Faster sign-bit derivation             |
| HyperLogLog           | ✅           | ✅           | Fast-path–only insert<br>Hash-based execution                        | Register layout optimization           |
| KLL Quantile          | ✅           | ✅           | Prehashed insert<br>Merge correctness                                | Fixed buffer<br>Lazy compaction        |
| Exponential Histogram | ✅           | ✅           | Exponential bucketing<br>Scale adjustment<br>No allocation           | Bucket compression<br>Adaptive scaling |
| UnivMon               | ✅           | ✅          | Layered CountSketch<br>Multi-metric support                          | Memory tuning<br>Query fusion          |
| HydraSketch           | ✅          | ✅          | Framework design<br>Sketch composition                               | Strategy optimization<br>Cost modeling |
| ElasticSketch         | ❌           | ❌           | —                                                                    | Design optimization                    |

