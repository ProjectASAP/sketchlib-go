package benchmark

import (
	"encoding/binary"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	nitrosketch "github.com/ProjectASAP/sketchlib-go/sketch_framework/NitroSketch"
	"github.com/ProjectASAP/sketchlib-go/testdata"
)

const (
	nitroBenchRows = 5
	nitroBenchCols = 2048
	nitroBatchSize = 10_000
)

// loadNitroInputs loads one CAIDA pcap file and converts packets to SketchInputs
// keyed by source IP (BigEndian 4-byte encoding).
func loadNitroInputs(tb testing.TB) ([]*common.SketchInput, int) {
	tb.Helper()
	file1 := "../testdata/caida/equinix-nyc.dirA.20181220-130200.UTC.anon.pcap.gz"
	samples, err := testdata.ReadCAIDAStream(file1, "")
	if err != nil || len(samples) == 0 {
		tb.Skipf("Skipping NitroSketch CAIDA benchmark: %v", err)
	}
	inputs := make([]*common.SketchInput, len(samples))
	for i, s := range samples {
		ip := uint32(s.F)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], ip)
		inputs[i] = common.FromBytes(b[:])
	}
	return inputs, len(samples)
}

// batchSlice returns a nitroBatchSize-wide slice starting at iteration i,
// cycling safely within inputs.
func batchSlice(inputs []*common.SketchInput, i int) []*common.SketchInput {
	n := len(inputs)
	start := (i * nitroBatchSize) % (n - nitroBatchSize)
	return inputs[start : start+nitroBatchSize]
}

// =====================================================
// 1. INSERT THROUGHPUT
// =====================================================

// BenchmarkNitro_Insert benchmarks NitroSketch batch insertion across all target types
// and sampling rates as sub-benchmarks. Each op processes nitroBatchSize items.
//
// Run a specific sub-benchmark with:
//
//	-bench=BenchmarkNitro_Insert/CountMin/rate=0.01/CachedStep
func BenchmarkNitro_Insert(b *testing.B) {
	inputs, _ := loadNitroInputs(b)

	cases := []struct {
		name   string
		rate   float64
		cached bool
		cm     bool // true = CountMin, false = CountSketch
	}{
		{"CountMin/rate=1.00", 1.00, false, true},
		{"CountMin/rate=0.10", 0.10, false, true},
		{"CountMin/rate=0.01", 0.01, false, true},
		{"CountMin/rate=0.01/CachedStep", 0.01, true, true},
		{"CountSketch/rate=1.00", 1.00, false, false},
		{"CountSketch/rate=0.10", 0.10, false, false},
		{"CountSketch/rate=0.01", 0.01, false, false},
		{"CountSketch/rate=0.01/CachedStep", 0.01, true, false},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			// Bind the right Insert/InsertCachedStep method to a common closure
			// so the hot loop is identical regardless of the underlying target type.
			var insertFn func([]*common.SketchInput)
			if tc.cm {
				batch, _ := nitrosketch.NewCountMinBatch(tc.rate, nitroBenchRows, nitroBenchCols)
				if tc.cached {
					insertFn = batch.InsertCachedStep
				} else {
					insertFn = batch.Insert
				}
			} else {
				batch, _ := nitrosketch.NewCountSketchBatch(tc.rate, nitroBenchRows, nitroBenchCols)
				if tc.cached {
					insertFn = batch.InsertCachedStep
				} else {
					insertFn = batch.Insert
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				insertFn(batchSlice(inputs, i))
			}
		})
	}
}

// =====================================================
// 2. QUERY THROUGHPUT
// =====================================================

// BenchmarkNitro_Query_CountMin measures EstimateMedian throughput on a pre-filled
// CountMin batch. Each op queries one item.
func BenchmarkNitro_Query_CountMin(b *testing.B) {
	inputs, n := loadNitroInputs(b)
	batch, _ := nitrosketch.NewCountMinBatch(1.0, nitroBenchRows, nitroBenchCols)
	batch.Insert(inputs)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = batch.EstimateMedian(inputs[i%n])
	}
}

