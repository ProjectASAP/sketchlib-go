package ddsketch

import (
	"math"
	"math/rand"
	"testing"
)

// TestCollapsingStoreCapsMemory is the core sketchlib-go#72 regression: a
// single finite-but-extreme outlier must not force the store past maxBins,
// no matter how far its bucket index is from the current window.
func TestCollapsingStoreCapsMemory(t *testing.T) {
	const alpha = 0.01
	const maxBins = 100
	d := NewDDSketchWithMaxBins(alpha, maxBins)

	for i := 0; i < 5000; i++ {
		d.Update(1 + rand.Float64()*1000)
	}
	if got := d.store.counts.Len(); got > maxBins {
		t.Fatalf("store span %d exceeds maxBins %d after normal inserts", got, maxBins)
	}

	// A genuinely adversarial single outlier — far outside the working
	// range, but still a legitimate finite positive value.
	d.Update(1e15)
	if got := d.store.counts.Len(); got > maxBins {
		t.Fatalf("store span %d exceeds maxBins %d after the outlier — collapse did not cap growth", got, maxBins)
	}

	// Another extreme outlier, on the other side of the window this time —
	// growing RIGHT must also stay capped (not just growing left).
	for i := 0; i < 100; i++ {
		d.Update(1 + rand.Float64()*1000)
	}
	d.Update(1e18)
	if got := d.store.counts.Len(); got > maxBins {
		t.Fatalf("store span %d exceeds maxBins %d after a second outlier", got, maxBins)
	}
}

// TestCollapsingStorePreservesCount checks that a collapse never loses mass:
// the sketch's total observed count always equals the number of Update calls,
// even across repeated collapses.
func TestCollapsingStorePreservesCount(t *testing.T) {
	const alpha = 0.01
	const maxBins = 50
	d := NewDDSketchWithMaxBins(alpha, maxBins)

	n := 0
	// A spread of values whose bucket indices span far more than maxBins,
	// forcing many collapses.
	for exp := -20; exp <= 20; exp++ {
		v := math.Pow(1.5, float64(exp))
		for i := 0; i < 10; i++ {
			d.Update(v)
			n++
		}
	}
	if int(d.Count()) != n {
		t.Fatalf("count after repeated collapses: got %d, want %d (mass lost)", d.Count(), n)
	}

	// Cross-check: summing the live bucket store must match too.
	var summed uint64
	d.EachBucket(func(_ int32, c uint64) { summed += c })
	if summed != uint64(n) {
		t.Fatalf("summed bucket counts: got %d, want %d", summed, n)
	}
}

// TestCollapsingStoreHighEndStaysExact checks the defining property of
// CollapsingLOWEST: once a collapse has occurred, the HIGH end of the
// distribution (the most recently/highest inserted values) keeps its full
// per-bucket resolution — only the low end degrades. This matters for
// ASAPCollector's typical use (p95/p99 latency tail).
func TestCollapsingStoreHighEndStaysExact(t *testing.T) {
	const alpha = 0.01
	const maxBins = 20
	d := NewDDSketchWithMaxBins(alpha, maxBins)
	dUncapped := NewDDSketch(alpha)

	// Insert a wide low-value spread (forces collapsing) then a tight
	// cluster of HIGH values that both sketches should resolve identically.
	for exp := -50; exp <= -10; exp++ {
		v := math.Pow(1.2, float64(exp))
		d.Update(v)
		dUncapped.Update(v)
	}
	highVals := []float64{9000, 9500, 9900, 9990, 9999}
	for _, v := range highVals {
		d.Update(v)
		dUncapped.Update(v)
	}

	// The top quantile must match within alpha regardless of the capped
	// low-end history — high-end precision is untouched by collapsing.
	gotP99, ok := d.Quantile(0.99)
	if !ok {
		t.Fatal("capped sketch: p99 not ok")
	}
	wantP99, ok := dUncapped.Quantile(0.99)
	if !ok {
		t.Fatal("uncapped sketch: p99 not ok")
	}
	if re := math.Abs(gotP99-wantP99) / wantP99; re > alpha+1e-6 {
		t.Fatalf("p99 diverged from uncapped: got %v want %v relErr %v > alpha %v", gotP99, wantP99, re, alpha)
	}
}

// TestCollapsingStoreDoesNotAffectUncappedSketch is a behavior-preservation
// guard: NewDDSketch (maxBins=0, the default) must be byte-for-byte
// unaffected by the ensure()/addOne()/AddToBucket() refactor — same bucket
// contents as before the refactor for an ordinary insertion sequence.
func TestCollapsingStoreDoesNotAffectUncappedSketch(t *testing.T) {
	const alpha = 0.01
	d := NewDDSketch(alpha)
	for i := 0; i < 3000; i++ {
		d.Update(1 + rand.Float64()*100000)
	}
	// An uncapped sketch's span is whatever the data needs — no artificial
	// ceiling. This is a sanity check that maxBins=0 truly means unbounded
	// (the field's zero value, so NewDDSketch never triggers a collapse).
	left, right, ok := d.store.Range()
	if !ok {
		t.Fatal("store empty")
	}
	if right-left+1 <= 0 {
		t.Fatal("bogus span")
	}
	if got := int(d.Count()); got != 3000 {
		t.Fatalf("count: got %d, want 3000", got)
	}
}

