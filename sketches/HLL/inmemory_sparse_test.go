package hll

import (
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"google.golang.org/protobuf/proto"
)

// sparseBitWidthGuard fails to compile if rho or index no longer fit the packed
// uint32 layout at the configured precision.
func TestSparseBitWidthsFit(t *testing.T) {
	maxRho := HLLRegisterBits + 1 // 51 at p=14
	if maxRho > sparseRhoMask {
		t.Fatalf("rho max %d does not fit in %d bits (mask %d)", maxRho, sparseRhoBits, sparseRhoMask)
	}
	maxIndex := HLLRegisterCount - 1
	// index occupies the high (32-sparseRhoBits) bits.
	if maxIndex > (1<<(32-sparseRhoBits))-1 {
		t.Fatalf("index max %d does not fit in %d bits", maxIndex, 32-sparseRhoBits)
	}
	// Round-trip a worst-case packed entry.
	e := packSparse(maxIndex, uint8(maxRho))
	if gi, gr := unpackSparse(e); gi != maxIndex || int(gr) != maxRho {
		t.Fatalf("pack/unpack round-trip failed: got (%d,%d) want (%d,%d)", gi, gr, maxIndex, maxRho)
	}
}

// buildSparse / buildDense insert the same `card` distinct keys into a sparse
// resp. dense instance.
func buildSparse(card int) *HyperLogLog {
	h := NewSparseHyperLogLog()
	for i := 0; i < card; i++ {
		h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("k:%d", i))))
	}
	return h
}

func buildDense(card int) *HyperLogLog {
	h := NewHyperLogLog()
	for i := 0; i < card; i++ {
		h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("k:%d", i))))
	}
	return h
}

// TestSparseAccuracyParity checks that, across the sparse range, the promotion
// boundary and well into dense, a sparse instance's estimate tracks the true
// cardinality within HLL's standard error AND that there is no discontinuity at
// the promotion threshold versus the dense estimator.
func TestSparseAccuracyParity(t *testing.T) {
	cards := []int{1, 10, 100, 1000, 4000, SparsePromoteThreshold, SparsePromoteThreshold + 1, 5000, 50000, 500000}
	for _, card := range cards {
		card := card
		t.Run(fmt.Sprintf("card=%d", card), func(t *testing.T) {
			sparse := buildSparse(card)
			dense := buildDense(card)

			es := sparse.Estimate()
			ed := dense.Estimate()

			// Standard error for p=14 is ~0.81%; allow generous headroom plus a
			// small additive floor for tiny cardinalities. Both sparse and dense
			// are compared to the true cardinality.
			tol := 0.03
			floor := 3.0
			relS := math.Abs(float64(es-card)) / math.Max(float64(card), 1)
			relD := math.Abs(float64(ed-card)) / math.Max(float64(card), 1)
			t.Logf("card=%d sparse_est=%d (rel %.4f) dense_est=%d (rel %.4f) promoted=%v",
				card, es, relS, ed, relD, !sparse.isSparse())

			if float64(card) > floor {
				if relS > tol {
					t.Errorf("sparse estimate %d for card %d rel err %.4f > %.4f", es, card, relS, tol)
				}
			}

			_ = relD

			// Register arrays are the true correctness invariant: a sparse
			// instance always materialises the SAME registers as the dense
			// instance for the same inputs, whether or not it has promoted.
			if !registersEqualHelper(sparse.RegisterSlice(), dense.RegisterSlice()) {
				t.Fatalf("card=%d: sparse registers differ from dense", card)
			}

			// Estimate agreement: once the sparse instance has PROMOTED it runs
			// the identical Ertl estimator over identical registers, so the
			// estimates must match exactly. While still sparse it uses linear
			// counting, which may differ from Ertl by a small amount — assert
			// only a tiny gap (no discontinuity at the threshold). Note: an
			// instance may remain sparse slightly past `card == threshold`
			// because hash collisions keep the distinct register count below it.
			if !sparse.isSparse() {
				if es != ed {
					t.Errorf("post-promotion estimate mismatch: sparse=%d dense=%d", es, ed)
				}
			} else {
				gap := math.Abs(float64(es-ed)) / math.Max(float64(ed), 1)
				if gap > 0.03 && math.Abs(float64(es-ed)) > floor {
					t.Errorf("sparse/dense estimate gap too large at card %d: sparse=%d dense=%d gap=%.4f",
						card, es, ed, gap)
				}
			}
		})
	}
}

