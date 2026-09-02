// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ddsketch

import (
	"math"
	"testing"
)

// TestUpdateGOS_Disabled verifies threshold==0 behaves exactly like Update
// (no crossings reported, normal accumulation, no reset, no gosPopulated
// bookkeeping) — mirrors CountSketch's TestUpdateStringGOS_Disabled.
func TestUpdateGOS_Disabled(t *testing.T) {
	d := NewDDSketch(0.01)
	for i := 0; i < 10; i++ {
		crossed, upd := d.UpdateGOS(100.0, 0)
		if crossed {
			t.Fatalf("threshold==0 must report no crossing, got %+v", upd)
		}
	}
	if d.Count() != 10 {
		t.Fatalf("Count: got %d, want 10", d.Count())
	}
	if d.PopulatedBuckets() != 0 {
		t.Fatalf("PopulatedBuckets: got %d, want 0 (threshold==0 does not track it)", d.PopulatedBuckets())
	}
}

// TestUpdateGOS_CrossesAndResets drives repeated inserts of the SAME value
// (so the touched bucket is deterministic) until the bucket's count crosses
// a fixed threshold, and verifies: the crossing is reported with the correct
// magnitude and bucket index, the bucket is reset to 0 in place, d.count
// reflects the reset (no other bucket contributes), and PopulatedBuckets()
// goes back to 0. Mirrors CountSketch's TestUpdateStringGOS_CrossesAndResets.
func TestUpdateGOS_CrossesAndResets(t *testing.T) {
	d := NewDDSketch(0.01)
	const v = 100.0
	const threshold = 5
	wantIdx := d.BucketIndex(v)

	var crossed bool
	var upd DDSketchGOSUpdate
	var i int
	for i = 0; i < 1000; i++ {
		crossed, upd = d.UpdateGOS(v, threshold)
		if crossed {
			break
		}
	}
	if !crossed {
		t.Fatalf("no crossing after %d inserts", i+1)
	}
	if upd.Index != wantIdx {
		t.Fatalf("GOSCellUpdate.Index = %d, want %d", upd.Index, wantIdx)
	}
	wantCount := uint64(i + 1)
	if upd.Count != wantCount {
		t.Fatalf("GOSCellUpdate.Count = %d, want %d (after %d inserts)", upd.Count, wantCount, i+1)
	}
	if upd.Count < threshold {
		t.Fatalf("reported crossing %d did not actually reach threshold %d", upd.Count, uint64(threshold))
	}
	// The bucket must be zeroed in place immediately.
	if c := d.BucketCount(wantIdx); c != 0 {
		t.Fatalf("BucketCount after crossing: got %d, want 0", c)
	}
	// Single touched-bucket sketch: d.count and PopulatedBuckets must both be
	// back to 0 after the reset (no other bucket contributes).
	if d.Count() != 0 {
		t.Fatalf("Count after crossing+reset: got %d, want 0", d.Count())
	}
	if d.PopulatedBuckets() != 0 {
		t.Fatalf("PopulatedBuckets after crossing+reset: got %d, want 0", d.PopulatedBuckets())
	}
}

// TestUpdateGOS_PopulatedBucketsTracksFirstTouchOnly verifies B (the
// PopulatedBuckets counter) increments exactly once per DISTINCT bucket
// touched (not once per insert), and decrements exactly when a bucket resets
// — the incremental invariant sampling-cdm-gos-derivations.md §8.4 requires
// for T=ε·N/(k·B) to stay exact rather than drift into an unsafe
// (B_assumed < B_actual) state.
func TestUpdateGOS_PopulatedBucketsTracksFirstTouchOnly(t *testing.T) {
	d := NewDDSketch(0.01)
	// threshold high enough that nothing crosses in this test.
	const threshold = 1_000_000
	values := []float64{1.0, 2.0, 3.0, 5.0, 10.0}
	for _, v := range values {
		for rep := 0; rep < 3; rep++ {
			if crossed, _ := d.UpdateGOS(v, threshold); crossed {
				t.Fatalf("unexpected crossing at threshold=%d", uint64(threshold))
			}
		}
	}
	if got := d.PopulatedBuckets(); got != uint32(len(values)) {
		t.Fatalf("PopulatedBuckets: got %d, want %d (one per distinct bucket, repeats don't recount)", got, len(values))
	}
}

