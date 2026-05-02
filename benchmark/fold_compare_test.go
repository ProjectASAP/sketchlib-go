package benchmark

import (
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
	foldcountminsketch "github.com/ProjectASAP/sketchlib-go/sketches/FoldCountMinSketch"
	foldcountsketch "github.com/ProjectASAP/sketchlib-go/sketches/FoldCountSketch"
)

const (
	foldCompareRows      = 3
	foldCompareCols      = 4096
	foldCompareFoldLevel = 4
	foldCompareDomain    = 8192
	foldCompareSamples   = 250_000
	foldCompareQueries   = 4096
)

type sketchAccuracyMetrics struct {
	MeanAbsErr float64
	RMSE       float64
	P50AbsErr  float64
	P95AbsErr  float64
	P99AbsErr  float64
	MaxAbsErr  float64
}

func generateZipfInputs(samples, domain uint64, s float64, seed int64) ([]*common.SketchInput, map[uint64]float64) {
	rng := rand.New(rand.NewSource(seed))
	zipf := rand.NewZipf(rng, s, 1, domain-1)
	inputs := make([]*common.SketchInput, samples)
	truth := make(map[uint64]float64, domain)
	for i := uint64(0); i < samples; i++ {
		value := zipf.Uint64()
		inputs[i] = common.FromU64(value)
		truth[value]++
	}
	return inputs, truth
}

