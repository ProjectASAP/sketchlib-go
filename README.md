# sketchlib-go

`sketchlib-go` is a probabilistic sketch library written in Go, designed for  
**high-throughput telemetry, streaming analytics, and OpenTelemetry-style aggregation**.

The library emphasizes:
- **Prehashed insertion**
- **Single-hash multi-row derivation**
- **Zero-allocation hot paths**
- **Engine-style architecture (hashing outside sketches)**

---

## Repository Structure

The repository is organized into the following core components:

### 1. Common Building Blocks (`/common`)

The `common` package provides shared primitives used by all sketches:

- **SketchInput**
  - Normalizes input values
  - Computes and caches a **64-bit hash exactly once**
- **Hash utilities**
  - `Hash64` based on `xxhash`
  - Hashing is never performed inside sketches
- **Sketch interface**
  - Enforces a strict prehashed-insertion contract

Example:

```go
input := common.FromString("user_id")
hash := input.Hash // computed once and reused
````

---

### 2. Core Sketches (`/sketches`)

This directory contains the probabilistic sketch implementations.

Implemented / planned sketches:

* Count-Min Sketch (CMS)
* Count Sketch (CS)
* HyperLogLog (HLL)
* KLL / Quantile Sketches

All sketches follow the same rules:

* **They accept hashes**
* **They never compute hashes**
* **They allocate nothing in the hot path**

Common sketch contract:

```go
type Sketch interface {
	InsertWithHash(hash uint64)
	QueryWithHash(q QueryType, hash uint64) (float64, error)
	Merge(other Sketch) error
	TypeName() string
}
```

---

### 3. HashLayer (`/hashlayer`)

`HashLayer` is an **engine-level component**, not a user-facing façade.

Its responsibility is **strictly limited** to:

> Distributing a precomputed hash to multiple sketches (fan-out insertion).

```go
hl := hashlayer.New(cms, cs, hll)
hl.Insert(input) // hash computed once, reused by all sketches
```

Design constraints:

* ❌ HashLayer does **not** compute hashes
* ❌ HashLayer does **not** perform queries
* ✅ Queries are executed **directly on sketches**

Example query:

```go
freq, _ := cms.QueryWithHash(common.QueryFrequency, input.Hash)
```

This design mirrors the **engine-style HashLayer** used in the Rust implementation.

---

### 4. Benchmarks (`/benchmark`)

Benchmarks are designed to validate that:

* Hashing is completely removed from the hot path
* Prehashed insertion provides measurable speedups
* No allocations occur during insert or query

Representative results:

```
InsertPrehashed              ~23 ns/op   0 allocs/op
InsertWithHashingInLoop      ~52 ns/op   2 allocs/op
Speedup                      ~2.25×
```

---

## API Overview

### SketchInput

`SketchInput` is a normalized input container that:

* Stores a byte representation of the input
* Stores a **precomputed 64-bit hash**

```go
input := common.FromU64(42)
fmt.Println(input.Hash)
```

All hashing occurs **here**, never inside sketches.

---

### Count-Min Sketch (CMS)

Count-Min Sketch is used for approximate frequency estimation.

Initialization:

```go
cms, _ := countminsketch.NewCountMinSketch(5, 1024)
```

Insertion (via HashLayer):

```go
input := common.FromString("event")
hl.Insert(input)
```

Query (directly on the sketch):

```go
est, _ := cms.QueryWithHash(common.QueryFrequency, input.Hash)
```

Key characteristics:

* Single-hash multi-row derivation
* Power-of-two width
* Bit masking instead of modulo
* Zero allocations in hot paths

---

## Architectural Decisions

This library follows several strict architectural rules:

1. **Hashes are computed exactly once**
2. **Sketches never perform hashing**
3. **HashLayer is insert-only**
4. **Queries are executed directly on sketches**
5. **Seeded hashing (`HashIt`) is not used in hot paths**
6. **Optimizations are structural, not micro-optimizations**

High-level flow:

```
User / OpenTelemetry
        ↓
common.FromXxx   (hash computed once)
        ↓
HashLayer.Insert
        ↓
Sketch.InsertWithHash
```

---

## Implementation Status

### Status Legend

* ✅ Done
* 🟡 In Progress / Partial
* ❌ To Do

### Sketch Status Table

| Sketch              | Correctness | Performance | Current Optimization                                        | Next Optimization                         |
| ------------------- | ----------- | ----------- | ----------------------------------------------------------- | ----------------------------------------- |
| Count-Min Sketch    | ✅           | ✅           | Prehashed insert<br>Single-hash derivation<br>No allocation | Sharded CMS<br>Cache-line layout          |
| Count Sketch        | 🟡          | 🟡          | Prehashed insert                                            | Sign-bit derivation<br>Power-of-two width |
| HyperLogLog         | 🟡          | 🟡          | Prehashed insert path                                       | Bias correction<br>Register optimization  |
| KLL Quantile        | 🟡          | 🟡          | Prehashed insert                                            | Fixed buffer<br>Lazy compaction           |

---


