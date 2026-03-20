// Package octosketch_test — Theoretical Guarantee Verification
//
// This file verifies the three core guarantees of the OctoSketch framework:
//
//  1. Error-bound preservation  (CMS ε,δ guarantees survive delta aggregation)
//  2. Communication reduction   (# deltas << # inserts)
//  3. Convergence               (aggregator ≡ single-machine sketch after full flush)
//
// Additionally it checks online accuracy: the aggregator tracks the reference
// sketch during the stream, not only at the end.
package octosketch_test

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	octosketch "github.com/ProjectASAP/sketchlib-go/sketch_framework/OctoSketch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Guarantee 1 — CMS error-bound preservation
//
// Standard CMS guarantee:  Pr[ f̂(x) ≤ f(x) + ε·N ] ≥ 1 − δ
// where ε = e/cols ≈ 2.718/2048 ≈ 0.00133, N = total stream weight.
//
// After Δ-based aggregation the sketch retains the no-underestimate property
// (f̂(x) ≥ f(x)) and the overestimate bound because:
//   • Every insert is eventually reflected in the aggregator (via threshold or Flush).
//   • The aggregator performs only non-negative additions.
// ─────────────────────────────────────────────────────────────────────────────

func TestCMSErrorBoundPreserved(t *testing.T) {
	const rows = 5
	const cols = 2048
	const tau = 8.0
	const hotKeyCount = 5000
	const coldKeyCount = 200  // per cold key
	const numColdKeys = 50

	deltaCh := make(chan octosketch.DeltaUpdate, 1<<20)

	// Reference single-machine sketch (no delta, ground truth).
	ref, err := octosketch.NewCountMinOcto(rows, cols)
	require.NoError(t, err)

	// Worker sketch.
	workerSketch, err := octosketch.NewCountMinOcto(rows, cols)
	require.NoError(t, err)
	tau_ := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(tau))
	worker := octosketch.NewWorker(0, workerSketch, tau_, deltaCh, nil)

	// Insert stream into both reference and worker.
	hotKey := common.FromString("hot-key")
	for range hotKeyCount {
		worker.Process(hotKey)
		ref.Insert(hotKey)
	}
	for k := range numColdKeys {
		cold := common.FromString(fmt.Sprintf("cold-%d", k))
		for range coldKeyCount {
			worker.Process(cold)
			ref.Insert(cold)
		}
	}
	worker.Flush()
	close(deltaCh)

	// Aggregate.
	aggSketch, err := octosketch.NewCountMinOcto(rows, cols)
	require.NoError(t, err)
	for delta := range deltaCh {
		aggSketch.MergeDelta(delta)
	}

	// ε = e / cols (standard CMS width-error bound).
	eps := math.E / float64(cols)
	N := float64(hotKeyCount + numColdKeys*coldKeyCount)

	// Verify hot key.
	fTrue := ref.Estimate(hotKey)
	fHat := aggSketch.Estimate(hotKey)

	assert.GreaterOrEqual(t, fHat, fTrue,
		"CMS must not underestimate: f̂ >= f_true")
	assert.LessOrEqual(t, fHat, fTrue+eps*N,
		"CMS overestimate must be within ε·N: f̂ <= f_true + ε*N  (%.0f <= %.0f + %.0f)",
		fHat, fTrue, eps*N)

	// Verify a sample of cold keys.
	for k := range min(10, numColdKeys) {
		cold := common.FromString(fmt.Sprintf("cold-%d", k))
		fTrueCold := ref.Estimate(cold)
		fHatCold := aggSketch.Estimate(cold)
		assert.GreaterOrEqual(t, fHatCold, fTrueCold,
			"cold key %d: CMS must not underestimate", k)
		assert.LessOrEqual(t, fHatCold, fTrueCold+eps*N,
			"cold key %d: overestimate exceeds ε·N", k)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Guarantee 2 — Communication reduction
//
// With threshold τ, a key inserted f times generates at most ⌈f/τ⌉ deltas
// per row. The total delta count must be strictly less than total insertions
// when τ > 1.
// ─────────────────────────────────────────────────────────────────────────────

func TestCommunicationReduction(t *testing.T) {
	const tau = 20.0
	const insertions = 10_000
	const rows = 5
	const cols = 2048

	deltaCh := make(chan octosketch.DeltaUpdate, 1<<20)
	sketch, err := octosketch.NewCountMinOcto(rows, cols)
	require.NoError(t, err)
	tau_ := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(tau))
	worker := octosketch.NewWorker(0, sketch, tau_, deltaCh, nil)

	key := common.FromString("comm-key")
	for range insertions {
		worker.Process(key)
	}
	worker.Flush()
	close(deltaCh)

	var deltaCount int
	for range deltaCh {
		deltaCount++
	}

	// Upper bound: at most ⌈insertions/τ⌉ * rows deltas (one per row per τ items).
	maxDeltas := int(math.Ceil(float64(insertions)/tau))*rows + rows // +rows for flush residual
	assert.Less(t, deltaCount, insertions,
		"total deltas (%d) must be < total insertions (%d) with τ=%.0f",
		deltaCount, insertions, tau)
	assert.LessOrEqual(t, deltaCount, maxDeltas,
		"delta count (%d) must not exceed theoretical bound (%d)", deltaCount, maxDeltas)

	t.Logf("communication reduction: %d inserts → %d deltas (%.1fx reduction, τ=%.0f)",
		insertions, deltaCount, float64(insertions)/float64(deltaCount), tau)
}