// BenchmarkNitro_Query_CountSketch measures EstimateMedian throughput on a pre-filled
// CountSketch batch.
func BenchmarkNitro_Query_CountSketch(b *testing.B) {
	inputs, n := loadNitroInputs(b)
	batch, _ := nitrosketch.NewCountSketchBatch(1.0, nitroBenchRows, nitroBenchCols)
	batch.Insert(inputs)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = batch.EstimateMedian(inputs[i%n])
	}
}

// =====================================================
// 3. MERGE THROUGHPUT
// =====================================================

// BenchmarkNitro_Merge_2 measures the latency of merging two CountMin batches.
func BenchmarkNitro_Merge_2(b *testing.B) {
	inputs, _ := loadNitroInputs(b)
	s1, _ := nitrosketch.NewCountMinBatch(1.0, nitroBenchRows, nitroBenchCols)
	s2, _ := nitrosketch.NewCountMinBatch(1.0, nitroBenchRows, nitroBenchCols)
	s1.Insert(inputs[:nitroBatchSize])
	s2.Insert(inputs[nitroBatchSize : nitroBatchSize*2])
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s1.Merge(s2)
	}
}

func benchmarkNitroMergeN(b *testing.B, count int) {
	b.Helper()
	inputs, n := loadNitroInputs(b)
	chunk := benchMinInt(nitroBatchSize, n/count)
	batches := make([]*nitrosketch.NitroBatch[*nitrosketch.CountMinTarget], count)
	for i := range count {
		batches[i], _ = nitrosketch.NewCountMinBatch(1.0, nitroBenchRows, nitroBenchCols)
		start := (i * chunk) % (n - chunk)
		batches[i].Insert(inputs[start : start+chunk])
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 1; j < count; j++ {
			_ = batches[0].Merge(batches[j])
		}
	}
}

func BenchmarkNitro_Merge_10(b *testing.B)  { benchmarkNitroMergeN(b, 10) }
func BenchmarkNitro_Merge_100(b *testing.B) { benchmarkNitroMergeN(b, 100) }

// =====================================================
// 4. INSERT LATENCY DISTRIBUTION (P50 / P99)
// =====================================================

// TestNitro_Insert_Latency reports per-batch and per-item insert latency at various
// sampling rates for both CountMin and CountSketch.
func TestNitro_Insert_Latency(t *testing.T) {
	inputs, n := loadNitroInputs(t)
	sampleSize := benchMinInt(2_000, n/nitroBatchSize)

	type result struct{ p50, p99 int64 }

	measureCM := func(rate float64, cached bool) result {
		batch, _ := nitrosketch.NewCountMinBatch(rate, nitroBenchRows, nitroBenchCols)
		lat := make([]int64, sampleSize)
		for i := range sampleSize {
			chunk := batchSlice(inputs, i)
			t0 := time.Now()
			if cached {
				batch.InsertCachedStep(chunk)
			} else {
				batch.Insert(chunk)
			}
			lat[i] = time.Since(t0).Nanoseconds()
		}
		sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
		return result{benchPercentileInt64(lat, 0.50), benchPercentileInt64(lat, 0.99)}
	}

	measureCS := func(rate float64, cached bool) result {
		batch, _ := nitrosketch.NewCountSketchBatch(rate, nitroBenchRows, nitroBenchCols)
		lat := make([]int64, sampleSize)
		for i := range sampleSize {
			chunk := batchSlice(inputs, i)
			t0 := time.Now()
			if cached {
				batch.InsertCachedStep(chunk)
			} else {
				batch.Insert(chunk)
			}
			lat[i] = time.Since(t0).Nanoseconds()
		}
		sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
		return result{benchPercentileInt64(lat, 0.50), benchPercentileInt64(lat, 0.99)}
	}

	pr := func(name string, r result) {
		fmt.Printf("  %-42s  P50 %7d ns (%4d ns/item)  P99 %7d ns (%4d ns/item)\n",
			name,
			r.p50, r.p50/int64(nitroBatchSize),
			r.p99, r.p99/int64(nitroBatchSize))
	}

	fmt.Printf("\n=== NitroSketch Insert Latency  (batch=%d, rows=%d, cols=%d, samples=%d) ===\n",
		nitroBatchSize, nitroBenchRows, nitroBenchCols, sampleSize)
	pr("CountMin   rate=1.00", measureCM(1.00, false))
	pr("CountMin   rate=0.10", measureCM(0.10, false))
	pr("CountMin   rate=0.01", measureCM(0.01, false))
	pr("CountMin   rate=0.01 CachedStep", measureCM(0.01, true))
	pr("CountSketch rate=1.00", measureCS(1.00, false))
	pr("CountSketch rate=0.10", measureCS(0.10, false))
	pr("CountSketch rate=0.01", measureCS(0.01, false))
	pr("CountSketch rate=0.01 CachedStep", measureCS(0.01, true))
	fmt.Println()
}

