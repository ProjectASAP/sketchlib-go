# OctoSketch Architecture

```mermaid
flowchart TD
    %% ── Stream input ──────────────────────────────────────────────────────────
    STREAM(["Stream Items\n(SketchInput)"])

    %% ── Input channels ────────────────────────────────────────────────────────
    IC0["inputCh[0]\nbuf=1024"]
    IC1["inputCh[1]\nbuf=1024"]
    ICN["inputCh[N-1]\nbuf=1024"]

    STREAM -->|"shard by\nworker id"| IC0
    STREAM --> IC1
    STREAM --> ICN

    %% ── Workers ───────────────────────────────────────────────────────────────
    subgraph W0["Worker 0  (goroutine)"]
        direction TB
        LS0["Local CellSketch\n(CountMin / CountSketch\n/ HLL / DDSketch)"]
        PR0["Process(input)\n1. UpdateCell(row, col)\n2. ShouldEmit(newVal, τ)?\n3. BuildDelta → emitFn\n4. ResetCell"]
        FL0["Flush()\ndrain sub-τ cells\nat end-of-stream"]
        LS0 --> PR0 --> FL0
    end

    subgraph W1["Worker 1  (goroutine)"]
        direction TB
        LS1["Local CellSketch"] --> PR1["Process"] --> FL1["Flush"]
    end

    subgraph WN["Worker N-1  (goroutine)"]
        direction TB
        LSN["Local CellSketch"] --> PRN["Process"] --> FLN["Flush"]
    end

    IC0 --> W0
    IC1 --> W1
    ICN --> WN

    %% ── AdaptiveTau ───────────────────────────────────────────────────────────
    subgraph TAU["AdaptiveTau  (atomic.Uint64)"]
        direction LR
        TAU_VAL["τ  (current value)\nFixed: τ = const\nAdaptive: τ ∈ [Min, Max]"]
    end

    TAU -->|"Current()  atomic load\nper insert"| W0
    TAU --> W1
    TAU --> WN

    %% ── TauController ─────────────────────────────────────────────────────────
    subgraph TC["TauController  (goroutine)"]
        direction TB
        TICK["ticker  (50 ms default)"]
        ADJ["Adjust(queueLen)\nif depth > UpperBound → τ += Step\nif depth < LowerBound → τ -= Step"]
        TICK --> ADJ
    end

    ADJ -->|"Adjust()"| TAU

    %% ── Batch channels & recycle pools ────────────────────────────────────────
    BC0["batchCh[0]\ndeltaBatch[]"]
    BC1["batchCh[1]\ndeltaBatch[]"]
    BCN["batchCh[N-1]\ndeltaBatch[]"]

    RC0["recycleCh[0]\n(pool of pre-alloc\ndeltaBatch bufs)"]
    RC1["recycleCh[1]"]
    RCN["recycleCh[N-1]"]

    W0 -->|"emitDelta → batch\n(256 deltas/batch)"| BC0
    W1 --> BC1
    WN --> BCN

    BC0 -->|"recycle after drain"| RC0
    BC1 --> RC1
    BCN --> RCN

    RC0 -->|"fresh buffer"| W0
    RC1 --> W1
    RCN --> WN

    TC -->|"len(batchCh[i])\nqueue depth probe"| BC0
    TC --> BC1
    TC --> BCN

    %% ── Sharded aggregator goroutines ─────────────────────────────────────────
    subgraph AGG["Aggregator  (sharded, N goroutines + 1 merge)"]
        direction TB

        subgraph SH0["Shard goroutine 0"]
            SS0["ShardSketch[0]"]
            MD0["MergeDelta(Δ)\nfor each delta in batch"]
            SS0 --- MD0
        end

        subgraph SH1["Shard goroutine 1"]
            SS1["ShardSketch[1]"] --- MD1["MergeDelta(Δ)"]
        end

        subgraph SHN["Shard goroutine N-1"]
            SSN["ShardSketch[N-1]"] --- MDN["MergeDelta(Δ)"]
        end

        MERGE["Merge all shards\ninto Global Sketch\n(after wg.Wait)"]

        GS["Global Sketch\n(aggregator.sketch)"]

        SH0 --> MERGE
        SH1 --> MERGE
        SHN --> MERGE
        MERGE --> GS
    end

    BC0 --> SH0
    BC1 --> SH1
    BCN --> SHN

    %% ── Query ─────────────────────────────────────────────────────────────────
    QUERY(["Query(input)\nEstimate(input)\nCall after Done()"])
    GS --> QUERY

    %% ── Shutdown sequence annotation ──────────────────────────────────────────
    SHUTDOWN["Shutdown sequence:\n1. Close inputCh[i]\n2. Wait Worker.Done()\n3. TauController.Stop()\n4. Aggregator drains\n5. Wait Aggregator.Done()\n6. Call Query()"]

    style SHUTDOWN fill:#fffbe6,stroke:#e6c300,color:#333
    style TAU fill:#e8f4fd,stroke:#2196f3,color:#000
    style TC fill:#e8f4fd,stroke:#2196f3,color:#000
    style AGG fill:#f0fff0,stroke:#4caf50,color:#000
    style W0 fill:#fff3e0,stroke:#ff9800,color:#000
    style W1 fill:#fff3e0,stroke:#ff9800,color:#000
    style WN fill:#fff3e0,stroke:#ff9800,color:#000
    style STREAM fill:#f3e5f5,stroke:#9c27b0,color:#000
    style QUERY fill:#f3e5f5,stroke:#9c27b0,color:#000
```