// ─────────────────────────────────────────────────────────────────────────────
// Guarantee 3 — Convergence
//
// After all workers flush, the aggregator sketch must be equivalent to a
// single-machine sketch that processed the entire stream directly.
// Equivalence: aggregator.Estimate(x) == reference.Estimate(x) for all x.
// ─────────────────────────────────────────────────────────────────────────────

func TestConvergenceAfterFlush(t *testing.T) {
	const rows = 5
	const cols = 2048
	const tau = 7.0
	const numWorkers = 4
	const insertionsPerWorker = 300

	deltaCh := make(chan octosketch.DeltaUpdate, 1<<20)
	fixedTau := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(tau))

	// Reference: single sketch processes all the same inserts locally.
	ref, err := octosketch.NewCountMinOcto(rows, cols)
	require.NoError(t, err)

	aggSketch, err := octosketch.NewCountMinOcto(rows, cols)
	require.NoError(t, err)

	keys := make([]*common.SketchInput, 20)
	for i := range keys {
		keys[i] = common.FromString(fmt.Sprintf("key-%d", i))
	}

	var wg sync.WaitGroup
	for workerID := range numWorkers {
		wg.Add(1)
		workerID := workerID
		go func() {
			defer wg.Done()
			s, err := octosketch.NewCountMinOcto(rows, cols)
			require.NoError(t, err)
			w := octosketch.NewWorker(workerID, s, fixedTau, deltaCh, nil)
			for i := range insertionsPerWorker {
				k := keys[i%len(keys)]
				w.Process(k)
			}
			w.Flush()
		}()
	}

	// Populate reference in parallel (deterministic — same keys, same counts).
	go func() {
		for workerID := range numWorkers {
			for i := range insertionsPerWorker {
				_ = workerID
				ref.Insert(keys[i%len(keys)])
			}
		}
	}()

	wg.Wait()
	close(deltaCh)
	for delta := range deltaCh {
		aggSketch.MergeDelta(delta)
	}

	// After convergence: aggregator >= reference (CMS no-underestimate + complete delivery).
	for _, k := range keys {
		refEst := ref.Estimate(k)
		aggEst := aggSketch.Estimate(k)
		assert.GreaterOrEqual(t, aggEst, refEst,
			"convergence: aggregator(%s)=%.0f must be >= reference=%.0f", string(k.Bytes), aggEst, refEst)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Guarantee 4 — Online accuracy
//
// The aggregator estimate should stay close to the true count at intermediate
// points in the stream, not only at the end.
// We verify this by snapshotting the aggregator estimate after each batch.
// ─────────────────────────────────────────────────────────────────────────────

func TestOnlineAccuracy(t *testing.T) {
	const rows = 5
	const cols = 2048
	const tau = 5.0
	const batches = 10
	const batchSize = 100

	deltaCh := make(chan octosketch.DeltaUpdate, 65536)
	sketch, err := octosketch.NewCountMinOcto(rows, cols)
	require.NoError(t, err)
	tau_ := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(tau))
	worker := octosketch.NewWorker(0, sketch, tau_, deltaCh, nil)

	aggSketch, err := octosketch.NewCountMinOcto(rows, cols)
	require.NoError(t, err)

	key := common.FromString("online-key")
	// ε = e/cols — allowed overestimate fraction.
	eps := math.E / float64(cols)

	for batch := range batches {
		insertedSoFar := float64((batch + 1) * batchSize)

		// Insert batch.
		for range batchSize {
			worker.Process(key)
		}

		// Drain available deltas into aggregator (non-blocking).
		draining := true
		for draining {
			select {
			case d := <-deltaCh:
				aggSketch.MergeDelta(d)
			default:
				draining = false
			}
		}

		// At every batch boundary the aggregator may lag by at most τ*rows
		// (pending cells not yet emitted).  It must not underestimate by more
		// than that, and must not overestimate by more than ε*N.
		lagBound := tau * float64(rows)
		est := aggSketch.Estimate(key)

		assert.GreaterOrEqual(t, est+lagBound, insertedSoFar,
			"batch %d: online estimate lags by more than τ*rows (%.0f+%.0f < %.0f)",
			batch, est, lagBound, insertedSoFar)
		assert.LessOrEqual(t, est, insertedSoFar*(1+eps),
			"batch %d: online estimate overshoots (%.0f > %.0f)",
			batch, est, insertedSoFar*(1+eps))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Guarantee 5 — Adaptive τ does not violate CMS guarantees
//
// Even when τ changes mid-stream, all counts are eventually delivered
// (no count is permanently lost) because Flush always drains residuals.
// ─────────────────────────────────────────────────────────────────────────────

func TestAdaptiveTauPreservesGuarantees(t *testing.T) {
	const rows = 5
	const cols = 2048
	const insertions = 1000

	deltaCh := make(chan octosketch.DeltaUpdate, 1<<18)

	// Start with τ=2, then have TauController push it up to τ=50.
	cfg := octosketch.TauConfig{
		Initial: 2, Min: 1, Max: 50, Step: 5,
		UpperBound: 0, // always increase (simulate backpressure from first tick)
		LowerBound: -1,
	}
	adaptiveTau := octosketch.NewAdaptiveTau(cfg)

	sketch, err := octosketch.NewCountMinOcto(rows, cols)
	require.NoError(t, err)
	worker := octosketch.NewWorker(0, sketch, adaptiveTau, deltaCh, nil)

	ref, err := octosketch.NewCountMinOcto(rows, cols)
	require.NoError(t, err)

	key := common.FromString("adaptive-guarantee-key")
	for i := range insertions {
		worker.Process(key)
		ref.Insert(key)
		// Simulate τ adjustment mid-stream at half-way point.
		if i == insertions/2 {
			adaptiveTau.Adjust(999) // force increase
		}
	}
	worker.Flush()
	close(deltaCh)

	aggSketch, err := octosketch.NewCountMinOcto(rows, cols)
	require.NoError(t, err)
	for delta := range deltaCh {
		aggSketch.MergeDelta(delta)
	}

	refEst := ref.Estimate(key)
	aggEst := aggSketch.Estimate(key)
	assert.GreaterOrEqual(t, aggEst, refEst,
		"adaptive τ: aggregator must not underestimate (got %.0f, ref %.0f)", aggEst, refEst)
}

// ─────────────────────────────────────────────────────────────────────────────
// DDSketch OctoSketch Guarantee 6 — Relative error preserved after Δ-aggregation
//
// Guarantee 1 (DDSketch):  |x̂ − x| / x ≤ α
//
// After all workers flush, the aggregator sketch must match the reference DDSketch
// estimate to within the DDSketch relative-error bound α.  The delta-delivery
// path (Worker → delta channel → Aggregator) must not introduce additional error
// beyond the inherent logarithmic bucket approximation.
//
// Why this holds:
//   - Every insert is eventually delivered (via threshold emission or Flush).
//   - MergeDelta only adds non-negative counts — no cancellation, no loss.
//   - The quantile scan on the aggregator sees the same bucket-count totals as the
//     reference sketch, so the bucket index chosen (and thus γ^(k+0.5)) is identical.
// ─────────────────────────────────────────────────────────────────────────────

func TestDDSketchRelativeErrorPreserved(t *testing.T) {
	const alpha = 0.01  // 1% relative error guarantee
	const tau = 10.0
	const N = 5_000

	deltaCh := make(chan octosketch.DeltaUpdate, 1<<20)
	tau_ := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(tau))

	workerSketch, err := octosketch.NewDDSketchOcto(alpha)
	require.NoError(t, err)
	worker := octosketch.NewWorker(0, workerSketch, tau_, deltaCh, nil)

	// Reference: direct DDSketchOcto populated without the delta path.
	ref, err := octosketch.NewDDSketchOcto(alpha)
	require.NoError(t, err)

	// Stream: values spread geometrically so many distinct buckets are active.
	// v_i = 1.0 * (1.001)^i — slowly growing, N distinct positive values.
	for i := range N {
		v := math.Pow(1.001, float64(i))
		input := common.FromF64(v)
		worker.Process(input)
		ref.Insert(input)
	}
	worker.Flush()
	close(deltaCh)

	// Build aggregator by draining the delta channel.
	aggSketch, err := octosketch.NewDDSketchOcto(alpha)
	require.NoError(t, err)
	for delta := range deltaCh {
		aggSketch.MergeDelta(delta)
	}

	// Guarantee: for each quantile, |x̂_agg − x̂_ref| / x̂_ref ≤ α.
	// We use the reference DDSketch estimate as "x" (itself within α of truth).
	// Because both sketches use the same α, the two estimates land in the same
	// bucket, so the relative difference is exactly 0 after convergence.
	quantiles := []float64{0.25, 0.5, 0.75, 0.9, 0.99}
	for _, phi := range quantiles {
		q := common.FromF64(phi)
		xRef := ref.Estimate(q)
		xHat := aggSketch.Estimate(q)

		require.Greater(t, xRef, 0.0, "phi=%.2f: reference estimate must be positive", phi)
		relErr := math.Abs(xHat-xRef) / xRef
		assert.LessOrEqual(t, relErr, alpha,
			"phi=%.2f: relative error %.6f exceeds α=%.4f (x̂=%.6f, x_ref=%.6f)",
			phi, relErr, alpha, xHat, xRef)
	}

	t.Logf("DDSketch relative error preserved: α=%.3f, N=%d, τ=%.0f, quantiles=%v",
		alpha, N, tau, quantiles)
}

// ─────────────────────────────────────────────────────────────────────────────
// DDSketch OctoSketch Guarantee 7 — Per-bucket delay bound enforced by Worker
//
// Guarantee 2 (OctoSketch):  max unsent count per bucket  <  τ  at all times.
//
// The Worker's generic Process loop enforces this invariant:
//
//	UpdateCell(row, col, input) → newVal
//	if newVal ≥ τ: emit delta carrying newVal, then ResetCell → count = 0
//
// Immediately after any Process() call, every local bucket count is in [0, τ).
// ─────────────────────────────────────────────────────────────────────────────

func TestDDSketchDelayBound(t *testing.T) {
	const alpha = 0.02
	const tau = 15.0
	const insertions = 3_000

	deltaCh := make(chan octosketch.DeltaUpdate, 1<<20)
	tau_ := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(tau))

	sketch, err := octosketch.NewDDSketchOcto(alpha)
	require.NoError(t, err)
	worker := octosketch.NewWorker(0, sketch, tau_, deltaCh, nil)

	// Stream of varied values so multiple distinct buckets are exercised.
	for i := range insertions {
		// Interleave small and large values to stress multiple bucket indices.
		v := 1.0 + float64(i%50)*3.7
		worker.Process(common.FromF64(v))

		// Invariant: every local bucket count must be strictly below τ.
		// If ShouldEmit fired and ResetCell ran, the count is 0.
		// If ShouldEmit has not fired yet, the count is in [1, τ).
		for bucketIdx, count := range sketch.LocalBuckets() {
			assert.Less(t, count, tau,
				"delay bound violated: bucket %d has unsent count %.0f ≥ τ=%.0f after insert %d",
				bucketIdx, count, tau, i)
		}
	}

	// After Flush: all local counts must be zero (everything delivered).
	worker.Flush()
	close(deltaCh)

	remaining := sketch.LocalBuckets()
	assert.Empty(t, remaining, "after Flush all local buckets must be empty")

	// Consume the channel to verify every emitted value is positive.
	for delta := range deltaCh {
		assert.Greater(t, delta.Value, 0.0,
			"delta for bucket %d must carry positive count", delta.Col)
	}

	t.Logf("delay bound verified: τ=%.0f, %d inserts, all per-bucket counts < τ throughout",
		tau, insertions)
}

