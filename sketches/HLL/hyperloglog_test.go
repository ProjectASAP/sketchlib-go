package hll

import (
	"encoding/binary"
	"math"
	"slices"
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
	estimatedCardinality := hll.Estimate()

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
	estMerged := hllPart1.Estimate()
	estTotal := hllTotal.Estimate()

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
	est1 := hll.Estimate()

	// Pass 2 (Duplicate Data)
	for _, s := range samples {
		hll.InsertWithHash(common.Hash64(floatIpToBytes(s.F)))
	}
	est2 := hll.Estimate()

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

func TestHyperLogLogRustStyleAPI(t *testing.T) {
	h := New()
	if h.Registers == nil {
		t.Fatal("register storage must be initialized")
	}
	if h.Registers.Len() != HLLRegisterCount {
		t.Fatalf("unexpected register length: got %d want %d", h.Registers.Len(), HLLRegisterCount)
	}

	h.InsertInput(common.FromString("a"))
	h.InsertMany([]*common.SketchInput{common.FromString("b"), common.FromString("c")})
	h.InsertManyWithHashes([]uint64{common.FromString("d").Hash})

	if got := h.Estimate(); got < 4 {
		t.Fatalf("estimate too small: got %d", got)
	}
}