## Component Summary

| Component | Role | Goroutine? |
|---|---|---|
| **Worker** | Per-core stream processor; maintains local `CellSketch`; emits `DeltaUpdate` batches when cell value ≥ τ; flushes residuals at end-of-stream | Yes — one per worker |
| **CellSketch** | Pluggable sketch data structure (`UpdateCell`, `ShouldEmit`, `BuildDelta`, `ResetCell`, `MergeDelta`, `Estimate`, `Flush`) | No |
| **AdaptiveTau** | Shared atomic `float64` threshold τ; workers read it on every insert via `Current()` | No (lock-free) |
| **TauController** | Background ticker; probes aggregate `batchCh` queue depth; calls `AdaptiveTau.Adjust()` to apply backpressure or reduce latency | Yes — one |
| **batchCh / recycleCh** | Per-worker bounded channel pair; workers accumulate up to 256 deltas per `deltaBatch` before sending; aggregator shard recycles the buffer | — |
| **Aggregator shard goroutine** | One per worker; drains that worker's `batchCh`, calls `ShardSketch.MergeDelta()` for each delta, returns buffer to `recycleCh` | Yes — one per worker |
| **Global Sketch** | Final merged result; all shard sketches are merged into it via `Merge()` after all shard goroutines finish | No |
| **Query** | `Aggregator.Query(input)` → `GlobalSketch.Estimate(input)`; valid only after `Aggregator.Done()` | No |

## Data Flow (per stream item)

```
SketchInput
  └─► inputCh[w]
        └─► Worker.Process(input)
              ├─ UpdateCell(row, col)        # sketch-specific increment
              ├─ ShouldEmit(newVal, τ)?
              │     yes → BuildDelta(row,col) → batchBuf
              │              └─► [batch full] → batchCh[w]
              │                                    └─► ShardSketch[w].MergeDelta(Δ)
              └─ ResetCell(row, col)         # zero local cell after emission
```

## τ Adaptation Rule

```
Every 50 ms (TauController tick):
  queueLen = Σ len(batchCh[i])

  if queueLen > UpperBound (60% of buffer):   τ = min(τ + Step, Max)  ← slow down emission
  if queueLen < LowerBound (10% of buffer):   τ = max(τ - Step, Min)  ← speed up emission
  else:                                        τ unchanged
```

## Supported Sketch Types

| Sketch | `ShouldEmit` rule | `MergeDelta` rule | `ResetCell` |
|---|---|---|---|
| **CountMinSketch** | `val >= τ` | addition | `cell = 0` |
| **CountSketch** | `|val| >= τ` | addition | `cell = 0` |
| **HyperLogLog** | always true (on register increase) | max | no-op |
| **DDSketch** | `val >= τ` | addition | `cell = 0` |