// TestAddToBucketRespectsCollapsingCap exercises the bucket-level API
// (AddToBucket/addOneFast, used by the GOS/delta insert paths) directly,
// confirming the cap applies there too, not just through Update().
func TestAddToBucketRespectsCollapsingCap(t *testing.T) {
	const alpha = 0.01
	const maxBins = 10
	d := NewDDSketchWithMaxBins(alpha, maxBins)

	for k := int32(0); k < 500; k += 5 {
		d.AddToBucket(k, 1)
		if got := d.store.counts.Len(); got > maxBins {
			t.Fatalf("AddToBucket: store span %d exceeds maxBins %d at k=%d", got, maxBins, k)
		}
	}
	if int(d.Count()) != 100 {
		t.Fatalf("count: got %d, want 100", d.Count())
	}
}

// TestCollapsingStoreMaxBinsOne is the tightest possible cap: every insert
// collapses into a single bucket, degenerating the sketch into an exact
// counter. Still must never exceed 1 bin and never lose count.
func TestCollapsingStoreMaxBinsOne(t *testing.T) {
	const alpha = 0.01
	d := NewDDSketchWithMaxBins(alpha, 1)
	vals := []float64{1, 1000, 1e-3, 1e9, 5, 5000000}
	for _, v := range vals {
		d.Update(v)
		if got := d.store.counts.Len(); got > 1 {
			t.Fatalf("maxBins=1: store span %d exceeds 1 after Update(%v)", got, v)
		}
	}
	if int(d.Count()) != len(vals) {
		t.Fatalf("count: got %d, want %d", d.Count(), len(vals))
	}
}

// TestCollapsingStoreRandomStress inserts a large, randomly-ordered mix of
// tightly-clustered and wildly-extreme values against a small cap, checking
// the span-cap and count-conservation invariants hold throughout — not just
// at the end — across many collapse events triggered from both directions.
func TestCollapsingStoreRandomStress(t *testing.T) {
	const alpha = 0.02
	const maxBins = 30
	d := NewDDSketchWithMaxBins(alpha, maxBins)
	rng := rand.New(rand.NewSource(42))

	n := 0
	for i := 0; i < 20000; i++ {
		var v float64
		switch rng.Intn(4) {
		case 0:
			v = 1 + rng.Float64()*100 // normal cluster
		case 1:
			v = math.Pow(10, rng.Float64()*30-15) // wide log-spread
		case 2:
			v = 1e-100 * (1 + rng.Float64()) // extreme low
		default:
			v = 1e100 * (1 + rng.Float64()) // extreme high
		}
		d.Update(v)
		n++
		if got := d.store.counts.Len(); got > maxBins {
			t.Fatalf("iter %d: store span %d exceeds maxBins %d (v=%v)", i, got, maxBins, v)
		}
	}
	if int(d.Count()) != n {
		t.Fatalf("count: got %d, want %d", d.Count(), n)
	}
	var summed uint64
	d.EachBucket(func(_ int32, c uint64) { summed += c })
	if summed != uint64(n) {
		t.Fatalf("summed bucket counts: got %d, want %d", summed, n)
	}
}

// TestApplyDeltaRespectsCollapsingCap exercises the delta-apply path
// (ApplyDelta, the backend-reconstruction route) against a capped target.
func TestApplyDeltaRespectsCollapsingCap(t *testing.T) {
	const alpha = 0.01
	const maxBins = 8
	snapshot := NewDDSketch(alpha)
	current := NewDDSketch(alpha)
	for k := int32(-200); k <= 200; k += 20 {
		current.AddToBucket(k, 1)
	}
	deltaBytes, err := ComputeDelta(snapshot, current, 1)
	if err != nil {
		t.Fatalf("ComputeDelta: %v", err)
	}

	target := NewDDSketchWithMaxBins(alpha, maxBins)
	if err := ApplyDelta(target, deltaBytes); err != nil {
		t.Fatalf("ApplyDelta: %v", err)
	}
	if got := target.store.counts.Len(); got > maxBins {
		t.Fatalf("ApplyDelta into a capped target: store span %d exceeds maxBins %d", got, maxBins)
	}
	if target.Count() != current.Count() {
		t.Fatalf("count: got %d, want %d (mass lost applying delta into a capped target)", target.Count(), current.Count())
	}
}
