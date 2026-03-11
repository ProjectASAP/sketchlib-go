package univmon

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

var (
	univCAIDAOnce   sync.Once
	univCAIDAInputs []*common.SketchInput
	univCAIDAHashes []uint64
	univCAIDAErr    error
)

func loadCAIDAForBenchmark(b *testing.B) ([]*common.SketchInput, []uint64) {
	b.Helper()
	univCAIDAOnce.Do(func() {
		file := "../../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			univCAIDAErr = err
			return
		}
		inputs := make([]*common.SketchInput, len(samples))
		hashes := make([]uint64, len(samples))
		for i, s := range samples {
			ipUint := uint32(s.F)
			var ipBytes [4]byte
			binary.BigEndian.PutUint32(ipBytes[:], ipUint)
			inputs[i] = common.FromBytes(ipBytes[:])
			hashes[i] = common.Hash64(ipBytes[:])
		}
		univCAIDAInputs = inputs
		univCAIDAHashes = hashes
	})
	if univCAIDAErr != nil {
		b.Skipf("Skipping benchmark (CAIDA unavailable): %v", univCAIDAErr)
	}
	if len(univCAIDAInputs) == 0 || len(univCAIDAHashes) == 0 {
		b.Skip("Skipping benchmark (CAIDA empty)")
	}
	return univCAIDAInputs, univCAIDAHashes
}

func mustBuildUnivSketch(tb testing.TB) *UnivSketch {
	tb.Helper()
	us, err := NewUnivSketchPyramid(200, 5, 4096, 16)
	if err != nil {
		tb.Fatalf("new univmon: %v", err)
	}
	return us
}

func cloneUnivSketch(tb testing.TB, src *UnivSketch) *UnivSketch {
	tb.Helper()
	data, err := src.SerializeToBytes()
	if err != nil {
		tb.Fatalf("serialize univmon: %v", err)
	}
	dst, err := DeserializeUnivSketchFromBytes(data)
	if err != nil {
		tb.Fatalf("deserialize univmon: %v", err)
	}
	return dst
}

func BenchmarkUnivMon_Update_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA inputs")
	inputs, _ := loadCAIDAForBenchmark(b)
	n := len(inputs)
	b.Logf("Total packets processed: %d", n)

	b.Log("[STEP 2] Initializing UnivMon")
	us := mustBuildUnivSketch(b)

	b.Log("[STEP 3] Running update benchmark (full input path)")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		us.Update(inputs[i%n], 1)
	}
}

func BenchmarkUnivMon_Update_NoTopK_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA inputs")
	inputs, _ := loadCAIDAForBenchmark(b)
	n := len(inputs)
	b.Logf("Total packets processed: %d", n)

	b.Log("[STEP 2] Initializing UnivMon (TopK disabled)")
	us := mustBuildUnivSketch(b)
	us.SetTopKEnabled(false)

	b.Log("[STEP 3] Running update benchmark (full input path, no TopK)")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		us.Update(inputs[i%n], 1)
	}
}

func BenchmarkUnivMon_InsertWithHash_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	_, hashes := loadCAIDAForBenchmark(b)
	n := len(hashes)
	b.Logf("Total packets processed: %d", n)

	b.Log("[STEP 2] Initializing UnivMon")
	us := mustBuildUnivSketch(b)

	b.Log("[STEP 3] Running insert benchmark (hash-only fast path)")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		us.InsertWithHash(hashes[i%n])
	}
}

func BenchmarkUnivMon_UpdateWithHashOnly_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA hashes")
	_, hashes := loadCAIDAForBenchmark(b)
	n := len(hashes)
	b.Logf("Total packets processed: %d", n)

	b.Log("[STEP 2] Initializing UnivMon")
	us := mustBuildUnivSketch(b)

	b.Log("[STEP 3] Running update benchmark (hash-only, value=1)")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		us.UpdateWithHashOnly(hashes[i%n], 1)
	}
}

func BenchmarkUnivMon_QueryTopK_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA inputs")
	inputs, _ := loadCAIDAForBenchmark(b)

	b.Log("[STEP 2] Building and pre-filling UnivMon")
	us := mustBuildUnivSketch(b)
	for _, input := range inputs {
		us.Update(input, 1)
	}

	b.Log("[STEP 3] Running QueryTopK benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = us.QueryTopK(100)
	}
}

func BenchmarkUnivMon_QueryCardinality_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA inputs")
	inputs, _ := loadCAIDAForBenchmark(b)

	b.Log("[STEP 2] Building and pre-filling UnivMon")
	us := mustBuildUnivSketch(b)
	for _, input := range inputs {
		us.Update(input, 1)
	}

	b.Log("[STEP 3] Running cardinality query benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = us.QueryWithHash(common.QueryCardinality, 0)
	}
}

func BenchmarkUnivMon_Merge_CAIDA(b *testing.B) {
	b.Log("[STEP 1] Loading CAIDA inputs")
	inputs, _ := loadCAIDAForBenchmark(b)
	mid := len(inputs) / 2

	b.Log("[STEP 2] Preparing source sketches for merge")
	leftSrc := mustBuildUnivSketch(b)
	rightSrc := mustBuildUnivSketch(b)
	for i, input := range inputs {
		if i < mid {
			leftSrc.Update(input, 1)
		} else {
			rightSrc.Update(input, 1)
		}
	}

	b.Log("[STEP 3] Running merge benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		left := cloneUnivSketch(b, leftSrc)
		right := cloneUnivSketch(b, rightSrc)
		b.StartTimer()
		if err := left.Merge(right); err != nil {
			b.Fatalf("merge failed: %v", err)
		}
	}
}
