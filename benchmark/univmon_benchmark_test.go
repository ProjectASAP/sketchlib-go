package benchmark

import (
	"encoding/binary"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	univmon "github.com/ProjectASAP/sketchlib-go/sketch_framework/UnivMon"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

var (
	univBenchOnce   sync.Once
	univBenchInputs []*common.SketchInput
	univBenchHashes []uint64
	univBenchErr    error
)

func loadCAIDAUnivMonBenchmark(tb testing.TB) ([]*common.SketchInput, []uint64) {
	tb.Helper()
	univBenchOnce.Do(func() {
		file := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
		samples, err := testdata.ReadCAIDAStream(file, "")
		if err != nil {
			univBenchErr = err
			return
		}
		inputs := make([]*common.SketchInput, len(samples))
		hashes := make([]uint64, len(samples))
		for i, s := range samples {
			var ip [4]byte
			binary.BigEndian.PutUint32(ip[:], uint32(s.F))
			inputs[i] = common.FromBytes(ip[:])
			hashes[i] = common.Hash64(ip[:])
		}
		univBenchInputs = inputs
		univBenchHashes = hashes
	})

	if univBenchErr != nil {
		tb.Skipf("Skipping CAIDA benchmark: %v", univBenchErr)
	}
	if len(univBenchInputs) == 0 || len(univBenchHashes) == 0 {
		tb.Skip("Skipping CAIDA benchmark: empty dataset")
	}
	return univBenchInputs, univBenchHashes
}

func mustNewUnivMonBenchmark(tb testing.TB) *univmon.UnivSketch {
	tb.Helper()
	us, err := univmon.NewUnivSketchPyramid(200, 5, 4096, 16)
	if err != nil {
		tb.Fatalf("new univmon: %v", err)
	}
	return us
}

func cloneUnivMonBenchmark(tb testing.TB, src *univmon.UnivSketch) *univmon.UnivSketch {
	tb.Helper()
	data, err := src.SerializeToBytes()
	if err != nil {
		tb.Fatalf("serialize univmon: %v", err)
	}
	dst, err := univmon.DeserializeUnivSketchFromBytes(data)
	if err != nil {
		tb.Fatalf("deserialize univmon: %v", err)
	}
	return dst
}

func BenchmarkUnivMon_Update_CAIDA(b *testing.B) {
	inputs, _ := loadCAIDAUnivMonBenchmark(b)
	n := len(inputs)
	us := mustNewUnivMonBenchmark(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		us.Update(inputs[i%n], 1)
	}
}

func BenchmarkUnivMon_InsertWithHash_CAIDA(b *testing.B) {
	_, hashes := loadCAIDAUnivMonBenchmark(b)
	n := len(hashes)
	us := mustNewUnivMonBenchmark(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		us.InsertWithHash(hashes[i%n])
	}
}

func TestUnivMon_Insert_Latency_P50P99(t *testing.T) {
	inputs, _ := loadCAIDAUnivMonBenchmark(t)
	us := mustNewUnivMonBenchmark(t)

	sampleSize := benchMinInt(20_000, len(inputs))
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		us.Update(inputs[i], 1)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== UnivMon Insert Latency Report ===")
	t.Logf(" P50 (Median): %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:          %d ns", benchPercentileInt64(latencies, 0.99))
	t.Log("=====================================")
}

func BenchmarkUnivMon_QueryCardinality_CAIDA(b *testing.B) {
	inputs, _ := loadCAIDAUnivMonBenchmark(b)
	us := mustNewUnivMonBenchmark(b)
	for _, input := range inputs {
		us.Update(input, 1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = us.QueryWithHash(common.QueryCardinality, 0)
	}
}

func BenchmarkUnivMon_QueryTopK_CAIDA(b *testing.B) {
	inputs, _ := loadCAIDAUnivMonBenchmark(b)
	us := mustNewUnivMonBenchmark(b)
	for _, input := range inputs {
		us.Update(input, 1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = us.QueryTopK(100)
	}
}

func TestUnivMon_Query_Latency_Distribution(t *testing.T) {
	inputs, _ := loadCAIDAUnivMonBenchmark(t)
	us := mustNewUnivMonBenchmark(t)
	for _, input := range inputs {
		us.Update(input, 1)
	}

	sampleSize := 5_000
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		_, _ = us.QueryWithHash(common.QueryCardinality, 0)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== UnivMon Query Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("==========================================")
}

func BenchmarkUnivMon_Merge_CAIDA(b *testing.B) {
	inputs, _ := loadCAIDAUnivMonBenchmark(b)
	mid := len(inputs) / 2

	leftSrc := mustNewUnivMonBenchmark(b)
	rightSrc := mustNewUnivMonBenchmark(b)
	for i, input := range inputs {
		if i < mid {
			leftSrc.Update(input, 1)
		} else {
			rightSrc.Update(input, 1)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		left := cloneUnivMonBenchmark(b, leftSrc)
		right := cloneUnivMonBenchmark(b, rightSrc)
		b.StartTimer()
		if err := left.Merge(right); err != nil {
			b.Fatalf("merge failed: %v", err)
		}
	}
}

func BenchmarkUnivMon_Serialize_CAIDA(b *testing.B) {
	inputs, _ := loadCAIDAUnivMonBenchmark(b)
	us := mustNewUnivMonBenchmark(b)
	for _, input := range inputs {
		us.Update(input, 1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := us.SerializeToBytes(); err != nil {
			b.Fatalf("serialize failed: %v", err)
		}
	}
}

func TestUnivMon_Merge_Latency_Distribution(t *testing.T) {
	inputs, _ := loadCAIDAUnivMonBenchmark(t)
	mid := len(inputs) / 2

	leftSrc := mustNewUnivMonBenchmark(t)
	rightSrc := mustNewUnivMonBenchmark(t)
	for i, input := range inputs {
		if i < mid {
			leftSrc.Update(input, 1)
		} else {
			rightSrc.Update(input, 1)
		}
	}

	sampleSize := 250
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		left := cloneUnivMonBenchmark(t, leftSrc)
		right := cloneUnivMonBenchmark(t, rightSrc)
		start := time.Now()
		if err := left.Merge(right); err != nil {
			t.Fatalf("merge failed: %v", err)
		}
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== UnivMon Merge Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("==========================================")
}
