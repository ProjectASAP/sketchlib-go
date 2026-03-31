package hll

import (
	"encoding/binary"
	"math"
	"slices"
	"strconv"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

// Tolerance for HLL (Standard Error for 14-bit HLL is ~0.8%, we allow 2% margin)
const relativeTolerance = 0.02

// ==============================================================================
// 1. HELPERS
// ==============================================================================

// Helper to load CAIDA data
func loadCAIDA(t *testing.T) []testdata.Sample {
	// Adjust path relative to this file location (sketches/HyperLogLog/)
	file1 := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		t.Skipf("Skipping CAIDA test: %v", err)
	}
	if len(samples) == 0 {
		t.Skip("No CAIDA samples found.")
	}
	return samples
}

// Helper to assert relative error
func assertWithinRelativeError(t *testing.T, estimate, actual int, tolerance float64) {
	t.Helper()
	if actual == 0 {
		if estimate != 0 {
			t.Errorf("Expected 0, got %d", estimate)
		}
		return
	}

	diff := math.Abs(float64(estimate - actual))
	relErr := diff / float64(actual)

	t.Logf(" Actual: %d | Estimate: %d | RelErr: %.4f%%", actual, estimate, relErr*100)

	if relErr > tolerance {
		t.Errorf("Accuracy violation! Error %.4f%% > Tolerance %.2f%%", relErr*100, tolerance*100)
	}
}

// Helper to convert float IP to bytes (simulating raw packet data)
func floatIpToBytes(f float64) []byte {
	ipUint := uint32(f)
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, ipUint)
	return b
}

// ==============================================================================
// 2. CAIDA TESTS
// ==============================================================================

// TestHyperLogLog_CAIDA_Accuracy verifies the cardinality estimation accuracy
// on the real-world CAIDA dataset.
func TestHyperLogLog_CAIDA_Accuracy(t *testing.T) {
	samples := loadCAIDA(t)
	hll := NewHyperLogLog()

	// Ground Truth Set
	uniqueIPs := make(map[uint32]bool)

	for _, s := range samples {
		ip := uint32(s.F)
		uniqueIPs[ip] = true

		// Insert into HLL
		// HLL expects a hash. We use the common hashing utility.
		ipBytes := floatIpToBytes(s.F)
		hll.InsertWithHash(common.Hash64(ipBytes))
	}

	actualCardinality := len(uniqueIPs)
	estimatedCardinality := hll.EstimateCardinality()

	t.Log("===================================================")
	t.Logf(" CAIDA CARDINALITY REPORT (HyperLogLog)")
	t.Logf(" Total Packets: %d", len(samples))
	t.Logf(" Unique IPs:    %d", actualCardinality)
	t.Log("===================================================")

	assertWithinRelativeError(t, estimatedCardinality, actualCardinality, relativeTolerance)
}

// TestHyperLogLog_CAIDA_Merge splits the CAIDA stream into two disjoint time windows,
// sketches them separately, merges them, and verifies the result matches the full stream.
func TestHyperLogLog_CAIDA_Merge(t *testing.T) {
	samples := loadCAIDA(t)
	mid := len(samples) / 2

	hllPart1 := NewHyperLogLog()
	hllPart2 := NewHyperLogLog()
	hllTotal := NewHyperLogLog()

	// Ingest Data
	for i, s := range samples {
		ipBytes := floatIpToBytes(s.F)
		hash := common.Hash64(ipBytes)

		// Full Sketch
		hllTotal.InsertWithHash(hash)

		// Split Sketches
		if i < mid {
			hllPart1.InsertWithHash(hash)
		} else {
			hllPart2.InsertWithHash(hash)
		}
	}

	// Merge
	if err := hllPart1.Merge(hllPart2); err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	// Verify 1: Estimate Match
	estMerged := hllPart1.EstimateCardinality()
	estTotal := hllTotal.EstimateCardinality()

	// The estimates should be IDENTICAL because HLL merge is lossless
	// (register max operation is deterministic regardless of order).
	if estMerged != estTotal {
		t.Errorf("Merge logic error. Merged Estimate (%d) != Total Estimate (%d)", estMerged, estTotal)
	}

	// Verify 2: Internal State Match
	// The registers themselves must be identical bit-for-bit
	if !slices.Equal(hllPart1.RegisterSlice(), hllTotal.RegisterSlice()) {
		t.Error("Merge state mismatch. Registers differ between Merged and Total sketch.")
	} else {
		t.Log("Merge state verification passed (Registers are identical).")
	}
}