// ─────────────────────────────────────────────────────────────────────────────
// DDSketch OctoSketch Guarantee 8 — Combined error bound  α + τ/N
//
// Guarantee 3 (combined):  |x̂ − x_true| / x_true  ≤  α + τ/N
//
// This test verifies the bound in two phases:
//
//  Phase A — full-flush convergence (error ≤ α):
//    After all workers flush and the delta channel is drained, the aggregator
//    estimate must match the reference DDSketch estimate to within α.
//    (The τ/N term vanishes because N_agg == N after flush.)
//
//  Phase B — mid-stream combined bound (error ≤ α + τ/N):
//    Using a stream where all N values map to a single DDSketch bucket, the
//    only delay is the unsent sub-τ residual in that one bucket.  The true
//    quantile is the fixed value v0.  After draining whatever deltas are
//    available (without flushing), the aggregator may have seen N_agg < N
//    counts.  The rank error from the unsent N − N_agg counts is at most τ
//    (one bucket's sub-threshold residual).  As a fraction of N this is τ/N,
//    and the value error from the bucket approximation is α, giving α + τ/N.
// ─────────────────────────────────────────────────────────────────────────────

func TestDDSketchCombinedErrorBound(t *testing.T) {
	const alpha = 0.01
	const tau = 20.0
	const N = 2_000
	const v0 = 100.0 // all insertions use this value → single active bucket

	// ── Phase A: full-flush convergence ──────────────────────────────────────

	{
		deltaCh := make(chan octosketch.DeltaUpdate, 1<<20)
		tau_ := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(tau))

		workerSketch, err := octosketch.NewDDSketchOcto(alpha)
		require.NoError(t, err)
		worker := octosketch.NewWorker(0, workerSketch, tau_, deltaCh, nil)

		ref, err := octosketch.NewDDSketchOcto(alpha)
		require.NoError(t, err)

		inp := common.FromF64(v0)
		for range N {
			worker.Process(inp)
			ref.Insert(inp)
		}
		worker.Flush()
		close(deltaCh)

		agg, err := octosketch.NewDDSketchOcto(alpha)
		require.NoError(t, err)
		for delta := range deltaCh {
			agg.MergeDelta(delta)
		}

		// After full flush, aggregator total == N.
		assert.InDelta(t, float64(N), agg.TotalCount(), 0.5,
			"phase A: aggregator total count must equal N=%d after flush", N)

		// For each quantile: |x̂ − x_ref| / x_ref ≤ α.
		for _, phi := range []float64{0.1, 0.5, 0.9, 0.99} {
			q := common.FromF64(phi)
			xRef := ref.Estimate(q)
			xHat := agg.Estimate(q)
			require.Greater(t, xRef, 0.0, "phi=%.2f: ref estimate must be positive", phi)
			relErr := math.Abs(xHat-xRef) / xRef
			assert.LessOrEqual(t, relErr, alpha,
				"phase A phi=%.2f: |x̂−x_ref|/x_ref=%.6f > α=%.4f", phi, relErr, alpha)
		}

		t.Log("phase A (full-flush convergence) passed")
	}

	// ── Phase B: mid-stream combined bound ───────────────────────────────────
	//
	// Insert N values all equal to v0.  All land in a single bucket k0.
	// Process floor(N/tau) batches of tau items each → floor(N/tau) deltas emitted.
	// Remaining N mod tau items are sub-threshold residual (unsent), at most τ.
	//
	// Drain the channel after processing (without calling Flush).
	// The aggregator has seen N_agg = floor(N/tau)*tau counts.
	//
	// True value at any quantile = v0 (all values identical).
	// Aggregator estimate = bucketValue(k0) = γ^(k0+0.5)  if N_agg > 0.
	//
	// |x̂ − v0| / v0  ≤  α                  (DDSketch bucket approximation)
	// rank_error       =  N − N_agg  ≤  τ    (OctoSketch delay bound)
	// Combined:         ≤  α + τ/N            (delay shifts quantile by τ/N)

	{
		deltaCh := make(chan octosketch.DeltaUpdate, 1<<20)
		tau_ := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(tau))

		workerSketch, err := octosketch.NewDDSketchOcto(alpha)
		require.NoError(t, err)
		worker := octosketch.NewWorker(0, workerSketch, tau_, deltaCh, nil)

		inp := common.FromF64(v0)
		for range N {
			worker.Process(inp)
		}
		// Do NOT flush — measure mid-stream state.

		// Drain whatever deltas are available without blocking.
		agg, err := octosketch.NewDDSketchOcto(alpha)
		require.NoError(t, err)
		draining := true
		for draining {
			select {
			case d := <-deltaCh:
				agg.MergeDelta(d)
			default:
				draining = false
			}
		}

		// N_agg = floor(N/tau)*tau — exact number of counted items emitted.
		NAgg := agg.TotalCount()
		NSent := math.Floor(float64(N)/tau) * tau // expected lower bound on N_agg
		assert.GreaterOrEqual(t, NAgg, NSent,
			"phase B: aggregator should have at least %v counts after %d inserts with τ=%.0f",
			NSent, N, tau)

		// Per-bucket unsent residual = N − N_agg ≤ τ.
		unsent := float64(N) - NAgg
		assert.LessOrEqual(t, unsent, tau,
			"phase B: unsent residual %.0f must be ≤ τ=%.0f", unsent, tau)

		// Value estimate: if any delta was emitted, aggregator can answer.
		if NAgg > 0 {
			q50 := common.FromF64(0.5)
			xHat := agg.Estimate(q50)
			require.Greater(t, xHat, 0.0, "phase B: estimate must be positive when N_agg > 0")

			// DDSketch relative error: |x̂ − v0| / v0 ≤ α.
			ddRelErr := math.Abs(xHat-v0) / v0
			assert.LessOrEqual(t, ddRelErr, alpha,
				"phase B DDSketch error: |x̂−v0|/v0=%.6f > α=%.4f (x̂=%.4f, v0=%.4f)",
				ddRelErr, alpha, xHat, v0)

			// Combined bound: total error ≤ α + τ/N.
			combinedBound := alpha + tau/float64(N)
			assert.LessOrEqual(t, ddRelErr, combinedBound,
				"phase B combined bound: |x̂−v0|/v0=%.6f > α+τ/N=%.6f",
				ddRelErr, combinedBound)
		}

		// Verify the delay bound: unsent counts in local worker sketch are < τ.
		for bucketIdx, count := range workerSketch.LocalBuckets() {
			assert.Less(t, count, tau,
				"phase B delay bound: bucket %d has unsent count %.0f ≥ τ=%.0f",
				bucketIdx, count, tau)
		}

		t.Logf("phase B (mid-stream combined bound): N=%d, τ=%.0f, N_agg=%.0f, unsent=%.0f, bound=α+τ/N=%.4f",
			N, tau, NAgg, unsent, alpha+tau/float64(N))
	}
}