// TestUpdateGOS_TelescopingReconstruction is the key correctness property
// this mechanism depends on: even though the SOURCE sketch's buckets are
// repeatedly zeroed in place as they cross threshold, applying every
// DDSketchGOSUpdate ever reported onto a fresh TARGET sketch (as a +=
// AddToBucket, mirroring ApplyDelta's per-bucket semantics), plus whatever
// residual is left un-crossed in source at the end (mirrors what a
// window-boundary flush would drain), reconstructs the EXACT same
// per-bucket distribution a never-reset REFERENCE sketch fed the identical
// inserts would hold — DDSketch buckets are exact counts (no hashing
// collisions), so this is exact equality, not an approximate bound.
// Mirrors CountSketch's TestUpdateStringGOS_TelescopingReconstruction.
func TestUpdateGOS_TelescopingReconstruction(t *testing.T) {
	const alpha = 0.01
	source := NewDDSketch(alpha)
	reference := NewDDSketch(alpha)
	target := NewDDSketch(alpha)

	const threshold = 7
	values := []float64{1.0, 2.5, 10.0, 42.0, 1000.0}
	const n = 3000
	for i := 0; i < n; i++ {
		v := values[i%len(values)]
		_, upd := source.UpdateGOS(v, threshold)
		if upd.Count > 0 {
			target.AddToBucket(upd.Index, upd.Count)
		}
		reference.Update(v)
	}
	// Drain whatever's left un-crossed in source directly (mirrors a
	// window-boundary flush draining the residual below threshold).
	source.EachBucket(func(k int32, count uint64) {
		target.AddToBucket(k, count)
	})

	if got, want := target.Count(), reference.Count(); got != want {
		t.Fatalf("telescoped Count = %d, want %d (reference, never reset)", got, want)
	}

	// Exact per-bucket parity: every bucket the reference populated must
	// match target exactly, and vice versa (no extra/missing buckets).
	refBuckets := reference.LocalBuckets()
	tgtBuckets := target.LocalBuckets()
	if len(refBuckets) != len(tgtBuckets) {
		t.Fatalf("bucket count mismatch: target has %d, reference has %d", len(tgtBuckets), len(refBuckets))
	}
	for k, want := range refBuckets {
		if got := tgtBuckets[k]; got != want {
			t.Errorf("bucket %d: telescoped count=%v, want %v", k, got, want)
		}
	}

	// Quantiles must match within the relative-accuracy guarantee. Bucket
	// counts are identical (checked above), but q=0/q=1 read min/max, and
	// target's min/max come from AddToBucket's bucket-REPRESENTATIVE value
	// (gamma^(k+0.5)) rather than reference's raw inserted value — the same
	// bucket-edge discrepancy TestSerializeStateProtoBytes_QuantilesAfter-
	// ScalarRemoval documents and tolerates via 2*alpha; every other quantile
	// is derived purely from the (identical) bucket distribution.
	const tol = 2 * alpha
	for _, q := range []float64{0.0, 0.25, 0.5, 0.9, 1.0} {
		gotQ, gotOK := target.Quantile(q)
		wantQ, wantOK := reference.Quantile(q)
		if gotOK != wantOK {
			t.Errorf("q=%v: telescoped ok=%v, want ok=%v", q, gotOK, wantOK)
			continue
		}
		if wantQ == 0 {
			continue
		}
		if re := math.Abs(gotQ-wantQ) / wantQ; re > tol {
			t.Errorf("q=%v: telescoped=%v, want=%v, relErr=%v > tol=%v", q, gotQ, wantQ, re, tol)
		}
	}
}