// TestHyperLogLog_CAIDA_Idempotency ensures that re-inserting the same CAIDA stream
// multiple times does not change the cardinality estimate.
func TestHyperLogLog_CAIDA_Idempotency(t *testing.T) {
	samples := loadCAIDA(t)
	hll := NewHyperLogLog()

	// Pass 1
	for _, s := range samples {
		hll.InsertWithHash(common.Hash64(floatIpToBytes(s.F)))
	}
	est1 := hll.EstimateCardinality()

	// Pass 2 (Duplicate Data)
	for _, s := range samples {
		hll.InsertWithHash(common.Hash64(floatIpToBytes(s.F)))
	}
	est2 := hll.EstimateCardinality()

	if est1 != est2 {
		t.Fatalf("Idempotency violation! Pass 1: %d, Pass 2: %d", est1, est2)
	}
	t.Logf("Idempotency verified. Estimate remains %d after duplicate ingestion.", est1)
}

func TestHyperLogLogSerializeRoundTrip(t *testing.T) {
	h := NewHyperLogLog()
	for i := 0; i < 1000; i++ {
		h.InsertWithHash(common.FromU64(uint64(i)).Hash)
	}

	before, err := h.QueryWithHash(common.QueryCardinality, 0)
	if err != nil {
		t.Fatalf("query before: %v", err)
	}

	data, err := h.SerializeToBytes()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	restored, err := DeserializeHyperLogLogFromBytes(data)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}

	after, err := restored.QueryWithHash(common.QueryCardinality, 0)
	if err != nil {
		t.Fatalf("query after: %v", err)
	}

	if before != after {
		t.Fatalf("round-trip mismatch: before=%v after=%v", before, after)
	}
}

// ==============================================================================
// 3. RESET TESTS
// ==============================================================================

// TestHyperLogLog_Reset_ClearsState verifies that Reset() returns the sketch to the
// same state as a freshly constructed one.
func TestHyperLogLog_Reset_ClearsState(t *testing.T) {
	h := NewHyperLogLog()
	fresh := NewHyperLogLog()

	for i := uint64(0); i < 1000; i++ {
		h.InsertWithHash(common.FromU64(i).Hash)
	}
	if h.EstimateCardinality() == 0 {
		t.Fatal("sketch should be non-empty before Reset")
	}

	h.Reset()

	if !slices.Equal(h.RegisterSlice(), fresh.RegisterSlice()) {
		t.Fatal("registers not fully zeroed after Reset")
	}
	if got := h.EstimateCardinality(); got != 0 {
		t.Fatalf("Estimate() = %d after Reset, want 0", got)
	}
}

// TestHyperLogLog_Reset_SubsequentInserts verifies that a reset sketch receiving
// the same inputs as a fresh sketch produces identical register state and estimates.
func TestHyperLogLog_Reset_SubsequentInserts(t *testing.T) {
	h := NewHyperLogLog()
	ref := NewHyperLogLog()

	// Noise pass
	for i := uint64(0); i < 500; i++ {
		h.InsertWithHash(common.FromU64(i).Hash)
	}
	h.Reset()

	// Signal pass — same inputs to both
	for i := uint64(500); i < 1500; i++ {
		hash := common.FromU64(i).Hash
		h.InsertWithHash(hash)
		ref.InsertWithHash(hash)
	}

	if !slices.Equal(h.RegisterSlice(), ref.RegisterSlice()) {
		t.Fatal("register state diverged after Reset + re-insert")
	}
	if h.EstimateCardinality() != ref.EstimateCardinality() {
		t.Fatalf("Estimate mismatch after Reset: got %d, want %d", h.EstimateCardinality(), ref.EstimateCardinality())
	}
}

// TestHyperLogLog_Reset_NoAllocs confirms that Reset() performs no heap allocations.
func TestHyperLogLog_Reset_NoAllocs(t *testing.T) {
	h := NewHyperLogLog()
	for i := uint64(0); i < 100; i++ {
		h.InsertWithHash(common.FromU64(i).Hash)
	}

	allocs := testing.AllocsPerRun(10, func() { h.Reset() })
	if allocs != 0 {
		t.Fatalf("Reset() allocated %.0f times, want 0", allocs)
	}
}

