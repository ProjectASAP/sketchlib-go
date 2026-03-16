package benchmark

import (
	"encoding/binary"
	"sort"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	cocosketch "github.com/ProjectASAP/sketchlib-go/sketches/CocoSketch"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

const (
	cocoBenchD      = 5
	cocoBenchLength = 2048
)

func LoadCAIDAHelperCoco(tb testing.TB) ([]uint64, int) {
	file1 := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"

	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil {
		tb.Skipf("Skipping CAIDA benchmark: %v", err)
	}
	if len(samples) == 0 {
		tb.Fatal("No samples loaded")
	}

	hashes := make([]uint64, len(samples))
	for i, s := range samples {
		ipUint := uint32(s.F)
		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, ipUint)
		hashes[i] = common.Hash64(ipBytes)
	}
	return hashes, len(samples)
}

// =====================================================
// 1. INSERT PERFORMANCE & LATENCY
// =====================================================

func BenchmarkCocoSketch_Insert_Single(b *testing.B) {
	hashes, n := LoadCAIDAHelperCoco(b)
	cs, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cs.InsertWithHash(hashes[i%n])
	}
}

func BenchmarkCocoSketch_Insert_Batch(b *testing.B) {
	hashes, n := LoadCAIDAHelperCoco(b)
	cs, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
	batchSize := 1000

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := (i * batchSize) % n
		end := start + batchSize
		if end > n {
			end = n
		}
		for k := start; k < end; k++ {
			cs.InsertWithHash(hashes[k])
		}
	}
}

func TestCocoSketch_Insert_Latency_P50P99(t *testing.T) {
	hashes, n := LoadCAIDAHelperCoco(t)
	cs, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)

	sampleSize := benchMinInt(100_000, n)
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		cs.InsertWithHash(hashes[i])
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== CocoSketch Insert Latency Report ===")
	t.Logf(" P50 (Median): %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:          %d ns", benchPercentileInt64(latencies, 0.99))
	t.Log("=======================================")
}

// =====================================================
// 2. QUERY PERFORMANCE & LATENCY
// =====================================================

func BenchmarkCocoSketch_Query_Throughput(b *testing.B) {
	hashes, n := LoadCAIDAHelperCoco(b)
	cs, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)

	for _, h := range hashes {
		cs.InsertWithHash(h)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cs.QueryWithHash(common.QueryFrequency, hashes[i%n])
	}
}

func TestCocoSketch_Query_Latency_P99(t *testing.T) {
	hashes, n := LoadCAIDAHelperCoco(t)
	cs, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)

	for _, h := range hashes {
		cs.InsertWithHash(h)
	}

	sampleSize := 100_000
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		start := time.Now()
		_, _ = cs.QueryWithHash(common.QueryFrequency, hashes[i%n])
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== CocoSketch Query Latency Report ===")
	t.Logf(" P50 (Median): %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:          %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:        %d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("=======================================")
}

// =====================================================
// 3. MERGE PERFORMANCE & LATENCY
// =====================================================

func createCocoSketches(n int) []*cocosketch.CocoSketch {
	list := make([]*cocosketch.CocoSketch, n)
	for i := 0; i < n; i++ {
		list[i], _ = cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
	}
	return list
}

func BenchmarkCocoSketch_Merge_2(b *testing.B) {
	s1, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
	s2, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
	s1.InsertWithHash(1)
	s2.InsertWithHash(2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s1.Merge(s2)
	}
}

func benchmarkCocoMergeN(b *testing.B, count int) {
	list := createCocoSketches(count)
	for i, sk := range list {
		sk.InsertWithHash(uint64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := list[0]
		for j := 1; j < count; j++ {
			_ = target.Merge(list[j])
		}
	}
}

func BenchmarkCocoSketch_Merge_10(b *testing.B)   { benchmarkCocoMergeN(b, 10) }
func BenchmarkCocoSketch_Merge_100(b *testing.B)  { benchmarkCocoMergeN(b, 100) }
func BenchmarkCocoSketch_Merge_1000(b *testing.B) { benchmarkCocoMergeN(b, 1000) }

func TestCocoSketch_Merge_Latency_Distribution(t *testing.T) {
	hashes, n := LoadCAIDAHelperCoco(t)
	mid := n / 2

	leftSrc, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
	rightSrc, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
	for i, h := range hashes {
		if i < mid {
			leftSrc.InsertWithHash(h)
		} else {
			rightSrc.InsertWithHash(h)
		}
	}

	sampleSize := 1_000
	latencies := make([]int64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		left, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
		right, _ := cocosketch.NewCocoSketch(cocoBenchD, cocoBenchLength)
		_ = left.Merge(leftSrc)
		_ = right.Merge(rightSrc)

		start := time.Now()
		_ = left.Merge(right)
		latencies[i] = time.Since(start).Nanoseconds()
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Log("=== CocoSketch Merge Latency Distribution ===")
	t.Logf(" P50:  %d ns", benchPercentileInt64(latencies, 0.50))
	t.Logf(" P99:  %d ns", benchPercentileInt64(latencies, 0.99))
	t.Logf(" P99.9:%d ns", benchPercentileInt64(latencies, 0.999))
	t.Log("=============================================")
}