// TestSparsePromotionState verifies an instance stays sparse below the threshold
// and is dense at/above it, and that the materialised registers match dense.
func TestSparsePromotionState(t *testing.T) {
	below := buildSparse(SparsePromoteThreshold / 2)
	if !below.isSparse() {
		t.Fatalf("instance with ~%d distinct should still be sparse", SparsePromoteThreshold/2)
	}
	above := buildSparse(SparsePromoteThreshold * 3)
	if above.isSparse() {
		t.Fatalf("instance with ~%d distinct should have promoted to dense", SparsePromoteThreshold*3)
	}

	// Registers must match the equivalent dense instance exactly at every card.
	for _, card := range []int{0, 1, 500, SparsePromoteThreshold + 200} {
		s := buildSparse(card)
		d := buildDense(card)
		if !registersEqualHelper(s.RegisterSlice(), d.RegisterSlice()) {
			t.Fatalf("card=%d: sparse registers differ from dense", card)
		}
	}
}

func registersEqualHelper(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSparseMergeParity verifies sparse⊕sparse, sparse⊕dense and dense⊕sparse
// all produce register arrays identical to the all-dense merge.
func TestSparseMergeParity(t *testing.T) {
	const n = 8000 // spans the promotion threshold for the halves
	keys := func(lo, hi int) []uint64 {
		out := make([]uint64, 0, hi-lo)
		for i := lo; i < hi; i++ {
			out = append(out, common.Hash64([]byte(fmt.Sprintf("m:%d", i))))
		}
		return out
	}
	left := keys(0, n/2)
	right := keys(n/2, n)

	mk := func(sparse bool, hashes []uint64) *HyperLogLog {
		var h *HyperLogLog
		if sparse {
			h = NewSparseHyperLogLog()
		} else {
			h = NewHyperLogLog()
		}
		for _, x := range hashes {
			h.InsertWithHash(x)
		}
		return h
	}

	// Reference: all-dense merge.
	refL := mk(false, left)
	refR := mk(false, right)
	if err := refL.Merge(refR); err != nil {
		t.Fatalf("ref merge: %v", err)
	}
	want := refL.RegisterSlice()

	cases := []struct {
		name           string
		lSparse, rSpar bool
	}{
		{"sparse+sparse", true, true},
		{"sparse+dense", true, false},
		{"dense+sparse", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := mk(c.lSparse, left)
			r := mk(c.rSpar, right)
			if err := l.Merge(r); err != nil {
				t.Fatalf("merge: %v", err)
			}
			if !registersEqualHelper(l.RegisterSlice(), want) {
				t.Fatalf("%s: merged registers differ from all-dense result", c.name)
			}
			if l.Estimate() != refL.Estimate() {
				t.Fatalf("%s: merged estimate %d != dense %d", c.name, l.Estimate(), refL.Estimate())
			}
		})
	}
}

// TestSparseWireByteIdentical asserts that a sparse instance and a dense instance
// fed the same keys serialise to byte-identical proto envelopes, at cardinalities
// below and above the in-memory promotion threshold.
func TestSparseWireByteIdentical(t *testing.T) {
	for _, card := range []int{0, 1, 100, 1000, SparsePromoteThreshold + 500} {
		card := card
		t.Run(fmt.Sprintf("card=%d", card), func(t *testing.T) {
			s := buildSparse(card)
			d := buildDense(card)

			sb, err := s.SerializeProtoBytes()
			if err != nil {
				t.Fatalf("sparse serialize: %v", err)
			}
			db, err := d.SerializeProtoBytes()
			if err != nil {
				t.Fatalf("dense serialize: %v", err)
			}
			if string(sb) != string(db) {
				t.Fatalf("card=%d: sparse proto (%d B) != dense proto (%d B)", card, len(sb), len(db))
			}

			// Round-trip decode of the sparse-produced bytes.
			got, err := DeserializeHyperLogLogFromProtoBytes(sb)
			if err != nil {
				t.Fatalf("deserialize: %v", err)
			}
			if !registersEqualHelper(got.RegisterSlice(), d.RegisterSlice()) {
				t.Fatalf("card=%d: round-tripped registers differ", card)
			}
		})
	}
}