func TestHyperLogLogRustStyleAPI(t *testing.T) {
	h := New()
	if h.Registers == nil {
		t.Fatal("register storage must be initialized")
	}
	if h.Registers.Len() != HLLRegisterCount {
		t.Fatalf("unexpected register length: got %d want %d", h.Registers.Len(), HLLRegisterCount)
	}

	h.InsertInput(common.FromString("a"))
	h.InsertBatch([]*common.SketchInput{common.FromString("b"), common.FromString("c")})
	h.InsertHashes([]uint64{common.FromString("d").Hash})

	if got := h.EstimateCardinality(); got < 4 {
		t.Fatalf("estimate too small: got %d", got)
	}
}

func TestHLL_Quality_CardinalityAndErrorBound(t *testing.T) {
	h := NewHyperLogLog()
	const n = 100000
	for i := 0; i < n; i++ {
		h.InsertWithHash(common.FromString("hll:q:" + strconv.Itoa(i)).Hash)
	}
	est := h.EstimateCardinality()
	relErr := math.Abs(float64(est-n)) / float64(n)
	if relErr > 0.05 {
		t.Fatalf("relative error too high: est=%d real=%d rel=%.4f", est, n, relErr)
	}
}

func TestHLL_Quality_MergeAccuracy(t *testing.T) {
	left := NewHyperLogLog()
	right := NewHyperLogLog()
	total := NewHyperLogLog()

	const n = 50000
	for i := 0; i < n; i++ {
		h := common.FromString("hll:m:" + strconv.Itoa(i)).Hash
		total.InsertWithHash(h)
		if i < n/2 {
			left.InsertWithHash(h)
		} else {
			right.InsertWithHash(h)
		}
	}
	if err := left.Merge(right); err != nil {
		t.Fatalf("merge: %v", err)
	}

	if left.EstimateCardinality() != total.EstimateCardinality() {
		t.Fatalf("merged estimate mismatch: got=%d want=%d", left.EstimateCardinality(), total.EstimateCardinality())
	}
}

// TestHLL_CrossLanguageRegressionVectors verifies that InsertWithHash maps each
// hash to the same register index and leading-zero value as the Rust implementation.
//
// Test vectors are taken verbatim from hll.rs hll_correctness_test (cumulative).
// Both HyperLogLog (DataFusion estimator) and HyperLogLogVariant are checked.
func TestHLL_CrossLanguageRegressionVectors(t *testing.T) {
	type step struct {
		hash    uint64
		bucket  int
		wantReg uint8
	}
	// Assertions are cumulative — each step checks the register state after all
	// prior insertions, exactly as the Rust test does.
	steps := []step{
		{0x0002_0000_0000_0000, 0, 1},
		{0x0000_0000_0000_0000, 0, 51},
		{0xfffc_3000_0000_0000, HLLRegisterMask, 5},
		{0xcafe_0000_0000_0000, 12991, 1},
		{0xcafc_00ce_cafe_face, 12991, 11},
		{0xface_cafe_face_cafe, 16051, 1},
		{0xfacc_ca00_0000_cafe, 16051, 3},
		{0x0831_8310_0000_0000, 524, 2},
		{0x3014_1592_6535_8000, 3077, 6},
		{0xcafc_0ace_cafe_face, 12991, 11}, // idempotent — already 11
	}

	hll := NewHyperLogLog()
	variant := NewDataFusion()

	for _, s := range steps {
		hll.InsertWithHash(s.hash)
		variant.InsertWithHash(s.hash)

		got := hll.Registers.AsSlice()[s.bucket]
		if got != s.wantReg {
			t.Errorf("HyperLogLog hash=0x%016x: register[%d] = %d, want %d",
				s.hash, s.bucket, got, s.wantReg)
		}

		gotV := variant.Registers.AsSlice()[s.bucket]
		if gotV != s.wantReg {
			t.Errorf("HyperLogLogVariant hash=0x%016x: register[%d] = %d, want %d",
				s.hash, s.bucket, gotV, s.wantReg)
		}
	}

	// Bucket 1000 must remain 0 (no unintended side-effects).
	if hll.Registers.AsSlice()[1000] != 0 {
		t.Errorf("register[1000] should be 0 (no collateral writes), got %d",
			hll.Registers.AsSlice()[1000])
	}
}

func TestHLL_Quality_SpecificHIPMergeUnsupported(t *testing.T) {
	h1 := NewHIP()
	h2 := NewHIP()
	if err := h1.Merge(h2); err == nil {
		t.Fatal("expected HIP merge to be unsupported")
	}
}