func sampleQueryKeys(truth map[uint64]float64, limit int) []*common.SketchInput {
	keys := make([]uint64, 0, len(truth))
	for key := range truth {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if len(keys) > limit {
		keys = keys[:limit]
	}
	out := make([]*common.SketchInput, len(keys))
	for i, key := range keys {
		out[i] = common.FromU64(key)
	}
	return out
}

func computeAccuracyMetrics(truth map[uint64]float64, estimate func(*common.SketchInput) float64) sketchAccuracyMetrics {
	if len(truth) == 0 {
		return sketchAccuracyMetrics{}
	}
	absErrs := make([]float64, 0, len(truth))
	var sumAbs float64
	var sumSq float64
	var maxAbs float64
	for key, exact := range truth {
		err := estimate(common.FromU64(key)) - exact
		absErr := math.Abs(err)
		absErrs = append(absErrs, absErr)
		sumAbs += absErr
		sumSq += err * err
		if absErr > maxAbs {
			maxAbs = absErr
		}
	}
	sort.Float64s(absErrs)
	pick := func(p float64) float64 {
		idx := int(math.Ceil(p*float64(len(absErrs)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(absErrs) {
			idx = len(absErrs) - 1
		}
		return absErrs[idx]
	}
	n := float64(len(absErrs))
	return sketchAccuracyMetrics{
		MeanAbsErr: sumAbs / n,
		RMSE:       math.Sqrt(sumSq / n),
		P50AbsErr:  pick(0.50),
		P95AbsErr:  pick(0.95),
		P99AbsErr:  pick(0.99),
		MaxAbsErr:  maxAbs,
	}
}

func logAccuracyComparison(t *testing.T, label string, full, folded sketchAccuracyMetrics) {
	t.Helper()
	t.Logf("%s accuracy", label)
	t.Logf("  full   mean_abs=%.3f rmse=%.3f p50=%.3f p95=%.3f p99=%.3f max=%.3f",
		full.MeanAbsErr, full.RMSE, full.P50AbsErr, full.P95AbsErr, full.P99AbsErr, full.MaxAbsErr)
	t.Logf("  folded mean_abs=%.3f rmse=%.3f p50=%.3f p95=%.3f p99=%.3f max=%.3f",
		folded.MeanAbsErr, folded.RMSE, folded.P50AbsErr, folded.P95AbsErr, folded.P99AbsErr, folded.MaxAbsErr)
}

func measureHeapBytes(alloc func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	alloc()
	runtime.ReadMemStats(&after)
	return after.HeapAlloc - before.HeapAlloc
}

func TestFoldedVsFullAccuracy(t *testing.T) {
	inputs, truth := generateZipfInputs(foldCompareSamples, foldCompareDomain, 1.1, 0x5eedc0de)

	fullCMS, err := countminsketch.NewCountMinSketch(foldCompareRows, foldCompareCols)
	if err != nil {
		t.Fatalf("new full cms: %v", err)
	}
	foldCMS, err := foldcountminsketch.NewFoldCountMinSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
	if err != nil {
		t.Fatalf("new folded cms: %v", err)
	}
	fullCS, err := countsketch.NewCountSketch(foldCompareRows, foldCompareCols)
	if err != nil {
		t.Fatalf("new full cs: %v", err)
	}
	foldCS, err := foldcountsketch.NewFoldCountSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
	if err != nil {
		t.Fatalf("new folded cs: %v", err)
	}

	for _, input := range inputs {
		fullCMS.Update(input)
		foldCMS.Update(input)
		fullCS.Update(input)
		foldCS.Update(input)
	}

	fullCMSMetrics := computeAccuracyMetrics(truth, fullCMS.Estimate)
	foldCMSMetrics := computeAccuracyMetrics(truth, foldCMS.Estimate)
	fullCSMetrics := computeAccuracyMetrics(truth, fullCS.Estimate)
	foldCSMetrics := computeAccuracyMetrics(truth, foldCS.Estimate)

	logAccuracyComparison(t, "Count-Min", fullCMSMetrics, foldCMSMetrics)
	logAccuracyComparison(t, "Count-Sketch", fullCSMetrics, foldCSMetrics)

	if foldCMSMetrics.MeanAbsErr != fullCMSMetrics.MeanAbsErr ||
		foldCMSMetrics.P99AbsErr != fullCMSMetrics.P99AbsErr ||
		foldCMSMetrics.MaxAbsErr != fullCMSMetrics.MaxAbsErr {
		t.Fatalf("folded CMS diverged from full CMS accuracy: full=%+v folded=%+v", fullCMSMetrics, foldCMSMetrics)
	}
	if foldCSMetrics.MeanAbsErr != fullCSMetrics.MeanAbsErr ||
		foldCSMetrics.P99AbsErr != fullCSMetrics.P99AbsErr ||
		foldCSMetrics.MaxAbsErr != fullCSMetrics.MaxAbsErr {
		t.Fatalf("folded CS diverged from full CS accuracy: full=%+v folded=%+v", fullCSMetrics, foldCSMetrics)
	}
}

func TestFoldedVsFullMemoryUsage(t *testing.T) {
	cmsFullAlloc := measureHeapBytes(func() {
		sketch, err := countminsketch.NewCountMinSketch(foldCompareRows, foldCompareCols)
		if err != nil {
			t.Fatalf("new full cms: %v", err)
		}
		runtime.KeepAlive(sketch)
	})
	cmsFoldAlloc := measureHeapBytes(func() {
		sketch, err := foldcountminsketch.NewFoldCountMinSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
		if err != nil {
			t.Fatalf("new folded cms: %v", err)
		}
		runtime.KeepAlive(sketch)
	})
	csFullAlloc := measureHeapBytes(func() {
		sketch, err := countsketch.NewCountSketch(foldCompareRows, foldCompareCols)
		if err != nil {
			t.Fatalf("new full cs: %v", err)
		}
		runtime.KeepAlive(sketch)
	})
	csFoldAlloc := measureHeapBytes(func() {
		sketch, err := foldcountsketch.NewFoldCountSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
		if err != nil {
			t.Fatalf("new folded cs: %v", err)
		}
		runtime.KeepAlive(sketch)
	})

	inputs, _ := generateZipfInputs(100_000, foldCompareDomain, 1.1, 0xabc123)

	cmsFullPop := measureHeapBytes(func() {
		sketch, _ := countminsketch.NewCountMinSketch(foldCompareRows, foldCompareCols)
		for _, input := range inputs {
			sketch.Update(input)
		}
		runtime.KeepAlive(sketch)
	})
	cmsFoldPop := measureHeapBytes(func() {
		sketch, _ := foldcountminsketch.NewFoldCountMinSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
		for _, input := range inputs {
			sketch.Update(input)
		}
		runtime.KeepAlive(sketch)
	})
	csFullPop := measureHeapBytes(func() {
		sketch, _ := countsketch.NewCountSketch(foldCompareRows, foldCompareCols)
		for _, input := range inputs {
			sketch.Update(input)
		}
		runtime.KeepAlive(sketch)
	})
	csFoldPop := measureHeapBytes(func() {
		sketch, _ := foldcountsketch.NewFoldCountSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
		for _, input := range inputs {
			sketch.Update(input)
		}
		runtime.KeepAlive(sketch)
	})

	t.Logf("Count-Min allocation bytes: full=%d folded=%d ratio=%.2fx", cmsFullAlloc, cmsFoldAlloc, float64(cmsFullAlloc)/float64(maxInt64(1, int64(cmsFoldAlloc))))
	t.Logf("Count-Min populated bytes:  full=%d folded=%d ratio=%.2fx", cmsFullPop, cmsFoldPop, float64(cmsFullPop)/float64(maxInt64(1, int64(cmsFoldPop))))
	t.Logf("Count-Sketch allocation bytes: full=%d folded=%d ratio=%.2fx", csFullAlloc, csFoldAlloc, float64(csFullAlloc)/float64(maxInt64(1, int64(csFoldAlloc))))
	t.Logf("Count-Sketch populated bytes:  full=%d folded=%d ratio=%.2fx", csFullPop, csFoldPop, float64(csFullPop)/float64(maxInt64(1, int64(csFoldPop))))
}

func BenchmarkFoldedVsFullCountMinInsert(b *testing.B) {
	inputs, _ := generateZipfInputs(200_000, foldCompareDomain, 1.1, 0x111111)
	b.Run("full", func(b *testing.B) {
		sketch, _ := countminsketch.NewCountMinSketch(foldCompareRows, foldCompareCols)
		b.ReportAllocs()
		b.SetBytes(8)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sketch.Update(inputs[i%len(inputs)])
		}
	})
	b.Run("folded", func(b *testing.B) {
		sketch, _ := foldcountminsketch.NewFoldCountMinSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
		b.ReportAllocs()
		b.SetBytes(8)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sketch.Update(inputs[i%len(inputs)])
		}
		b.ReportMetric(float64(sketch.TotalEntries()), "entries")
		b.ReportMetric(float64(sketch.CollidedCells()), "collided_cells")
	})
}

func BenchmarkFoldedVsFullCountMinQuery(b *testing.B) {
	inputs, truth := generateZipfInputs(200_000, foldCompareDomain, 1.1, 0x222222)
	queries := sampleQueryKeys(truth, foldCompareQueries)
	full, _ := countminsketch.NewCountMinSketch(foldCompareRows, foldCompareCols)
	folded, _ := foldcountminsketch.NewFoldCountMinSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
	for _, input := range inputs {
		full.Update(input)
		folded.Update(input)
	}

	b.Run("full", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = full.Estimate(queries[i%len(queries)])
		}
	})
	b.Run("folded", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = folded.Estimate(queries[i%len(queries)])
		}
	})
}