// =====================================================
// 5. QUERY LATENCY DISTRIBUTION (P50 / P99)
// =====================================================

// TestNitro_Query_Latency reports per-query latency for EstimateMedian on both
// CountMin and CountSketch, after the sketch is pre-filled with the full CAIDA stream.
func TestNitro_Query_Latency(t *testing.T) {
	inputs, n := loadNitroInputs(t)

	cmBatch, _ := nitrosketch.NewCountMinBatch(1.0, nitroBenchRows, nitroBenchCols)
	csBatch, _ := nitrosketch.NewCountSketchBatch(1.0, nitroBenchRows, nitroBenchCols)
	cmBatch.Insert(inputs)
	csBatch.Insert(inputs)

	const sampleSize = 100_000
	measureQuery := func(name string, fn func(i int) float64) {
		lat := make([]int64, sampleSize)
		for i := range sampleSize {
			t0 := time.Now()
			_ = fn(i)
			lat[i] = time.Since(t0).Nanoseconds()
		}
		sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
		p50 := benchPercentileInt64(lat, 0.50)
		p99 := benchPercentileInt64(lat, 0.99)
		p999 := benchPercentileInt64(lat, 0.999)
		fmt.Printf("  %-15s  P50 %5d ns  P99 %5d ns  P99.9 %5d ns\n",
			name, p50, p99, p999)
	}

	fmt.Printf("\n=== NitroSketch Query Latency  (rows=%d, cols=%d, samples=%d) ===\n",
		nitroBenchRows, nitroBenchCols, sampleSize)
	measureQuery("CountMin", func(i int) float64 {
		return cmBatch.EstimateMedian(inputs[i%n])
	})
	measureQuery("CountSketch", func(i int) float64 {
		return csBatch.EstimateMedian(inputs[i%n])
	})
	fmt.Println()
}

// =====================================================
// 6. MERGE LATENCY DISTRIBUTION (P50 / P99)
// =====================================================

// TestNitro_Merge_Latency reports the latency of a single CountMin merge operation
// on sketches pre-populated with the first and second halves of the CAIDA stream.
func TestNitro_Merge_Latency(t *testing.T) {
	inputs, _ := loadNitroInputs(t)
	half := len(inputs) / 2

	srcA, _ := nitrosketch.NewCountMinBatch(1.0, nitroBenchRows, nitroBenchCols)
	srcB, _ := nitrosketch.NewCountMinBatch(1.0, nitroBenchRows, nitroBenchCols)
	srcA.Insert(inputs[:half])
	srcB.Insert(inputs[half:])

	const sampleSize = 1_000
	lat := make([]int64, sampleSize)
	for i := range sampleSize {
		tgt, _ := nitrosketch.NewCountMinBatch(1.0, nitroBenchRows, nitroBenchCols)
		_ = tgt.Merge(srcA)
		t0 := time.Now()
		_ = tgt.Merge(srcB)
		lat[i] = time.Since(t0).Nanoseconds()
	}
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })

	fmt.Printf("\n=== NitroSketch CountMin Merge Latency  (rows=%d, cols=%d, samples=%d) ===\n",
		nitroBenchRows, nitroBenchCols, sampleSize)
	fmt.Printf("  P50   %d ns\n", benchPercentileInt64(lat, 0.50))
	fmt.Printf("  P99   %d ns\n", benchPercentileInt64(lat, 0.99))
	fmt.Printf("  P99.9 %d ns\n", benchPercentileInt64(lat, 0.999))
	fmt.Println()
}

// =====================================================
// 7. THROUGHPUT SUMMARY
// =====================================================

