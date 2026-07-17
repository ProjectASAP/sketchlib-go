package ddsketch

import (
	"math"
	"math/rand"
	"testing"
)

// TestRepresentativeMeetsAlphaBound pins the DataDog-parity representative
// formula (sketchlib-go#73 item 1): Value(k) = gamma^k * (1+alpha), chosen so
// the relative error between the representative and ANY value in bucket k is at
// most alpha at both bucket edges. The old midpoint gamma^(k+0.5) gave edge
// error sqrt(gamma)-1 > alpha, violating the documented guarantee.
func TestRepresentativeMeetsAlphaBound(t *testing.T) {
	for _, alpha := range []float64{0.001, 0.01, 0.05, 0.1} {
		m := NewIndexMapping(alpha)
		// Recovered alpha from gamma must match.
		if got := m.RelativeAccuracy(); math.Abs(got-alpha) > 1e-12 {
			t.Fatalf("alpha=%v: RelativeAccuracy()=%v", alpha, got)
		}
		for _, k := range []int32{-100, -1, 0, 1, 7, 500} {
			lo := m.LowerBound(k)         // gamma^k, bottom edge of the bucket
			hi := m.LowerBound(k + 1)     // gamma^(k+1), top edge
			rep := m.Value(k)
			// Representative sits inside the bucket.
			if rep < lo || rep > hi {
				t.Fatalf("alpha=%v k=%d: rep %v outside bucket [%v,%v]", alpha, k, rep, lo, hi)
			}
			// Relative error at BOTH edges must be <= alpha (+ float slack).
			if reLo := math.Abs(rep-lo) / lo; reLo > alpha+1e-9 {
				t.Errorf("alpha=%v k=%d: lower-edge relErr %v > alpha %v", alpha, k, reLo, alpha)
			}
			if reHi := math.Abs(rep-hi) / hi; reHi > alpha+1e-9 {
				t.Errorf("alpha=%v k=%d: upper-edge relErr %v > alpha %v", alpha, k, reHi, alpha)
			}
		}
	}
}

// TestQuantileWithinAlphaAtBucketEdges is the end-to-end consequence: values
// sitting exactly on bucket lower edges (gamma^k) must be recovered within
// alpha, which the old midpoint representative could not guarantee.
func TestQuantileWithinAlphaAtBucketEdges(t *testing.T) {
	const alpha = 0.01
	d := NewDDSketch(alpha)
	m := d.mapping
	// Insert one value at each bucket's exact lower edge across a range.
	var vals []float64
	for k := int32(0); k < 200; k++ {
		v := m.LowerBound(k)
		d.Update(v)
		vals = append(vals, v)
	}
	if int(d.Count()) != len(vals) {
		t.Fatalf("count %d, want %d", d.Count(), len(vals))
	}
	for _, q := range []float64{0.0, 0.1, 0.5, 0.9, 0.99, 1.0} {
		got, ok := d.Quantile(q)
		if !ok {
			t.Fatalf("q=%v not ok", q)
		}
		// Ground truth: the value at rank floor(q*(n-1)).
		rank := int(q * float64(len(vals)-1))
		exact := vals[rank]
		if re := math.Abs(got-exact) / exact; re > alpha+1e-9 {
			t.Errorf("q=%v: got %v exact %v relErr %v > alpha %v", q, got, exact, re, alpha)
		}
	}
}

// TestUntrackableExtremeDoesNotGrowStore guards the input-side bound
// (sketchlib-go#72 root symptom): a single finite-but-extreme value outside
// [MinIndexableValue, MaxIndexableValue] must be DROPPED, not mapped to an
// arbitrarily distant index that forces the dense bucket store to span the
// whole gap.
func TestUntrackableExtremeDoesNotGrowStore(t *testing.T) {
	const alpha = 0.01
	d := NewDDSketch(alpha)
	m := d.mapping

	// A normal working range: a few thousand modest values.
	for i := 0; i < 5000; i++ {
		d.Update(1 + rand.Float64()*1000)
	}
	if _, _, ok := d.store.Range(); !ok {
		t.Fatal("store empty after inserts")
	}
	spanBefore := d.store.counts.Len()
	countBefore := d.Count()

	// One absurd outlier just past MaxIndexableValue and one just below
	// MinIndexableValue must be silently dropped.
	d.Update(m.MaxIndexableValue() * 10)
	d.Update(m.MinIndexableValue() / 10)

	if d.Count() != countBefore {
		t.Fatalf("untrackable extremes were recorded: count %d -> %d", countBefore, d.Count())
	}
	if d.store.counts.Len() != spanBefore {
		t.Fatalf("store span grew from an untrackable extreme: %d -> %d", spanBefore, d.store.counts.Len())
	}

	// A large-but-trackable value IS still recorded.
	trackable := m.MaxIndexableValue() / 2
	d.Update(trackable)
	if d.Count() != countBefore+1 {
		t.Fatalf("a trackable large value was wrongly dropped")
	}
}