func BenchmarkFoldedVsFullCountSketchInsert(b *testing.B) {
	inputs, _ := generateZipfInputs(200_000, foldCompareDomain, 1.1, 0x333333)
	b.Run("full", func(b *testing.B) {
		sketch, _ := countsketch.NewCountSketch(foldCompareRows, foldCompareCols)
		b.ReportAllocs()
		b.SetBytes(8)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sketch.Update(inputs[i%len(inputs)])
		}
	})
	b.Run("folded", func(b *testing.B) {
		sketch, _ := foldcountsketch.NewFoldCountSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
		b.ReportAllocs()
		b.SetBytes(8)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sketch.Update(inputs[i%len(inputs)])
		}
		b.ReportMetric(float64(sketch.TotalEntries()), "entries")
		b.ReportMetric(float64(sketch.CollidedCells()), "collided_cells")
	})
}

func BenchmarkFoldedVsFullCountSketchQuery(b *testing.B) {
	inputs, truth := generateZipfInputs(200_000, foldCompareDomain, 1.1, 0x444444)
	queries := sampleQueryKeys(truth, foldCompareQueries)
	full, _ := countsketch.NewCountSketch(foldCompareRows, foldCompareCols)
	folded, _ := foldcountsketch.NewFoldCountSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
	for _, input := range inputs {
		full.Update(input)
		folded.Update(input)
	}

	b.Run("full", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = full.Estimate(queries[i%len(queries)])
		}
	})
	b.Run("folded", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = folded.Estimate(queries[i%len(queries)])
		}
	})
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func TestFoldedVsFullSummary(t *testing.T) {
	inputs, truth := generateZipfInputs(100_000, foldCompareDomain, 1.1, 0x555555)
	fullCMS, _ := countminsketch.NewCountMinSketch(foldCompareRows, foldCompareCols)
	foldCMS, _ := foldcountminsketch.NewFoldCountMinSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
	fullCS, _ := countsketch.NewCountSketch(foldCompareRows, foldCompareCols)
	foldCS, _ := foldcountsketch.NewFoldCountSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
	for _, input := range inputs {
		fullCMS.Update(input)
		foldCMS.Update(input)
		fullCS.Update(input)
		foldCS.Update(input)
	}
	t.Logf("summary cms: full_cells=%d folded_cells=%d folded_entries=%d folded_collisions=%d",
		foldCompareRows*foldCompareCols, len(foldCMS.Cells), foldCMS.TotalEntries(), foldCMS.CollidedCells())
	t.Logf("summary cs:  full_cells=%d folded_cells=%d folded_entries=%d folded_collisions=%d",
		foldCompareRows*foldCompareCols, len(foldCS.Cells), foldCS.TotalEntries(), foldCS.CollidedCells())
	for _, key := range sampleQueryKeys(truth, 5) {
		t.Logf("sample key=%x cms(full=%.0f folded=%.0f) cs(full=%.0f folded=%.0f)",
			key.Bytes, fullCMS.Estimate(key), foldCMS.Estimate(key), fullCS.Estimate(key), foldCS.Estimate(key))
	}
}

func BenchmarkFoldedVsFullSummary(b *testing.B) {
	inputs, _ := generateZipfInputs(100_000, foldCompareDomain, 1.1, 0x666666)
	for _, tc := range []struct {
		name string
		make func() (func(*common.SketchInput), string)
	}{
		{
			name: "cms_full",
			make: func() (func(*common.SketchInput), string) {
				sketch, _ := countminsketch.NewCountMinSketch(foldCompareRows, foldCompareCols)
				return sketch.Update, fmt.Sprintf("%d", foldCompareRows*foldCompareCols)
			},
		},
		{
			name: "cms_folded",
			make: func() (func(*common.SketchInput), string) {
				sketch, _ := foldcountminsketch.NewFoldCountMinSketch(foldCompareRows, foldCompareCols, foldCompareFoldLevel)
				return sketch.Update, fmt.Sprintf("%d", len(sketch.Cells))
			},
		},
	} {
		b.Run(tc.name, func(b *testing.B) {
			insert, cells := tc.make()
			_ = cells
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				insert(inputs[i%len(inputs)])
			}
		})
	}
}
