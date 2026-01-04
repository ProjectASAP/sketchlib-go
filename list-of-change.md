# Count-Min Sketch API Refactor — Design Summary

## 1. Motivation

The Count-Min Sketch implementation was refactored to:

* Separate **input handling**, **hashing**, and **sketch logic**
* Enable **hash-once-use-many** across multiple sketches
* Provide a **clean, minimal, and stable sketch interface**
* Improve **performance, testability, and scalability**

---

## 2. Key Architectural Change

### Before (Old Design)

* Sketches accepted raw inputs (`string`, `[]byte`, etc.)
* Each sketch performed its own hashing
* Hashing logic was duplicated

```
Input → Sketch → Hash → Update
```

---

### After (New Design)

* Input normalization and hashing are **moved outside** the sketch
* Sketches only operate on **precomputed hashes**
* Hash is computed **once** and reused

```
Input → SketchInput → Hash64 → HashLayer → Sketch
```

---

## 3. Input Model (New Files)

### `common/SketchInput`

All user inputs are normalized before reaching sketches.

Supported input types:

* `string`
* `uint64`
* `float64`
* `[]byte`
* (any type convertible to canonical bytes)

```go
type SketchInput struct {
    Bytes []byte
    Hash  uint64
}
```

Responsibilities:

* Canonical byte normalization
* Single hash computation (`Hash64`)
* Input immutability and reuse

---

## 4. Sketch Contract (Core API)

### `common.Sketch` (Final Interface)

```go
type Sketch interface {
    InsertWithHash(hash uint64)
    QueryWithHash(q QueryType, hash uint64) (float64, error)
    Merge(other Sketch) error
    TypeName() string
}
```

Key properties:

* Sketches **never hash**
* Sketches **never see raw input**
* Sketches only operate on `uint64` hashes

This contract is minimal, stable, and shared by all sketches.

---

## 5. HashLayer (New Core Component)

### Purpose

`HashLayer` coordinates one or more sketches by:

* Computing the hash once
* Dispatching the same hash to multiple sketches

```go
hl.Insert(input)
```

Benefits:

* Eliminates redundant hashing
* Enables efficient multi-sketch fan-out
* Centralizes input handling

---

## 6. Count-Min Sketch Changes

### What Was Removed

* Internal hashing
* Seeds and hash functions
* String / byte input handling

### What Was Kept

* `Count`, `Sum`, `Sum2`
* `L1`, `L2` norms
* Merge semantics
* Statistical correctness

### Insert Semantics (Final)

```go
func (s *CountMinSketch) InsertWithHash(h uint64)
```

* Count is incremented by 1
* Sum and Sum2 track count-based aggregation
* Norms are updated per row
* No allocations, no hashing

---

## 7. API Usage Pattern (User-Facing)

### Typical Usage

```go
in := common.FromString("user-id")

hl := hashlayer.New(cms)
hl.Insert(in)

freq, _ := cms.QueryWithHash(common.QueryFrequency, in.Hash)
```

Key rules:

* Users never call sketch insert directly with raw input
* `SketchInput` + `HashLayer` is the default path
* Sketch API is hash-only

---

## 8. Testing Strategy

### Unit Tests (`TestXxx`)

Purpose:

* Validate correctness, not performance

What is tested:

* No underestimation
* Determinism
* Monotonicity
* Correct L1 / L2 accounting
* Merge correctness
* Compatibility with old CMS semantics

---

## 9. Benchmark Strategy

Benchmarks are explicitly separated by concern.

### Micro Benchmarks

* `Benchmark_SketchOnly`
* Measures pure sketch update cost
* No input, no hashing, no allocations

### End-to-End Benchmarks

* `Benchmark_EndToEnd_UserAPI`
* `Benchmark_EndToEnd_SkewedKeys`
* `Benchmark_EndToEnd_MultiSketch`
* `Benchmark_EndToEnd_Parallel`

These reflect **real user API usage**:

```
Input → Hash → HashLayer → Sketch
```

Metrics reported:

* `ns/op` (intrinsic cost per operation)
* `allocs/op` (memory behavior)

---

## 10. Benchmark Interpretation

* `ns/op` measures intrinsic CPU cost (portable, stable)
* Throughput (`ops/sec`) is derived from `ns/op`
* Skewed workloads are faster due to cache locality
* Multi-sketch benchmarks demonstrate hash-once-use-many benefits

---

## 11. Final Design Principles

* **Sketches are pure statistical data structures**
* **Hashing is external and shared**
* **Interfaces are minimal and future-proof**
* **Correctness and performance are measured separately**
* **Benchmarks reflect real user behavior**