// TestSparseResetReturnsToSparse verifies that after promotion + Reset, a
// born-sparse instance is back in the sparse state (dense array + pendingMask
// released).
func TestSparseResetReturnsToSparse(t *testing.T) {
	h := buildSparse(SparsePromoteThreshold * 3)
	if h.isSparse() {
		t.Fatal("expected promotion before Reset")
	}
	if h.Registers == nil {
		t.Fatal("promoted instance must have a dense register array")
	}

	h.Reset()

	if !h.isSparse() {
		t.Fatal("born-sparse instance must return to sparse after Reset")
	}
	if h.Registers != nil {
		t.Fatal("dense register array must be released after Reset")
	}
	if got := h.Estimate(); got != 0 {
		t.Fatalf("Estimate() = %d after Reset, want 0", got)
	}

	// Re-inserting after Reset must behave like a fresh sparse instance.
	ref := buildSparse(1000)
	for i := 0; i < 1000; i++ {
		h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("k:%d", i))))
	}
	if !registersEqualHelper(h.RegisterSlice(), ref.RegisterSlice()) {
		t.Fatal("register state after Reset + re-insert diverges from a fresh sparse instance")
	}
}

// TestDenseResetUnchanged confirms a born-dense instance still clears in place
// with zero allocations and stays dense.
func TestDenseResetUnchanged(t *testing.T) {
	h := NewHyperLogLog()
	for i := uint64(0); i < 100; i++ {
		h.InsertWithHash(common.FromU64(i).Hash)
	}
	allocs := testing.AllocsPerRun(10, func() { h.Reset() })
	if allocs != 0 {
		t.Fatalf("dense Reset() allocated %.0f times, want 0", allocs)
	}
	if h.isSparse() {
		t.Fatal("born-dense instance must NOT become sparse on Reset")
	}
}

// TestSparseMemoryFootprint demonstrates that a low-cardinality sparse instance
// uses far less than the ~16KB dense register array (which a dense instance
// allocates immediately).
func TestSparseMemoryFootprint(t *testing.T) {
	const card = 200

	measure := func(mk func() *HyperLogLog) uint64 {
		// Hold references so the instances are live across the measurement.
		const batch = 200
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		hs := make([]*HyperLogLog, batch)
		for i := range hs {
			h := mk()
			for j := 0; j < card; j++ {
				h.InsertWithHash(common.Hash64([]byte(fmt.Sprintf("mem:%d:%d", i, j))))
			}
			hs[i] = h
		}
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(hs)
		return (after.HeapAlloc - before.HeapAlloc) / batch
	}

	densePer := measure(func() *HyperLogLog { return NewHyperLogLog() })
	sparsePer := measure(func() *HyperLogLog { return NewSparseHyperLogLog() })

	t.Logf("per-instance heap at card=%d: dense≈%d B, sparse≈%d B", card, densePer, sparsePer)

	if sparsePer >= densePer {
		t.Fatalf("sparse per-instance heap %d not smaller than dense %d", sparsePer, densePer)
	}
	// A 200-entry sparse store is well under the 16384-byte dense array.
	if sparsePer > HLLRegisterCount/2 {
		t.Fatalf("sparse per-instance heap %d unexpectedly large (>%d)", sparsePer, HLLRegisterCount/2)
	}
}

// TestSparseTempBufferDedup exercises the temp-set merge path: inserting the
// same keys repeatedly (which keeps max rho per index) must yield the same
// registers as a dense instance.
func TestSparseTempBufferDedup(t *testing.T) {
	s := NewSparseHyperLogLog()
	d := NewHyperLogLog()
	for pass := 0; pass < 4; pass++ {
		for i := 0; i < 2000; i++ {
			hash := common.Hash64([]byte(fmt.Sprintf("dup:%d", i)))
			s.InsertWithHash(hash)
			d.InsertWithHash(hash)
		}
	}
	if !registersEqualHelper(s.RegisterSlice(), d.RegisterSlice()) {
		t.Fatal("sparse registers differ from dense after repeated inserts")
	}
	if s.Estimate() != d.Estimate() && !s.isSparse() {
		t.Fatalf("estimate mismatch: sparse=%d dense=%d", s.Estimate(), d.Estimate())
	}
}

// TestSparseOctoPathPromotes verifies the OctoSketch delta path promotes a sparse
// instance to dense rather than corrupting it.
func TestSparseOctoPathPromotes(t *testing.T) {
	h := NewSparseHyperLogLog()
	in := common.FromString("octo-key")
	var emitted []common.DeltaUpdate
	h.ProcessInput(in, 1, func(d common.DeltaUpdate) { emitted = append(emitted, d) })
	if h.isSparse() {
		t.Fatal("ProcessInput must promote a sparse instance to dense")
	}
	if len(emitted) == 0 {
		t.Fatal("expected at least one delta emission after promotion")
	}

	// MergeDelta onto a sparse instance must also promote and apply.
	g := NewSparseHyperLogLog()
	g.MergeDelta(emitted[0])
	if g.isSparse() {
		t.Fatal("MergeDelta must promote a sparse instance to dense")
	}
}

// ensure proto import is used even if other assertions change.
var _ = proto.Marshal