// TestNitro_Throughput_Summary measures sustained insert throughput across all sketch
// and sampling-rate combinations and prints a ranked comparison table.
func TestNitro_Throughput_Summary(t *testing.T) {
	inputs, n := loadNitroInputs(t)

	const (
		warmup = 10
		reps   = 100
	)

	type row struct {
		name    string
		rate    float64
		mitemsS float64
	}

	measureCM := func(name string, rate float64, cached bool) row {
		batch, _ := nitrosketch.NewCountMinBatch(rate, nitroBenchRows, nitroBenchCols)
		for i := range warmup {
			batch.Insert(batchSlice(inputs, i))
		}
		t0 := time.Now()
		for i := range reps {
			if cached {
				batch.InsertCachedStep(batchSlice(inputs, warmup+i))
			} else {
				batch.Insert(batchSlice(inputs, warmup+i))
			}
		}
		elapsed := time.Since(t0)
		return row{name, rate, float64(reps*nitroBatchSize) / elapsed.Seconds() / 1e6}
	}

	measureCS := func(name string, rate float64, cached bool) row {
		batch, _ := nitrosketch.NewCountSketchBatch(rate, nitroBenchRows, nitroBenchCols)
		for i := range warmup {
			batch.Insert(batchSlice(inputs, i))
		}
		t0 := time.Now()
		for i := range reps {
			if cached {
				batch.InsertCachedStep(batchSlice(inputs, warmup+i))
			} else {
				batch.Insert(batchSlice(inputs, warmup+i))
			}
		}
		elapsed := time.Since(t0)
		return row{name, rate, float64(reps*nitroBatchSize) / elapsed.Seconds() / 1e6}
	}

	measureQuery := func(name string, fn func(i int) float64) row {
		for i := range warmup * nitroBatchSize {
			_ = fn(i)
		}
		const queryReps = reps * nitroBatchSize
		t0 := time.Now()
		for i := range queryReps {
			_ = fn(i)
		}
		elapsed := time.Since(t0)
		return row{name, 1.0, float64(queryReps) / elapsed.Seconds() / 1e6}
	}

	cmFull, _ := nitrosketch.NewCountMinBatch(1.0, nitroBenchRows, nitroBenchCols)
	csFull, _ := nitrosketch.NewCountSketchBatch(1.0, nitroBenchRows, nitroBenchCols)
	cmFull.Insert(inputs)
	csFull.Insert(inputs)

	insertRows := []row{
		measureCM("CountMin   rate=1.00", 1.00, false),
		measureCM("CountMin   rate=0.10", 0.10, false),
		measureCM("CountMin   rate=0.01", 0.01, false),
		measureCM("CountMin   rate=0.01 CachedStep", 0.01, true),
		measureCS("CountSketch rate=1.00", 1.00, false),
		measureCS("CountSketch rate=0.10", 0.10, false),
		measureCS("CountSketch rate=0.01", 0.01, false),
		measureCS("CountSketch rate=0.01 CachedStep", 0.01, true),
	}
	queryRows := []row{
		measureQuery("CountMin   query", func(i int) float64 {
			return cmFull.EstimateMedian(inputs[i%n])
		}),
		measureQuery("CountSketch query", func(i int) float64 {
			return csFull.EstimateMedian(inputs[i%n])
		}),
	}

	fmt.Printf("\n=== NitroSketch Throughput Summary  (N=%d, batch=%d, rows=%d, cols=%d) ===\n\n",
		n, nitroBatchSize, nitroBenchRows, nitroBenchCols)

	fmt.Printf("  INSERT\n")
	fmt.Printf("  %-38s  %8s  %10s\n", "sketch / rate", "Mitem/s", "ns/item")
	fmt.Printf("  %s\n", "----------------------------------------------------------------------")
	for _, r := range insertRows {
		nsPerItem := 1e3 / r.mitemsS
		fmt.Printf("  %-38s  %8.2f  %10.2f\n", r.name, r.mitemsS, nsPerItem)
	}

	fmt.Printf("\n  QUERY (EstimateMedian, sketch pre-filled)\n")
	fmt.Printf("  %-38s  %8s  %10s\n", "sketch", "Mquery/s", "ns/query")
	fmt.Printf("  %s\n", "----------------------------------------------------------------------")
	for _, r := range queryRows {
		nsPerQuery := 1e3 / r.mitemsS
		fmt.Printf("  %-38s  %8.2f  %10.2f\n", r.name, r.mitemsS, nsPerQuery)
	}
	fmt.Println()
}
