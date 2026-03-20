package benchmark

// octosketch_benchmark_test.go compares insertion, query, and update throughput
// between the four independent sketch implementations (CountMin, CountSketch, HLL,
// DDSketch) and their OctoSketch-optimised counterparts.
//
// Test organisation:
//
//	Section 1 — Insert Throughput     (Standalone vs OctoSketch, 1-worker and 4-worker)
//	Section 2 — Query Throughput      (Standalone vs Aggregator.Query)
//	Section 3 — Update Throughput     (Weighted standalone vs OctoSketch pipeline)
//	Section 4 — Worker Scaling        (1 / 2 / 4 / 8 workers, wall-clock)
//	Section 5 — Adaptive Tau          (Fixed-τ vs adaptive-τ throughput)
//	Section 6 — Comparison Report     (human-readable side-by-side summary)
//
// OctoSketch pipeline shutdown sequence (from aggregator.go §5):
//  1. Close each worker input channel.
//  2. Wait for all Worker.Done().
//  3. Stop the TauController (if adaptive).
//  4. Close the delta channel.
//  5. Wait for Aggregator.Done().

import (
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	octosketch "github.com/ProjectASAP/sketchlib-go/sketch_framework/OctoSketch"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
	ddsketch "github.com/ProjectASAP/sketchlib-go/sketches/DDSketch"
	hll "github.com/ProjectASAP/sketchlib-go/sketches/HLL"
)

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

const (
	octoCMRows   = 5
	octoCMCols   = 2048 // CountMin columns
	octoCSRows   = 5
	octoCSCols   = 2048 // CountSketch columns (must be power-of-2)
	octoDDAlpha  = 0.01 // DDSketch relative-error parameter
	octoDeltaBuf = 4096 // shared delta channel buffer size
	octoTau      = 2.0  // fixed emission threshold for non-adaptive benchmarks
)

// ─────────────────────────────────────────────────────────────────────────────
// Data-loading helpers
// ─────────────────────────────────────────────────────────────────────────────

// loadCAIDAInputs converts the CAIDA hash stream into SketchInput values for
// the OctoSketch worker input channels (CountMin / CountSketch / HLL paths).
func loadCAIDAInputs(tb testing.TB) []*common.SketchInput {
	hashes, n := LoadCAIDAHelper(tb)
	inputs := make([]*common.SketchInput, n)
	for i, h := range hashes {
		inputs[i] = common.FromU64(h)
	}
	return inputs
}

// loadCAIDAF64Inputs converts CAIDA hashes to positive float64 SketchInputs
// suitable for DDSketch (which requires positive values).
// Values are mapped to the range [0.1, 1000.0].
func loadCAIDAF64Inputs(tb testing.TB) ([]*common.SketchInput, []float64) {
	hashes, n := LoadCAIDAHelper(tb)
	inputs := make([]*common.SketchInput, n)
	values := make([]float64, n)
	for i, h := range hashes {
		// Map 64-bit hash to a positive float in [0.1, 1000.0].
		v := float64(h%10000+1) / 10.0
		values[i] = v
		inputs[i] = common.FromF64(v)
	}
	return inputs, values
}

// ─────────────────────────────────────────────────────────────────────────────
// Pipeline helpers
// ─────────────────────────────────────────────────────────────────────────────

// drainOctoFixedTau sends all inputs through numWorkers workers in round-robin,
// waits for full pipeline drain, and returns the populated Aggregator.
// The system uses a fixed τ (no adaptive controller).
func drainOctoFixed(
	tb testing.TB,
	agg *octosketch.Aggregator,
	workers []*octosketch.Worker,
	inputChans []chan<- *common.SketchInput,
	deltaCh chan octosketch.DeltaUpdate,
	inputs []*common.SketchInput,
) {
	tb.Helper()
	numW := len(inputChans)
	for i, inp := range inputs {
		inputChans[i%numW] <- inp
	}
	for _, ch := range inputChans {
		close(ch)
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(done <-chan struct{}) { defer wg.Done(); <-done }(w.Done())
	}
	wg.Wait()
	close(deltaCh)
	<-agg.Done()
}

// drainOctoAdaptive is like drainOctoFixed but stops the TauController before
// closing the delta channel.
func drainOctoAdaptive(
	tb testing.TB,
	agg *octosketch.Aggregator,
	workers []*octosketch.Worker,
	inputChans []chan<- *common.SketchInput,
	deltaCh chan octosketch.DeltaUpdate,
	tauCtrl *octosketch.TauController,
	inputs []*common.SketchInput,
) {
	tb.Helper()
	numW := len(inputChans)
	for i, inp := range inputs {
		inputChans[i%numW] <- inp
	}
	for _, ch := range inputChans {
		close(ch)
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(done <-chan struct{}) { defer wg.Done(); <-done }(w.Done())
	}
	wg.Wait()
	tauCtrl.Stop()
	close(deltaCh)
	<-agg.Done()
}

// octoThroughputMops measures end-to-end pipeline throughput in millions of
// items per second.  It starts all goroutines, times the full send→drain cycle,
// and returns the result.
func octoThroughputMops(
	t *testing.T,
	agg *octosketch.Aggregator,
	workers []*octosketch.Worker,
	inputChans []chan<- *common.SketchInput,
	deltaCh chan octosketch.DeltaUpdate,
	tauCtrl *octosketch.TauController,
	inputs []*common.SketchInput,
) float64 {
	t.Helper()
	agg.Run()
	for _, w := range workers {
		w.Run()
	}
	if tauCtrl != nil {
		tauCtrl.Run()
	}

	start := time.Now()
	numW := len(inputChans)
	for i, inp := range inputs {
		inputChans[i%numW] <- inp
	}
	for _, ch := range inputChans {
		close(ch)
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(done <-chan struct{}) { defer wg.Done(); <-done }(w.Done())
	}
	wg.Wait()
	if tauCtrl != nil {
		tauCtrl.Stop()
	}
	close(deltaCh)
	<-agg.Done()
	elapsed := time.Since(start)

	return float64(len(inputs)) / elapsed.Seconds() / 1e6
}

// ─────────────────────────────────────────────────────────────────────────────
// Section 1 — Insert Throughput
// ─────────────────────────────────────────────────────────────────────────────

// --- CountMin ---

func BenchmarkOcto_Insert_CountMin_Standalone(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cms, _ := countminsketch.NewCountMinSketch(octoCMRows, octoCMCols)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cms.InsertWithHash(hashes[i%n])
	}
}

// BenchmarkOcto_Insert_CountMin_1Worker measures ingestion rate when feeding a
// single OctoSketch worker (channel send + concurrent processing).
func BenchmarkOcto_Insert_CountMin_1Worker(b *testing.B) {
	inputs := loadCAIDAInputs(b)
	n := len(inputs)

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewCountMinSystem(octoCMRows, octoCMCols, octoTau, 1, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		inputChans[0] <- inputs[i%n]
	}
	b.StopTimer()

	close(inputChans[0])
	<-workers[0].Done()
	close(deltaCh)
	<-agg.Done()
}

func BenchmarkOcto_Insert_CountMin_4Workers(b *testing.B) {
	inputs := loadCAIDAInputs(b)
	n := len(inputs)
	const numW = 4

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewCountMinSystem(octoCMRows, octoCMCols, octoTau, numW, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	var idx atomic.Uint64
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := idx.Add(1) - 1
			inputChans[i%numW] <- inputs[i%uint64(n)]
		}
	})
	b.StopTimer()

	for _, ch := range inputChans {
		close(ch)
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(done <-chan struct{}) { defer wg.Done(); <-done }(w.Done())
	}
	wg.Wait()
	close(deltaCh)
	<-agg.Done()
}

// --- CountSketch ---

func BenchmarkOcto_Insert_CountSketch_Standalone(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cs, _ := countsketch.NewCountSketch(octoCSRows, octoCSCols)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cs.InsertWithHash(hashes[i%n])
	}
}

func BenchmarkOcto_Insert_CountSketch_1Worker(b *testing.B) {
	inputs := loadCAIDAInputs(b)
	n := len(inputs)

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewCountSketchSystem(octoCSRows, octoCSCols, octoTau, 1, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	workers[0].Run()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		inputChans[0] <- inputs[i%n]
	}
	b.StopTimer()

	close(inputChans[0])
	<-workers[0].Done()
	close(deltaCh)
	<-agg.Done()
}

func BenchmarkOcto_Insert_CountSketch_4Workers(b *testing.B) {
	inputs := loadCAIDAInputs(b)
	n := len(inputs)
	const numW = 4

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewCountSketchSystem(octoCSRows, octoCSCols, octoTau, numW, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	var idx atomic.Uint64
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := idx.Add(1) - 1
			inputChans[i%numW] <- inputs[i%uint64(n)]
		}
	})
	b.StopTimer()

	for _, ch := range inputChans {
		close(ch)
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(done <-chan struct{}) { defer wg.Done(); <-done }(w.Done())
	}
	wg.Wait()
	close(deltaCh)
	<-agg.Done()
}

// --- HLL ---

func BenchmarkOcto_Insert_HLL_Standalone(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	h := hll.NewHyperLogLog()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h.InsertWithHash(hashes[i%n])
	}
}

func BenchmarkOcto_Insert_HLL_1Worker(b *testing.B) {
	inputs := loadCAIDAInputs(b)
	n := len(inputs)

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewHLLSystem(1, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	workers[0].Run()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		inputChans[0] <- inputs[i%n]
	}
	b.StopTimer()

	close(inputChans[0])
	<-workers[0].Done()
	close(deltaCh)
	<-agg.Done()
}

func BenchmarkOcto_Insert_HLL_4Workers(b *testing.B) {
	inputs := loadCAIDAInputs(b)
	n := len(inputs)
	const numW = 4

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewHLLSystem(numW, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	var idx atomic.Uint64
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := idx.Add(1) - 1
			inputChans[i%numW] <- inputs[i%uint64(n)]
		}
	})
	b.StopTimer()

	for _, ch := range inputChans {
		close(ch)
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(done <-chan struct{}) { defer wg.Done(); <-done }(w.Done())
	}
	wg.Wait()
	close(deltaCh)
	<-agg.Done()
}

// --- DDSketch ---

func BenchmarkOcto_Insert_DDSketch_Standalone(b *testing.B) {
	_, values := loadCAIDAF64Inputs(b)
	n := len(values)
	dd := ddsketch.NewDDSketch(octoDDAlpha)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dd.Add(values[i%n])
	}
}

func BenchmarkOcto_Insert_DDSketch_1Worker(b *testing.B) {
	inputs, _ := loadCAIDAF64Inputs(b)
	n := len(inputs)

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewDDSketchSystem(octoDDAlpha, octoTau, 1, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	workers[0].Run()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		inputChans[0] <- inputs[i%n]
	}
	b.StopTimer()

	close(inputChans[0])
	<-workers[0].Done()
	close(deltaCh)
	<-agg.Done()
}

func BenchmarkOcto_Insert_DDSketch_4Workers(b *testing.B) {
	inputs, _ := loadCAIDAF64Inputs(b)
	n := len(inputs)
	const numW = 4

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewDDSketchSystem(octoDDAlpha, octoTau, numW, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	var idx atomic.Uint64
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := idx.Add(1) - 1
			inputChans[i%numW] <- inputs[i%uint64(n)]
		}
	})
	b.StopTimer()

	for _, ch := range inputChans {
		close(ch)
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(done <-chan struct{}) { defer wg.Done(); <-done }(w.Done())
	}
	wg.Wait()
	close(deltaCh)
	<-agg.Done()
}

// ─────────────────────────────────────────────────────────────────────────────
// Section 2 — Query Throughput
// Sketches are pre-filled before the timer starts.
// ─────────────────────────────────────────────────────────────────────────────

func BenchmarkOcto_Query_CountMin_Standalone(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cms, _ := countminsketch.NewCountMinSketch(octoCMRows, octoCMCols)
	for _, h := range hashes {
		cms.InsertWithHash(h)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = cms.QueryWithHash(common.QueryFrequency, hashes[i%n])
	}
}

func BenchmarkOcto_Query_CountMin_Aggregator(b *testing.B) {
	inputs := loadCAIDAInputs(b)
	n := len(inputs)

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewCountMinSystem(octoCMRows, octoCMCols, octoTau, 4, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}
	drainOctoFixed(b, agg, workers, inputChans, deltaCh, inputs)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = agg.Query(inputs[i%n])
	}
}

func BenchmarkOcto_Query_CountSketch_Standalone(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cs, _ := countsketch.NewCountSketch(octoCSRows, octoCSCols)
	for _, h := range hashes {
		cs.InsertWithHash(h)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = cs.QueryWithHash(common.QueryFrequency, hashes[i%n])
	}
}

func BenchmarkOcto_Query_CountSketch_Aggregator(b *testing.B) {
	inputs := loadCAIDAInputs(b)
	n := len(inputs)

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewCountSketchSystem(octoCSRows, octoCSCols, octoTau, 4, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}
	drainOctoFixed(b, agg, workers, inputChans, deltaCh, inputs)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = agg.Query(inputs[i%n])
	}
}

func BenchmarkOcto_Query_HLL_Standalone(b *testing.B) {
	hashes, _ := LoadCAIDAHelper(b)
	h := hll.NewHyperLogLog()
	for _, hash := range hashes {
		h.InsertWithHash(hash)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = h.EstimateCardinality()
	}
}

func BenchmarkOcto_Query_HLL_Aggregator(b *testing.B) {
	inputs := loadCAIDAInputs(b)
	probe := inputs[0] // HLL ignores the input, returns global cardinality

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewHLLSystem(4, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}
	drainOctoFixed(b, agg, workers, inputChans, deltaCh, inputs)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = agg.Query(probe)
	}
}

func BenchmarkOcto_Query_DDSketch_Standalone(b *testing.B) {
	_, values := loadCAIDAF64Inputs(b)
	dd := ddsketch.NewDDSketch(octoDDAlpha)
	for _, v := range values {
		dd.Add(v)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = dd.GetValueAtQuantile(0.5)
	}
}

func BenchmarkOcto_Query_DDSketch_Aggregator(b *testing.B) {
	inputs, _ := loadCAIDAF64Inputs(b)
	// phi=0.5 → median query
	probe := common.FromF64(0.5)

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewDDSketchSystem(octoDDAlpha, octoTau, 4, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}
	drainOctoFixed(b, agg, workers, inputChans, deltaCh, inputs)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = agg.Query(probe)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Section 3 — Update (Weighted Insert) Throughput
//
// Standalone sketches support weighted updates (e.g. FastInsertManyWithHashValue
// for CMS, InsertWithHashAndValue for CountSketch).  The OctoSketch pipeline
// always applies unit increments; values are encoded in SketchInput.Bytes for
// DDSketch only.  This section quantifies the overhead of weighted updates on
// the standalone path and compares it to OctoSketch's fixed-unit pipeline.
// ─────────────────────────────────────────────────────────────────────────────

func BenchmarkOcto_Update_CountMin_Standalone_Weighted(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cms, _ := countminsketch.NewCountMinSketch(octoCMRows, octoCMCols)

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(8)
	for i := 0; i < b.N; i++ {
		// Weighted update: count += float64 value (simulates flow byte-count).
		cms.FastInsertManyWithHashValue(hashes[i%n], float64(i%100+1))
	}
}

// BenchmarkOcto_Update_CountMin_OctoSketch_4Workers measures how fast the
// OctoSketch pipeline processes unit-increment updates across 4 workers.
// OctoSketch always applies +1 per row; compare with the weighted standalone above.
func BenchmarkOcto_Update_CountMin_OctoSketch_4Workers(b *testing.B) {
	inputs := loadCAIDAInputs(b)
	n := len(inputs)
	const numW = 4

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewCountMinSystem(octoCMRows, octoCMCols, octoTau, numW, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	var idx atomic.Uint64
	b.SetBytes(8)
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := idx.Add(1) - 1
			inputChans[i%numW] <- inputs[i%uint64(n)]
		}
	})
	b.StopTimer()

	for _, ch := range inputChans {
		close(ch)
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(done <-chan struct{}) { defer wg.Done(); <-done }(w.Done())
	}
	wg.Wait()
	close(deltaCh)
	<-agg.Done()
}

func BenchmarkOcto_Update_CountSketch_Standalone_Weighted(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	cs, _ := countsketch.NewCountSketch(octoCSRows, octoCSCols)

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(8)
	for i := 0; i < b.N; i++ {
		cs.InsertWithHashAndValue(hashes[i%n], float64(i%100+1))
	}
}

func BenchmarkOcto_Update_CountSketch_OctoSketch_4Workers(b *testing.B) {
	inputs := loadCAIDAInputs(b)
	n := len(inputs)
	const numW = 4

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewCountSketchSystem(octoCSRows, octoCSCols, octoTau, numW, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	var idx atomic.Uint64
	b.SetBytes(8)
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := idx.Add(1) - 1
			inputChans[i%numW] <- inputs[i%uint64(n)]
		}
	})
	b.StopTimer()

	for _, ch := range inputChans {
		close(ch)
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(done <-chan struct{}) { defer wg.Done(); <-done }(w.Done())
	}
	wg.Wait()
	close(deltaCh)
	<-agg.Done()
}

// DDSketch update: values encoded in SketchInput.Bytes both for standalone and OctoSketch.
func BenchmarkOcto_Update_DDSketch_Standalone(b *testing.B) {
	_, values := loadCAIDAF64Inputs(b)
	n := len(values)
	dd := ddsketch.NewDDSketch(octoDDAlpha)

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(8)
	for i := 0; i < b.N; i++ {
		dd.Add(values[i%n])
	}
}

func BenchmarkOcto_Update_DDSketch_OctoSketch_4Workers(b *testing.B) {
	inputs, _ := loadCAIDAF64Inputs(b)
	n := len(inputs)
	const numW = 4

	agg, workers, inputChans, deltaCh, err :=
		octosketch.NewDDSketchSystem(octoDDAlpha, octoTau, numW, octoDeltaBuf)
	if err != nil {
		b.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	var idx atomic.Uint64
	b.SetBytes(8)
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := idx.Add(1) - 1
			inputChans[i%numW] <- inputs[i%uint64(n)]
		}
	})
	b.StopTimer()

	for _, ch := range inputChans {
		close(ch)
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(done <-chan struct{}) { defer wg.Done(); <-done }(w.Done())
	}
	wg.Wait()
	close(deltaCh)
	<-agg.Done()
}

// ─────────────────────────────────────────────────────────────────────────────
// Section 4 — Worker Scaling
//
// For each sketch type, run the full pipeline with 1, 2, 4, and 8 workers.
// Results are reported as end-to-end Mops/s so they are directly comparable.
// ─────────────────────────────────────────────────────────────────────────────

func testWorkerScaling(
	t *testing.T,
	sketchName string,
	workerCounts []int,
	inputs []*common.SketchInput,
	newSystem func(numW int) (
		*octosketch.Aggregator,
		[]*octosketch.Worker,
		[]chan<- *common.SketchInput,
		chan octosketch.DeltaUpdate,
		*octosketch.TauController,
		error,
	),
) {
	t.Helper()
	t.Logf("=== Worker Scaling: %s (%d items) ===", sketchName, len(inputs))
	t.Logf("  %-8s  %12s", "Workers", "Mops/s")

	for _, numW := range workerCounts {
		agg, workers, inputChans, deltaCh, tauCtrl, err := newSystem(numW)
		if err != nil {
			t.Fatalf("newSystem(%d): %v", numW, err)
		}
		mops := octoThroughputMops(t, agg, workers, inputChans, deltaCh, tauCtrl, inputs)
		t.Logf("  %-8d  %12.2f", numW, mops)
	}
}

func TestOcto_WorkerScaling_CountMin(t *testing.T) {
	inputs := loadCAIDAInputs(t)
	workerCounts := []int{1, 2, 4, 8}
	testWorkerScaling(t, "CountMin", workerCounts, inputs,
		func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, *octosketch.TauController, error) {
			return octosketch.NewCountMinSystemAdaptive(
				octoCMRows, octoCMCols,
				octosketch.DefaultTauConfig(octoTau, octoDeltaBuf),
				numW, octoDeltaBuf,
			)
		},
	)
}

func TestOcto_WorkerScaling_CountSketch(t *testing.T) {
	inputs := loadCAIDAInputs(t)
	workerCounts := []int{1, 2, 4, 8}
	testWorkerScaling(t, "CountSketch", workerCounts, inputs,
		func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, *octosketch.TauController, error) {
			return octosketch.NewCountSketchSystemAdaptive(
				octoCSRows, octoCSCols,
				octosketch.DefaultTauConfig(octoTau, octoDeltaBuf),
				numW, octoDeltaBuf,
			)
		},
	)
}

func TestOcto_WorkerScaling_HLL(t *testing.T) {
	inputs := loadCAIDAInputs(t)
	workerCounts := []int{1, 2, 4, 8}
	testWorkerScaling(t, "HLL", workerCounts, inputs,
		func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, *octosketch.TauController, error) {
			return octosketch.NewHLLSystemAdaptive(
				octosketch.DefaultTauConfig(octoTau, octoDeltaBuf),
				numW, octoDeltaBuf,
			)
		},
	)
}

func TestOcto_WorkerScaling_DDSketch(t *testing.T) {
	inputs, _ := loadCAIDAF64Inputs(t)
	workerCounts := []int{1, 2, 4, 8}
	testWorkerScaling(t, "DDSketch", workerCounts, inputs,
		func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, *octosketch.TauController, error) {
			return octosketch.NewDDSketchSystemAdaptive(
				octoDDAlpha,
				octosketch.DefaultTauConfig(octoTau, octoDeltaBuf),
				numW, octoDeltaBuf,
			)
		},
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// Section 5 — Adaptive Tau vs Fixed Tau
//
// Shows how the TauController reduces delta channel pressure under high load
// by increasing τ, and how that affects end-to-end throughput.
// ─────────────────────────────────────────────────────────────────────────────

func TestOcto_AdaptiveTau_CountMin(t *testing.T) {
	inputs := loadCAIDAInputs(t)
	const numW = 4

	// Fixed τ=2: always emit when cell reaches 2 (high delta volume).
	{
		agg, workers, inputChans, deltaCh, err :=
			octosketch.NewCountMinSystem(octoCMRows, octoCMCols, 2.0, numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		agg.Run()
		for _, w := range workers {
			w.Run()
		}
		start := time.Now()
		drainOctoFixed(t, agg, workers, inputChans, deltaCh, inputs)
		fixedDur := time.Since(start)
		fixedMops := float64(len(inputs)) / fixedDur.Seconds() / 1e6
		t.Logf("Fixed  τ=2.0   → %.2f Mops/s (%v)", fixedMops, fixedDur.Round(time.Millisecond))
	}

	// Adaptive τ starting at 2, allowed to rise to 20 under backpressure.
	{
		tauCfg := octosketch.DefaultTauConfig(2.0, octoDeltaBuf)
		agg, workers, inputChans, deltaCh, tauCtrl, err :=
			octosketch.NewCountMinSystemAdaptive(octoCMRows, octoCMCols, tauCfg, numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		agg.Run()
		for _, w := range workers {
			w.Run()
		}
		tauCtrl.Run()
		start := time.Now()
		drainOctoAdaptive(t, agg, workers, inputChans, deltaCh, tauCtrl, inputs)
		adaptDur := time.Since(start)
		adaptMops := float64(len(inputs)) / adaptDur.Seconds() / 1e6
		finalTau := tauCtrl.Tau().Current()
		t.Logf("Adaptive τ∈[1,20] → %.2f Mops/s (%v) final τ=%.1f", adaptMops, adaptDur.Round(time.Millisecond), finalTau)
	}
}

func TestOcto_AdaptiveTau_HLL(t *testing.T) {
	inputs := loadCAIDAInputs(t)
	const numW = 4

	{
		agg, workers, inputChans, deltaCh, err :=
			octosketch.NewHLLSystem(numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		agg.Run()
		for _, w := range workers {
			w.Run()
		}
		start := time.Now()
		drainOctoFixed(t, agg, workers, inputChans, deltaCh, inputs)
		dur := time.Since(start)
		t.Logf("Fixed  τ (HLL always emits)  → %.2f Mops/s (%v)", float64(len(inputs))/dur.Seconds()/1e6, dur.Round(time.Millisecond))
	}

	{
		tauCfg := octosketch.DefaultTauConfig(1.0, octoDeltaBuf)
		agg, workers, inputChans, deltaCh, tauCtrl, err :=
			octosketch.NewHLLSystemAdaptive(tauCfg, numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		agg.Run()
		for _, w := range workers {
			w.Run()
		}
		tauCtrl.Run()
		start := time.Now()
		drainOctoAdaptive(t, agg, workers, inputChans, deltaCh, tauCtrl, inputs)
		dur := time.Since(start)
		t.Logf("Adaptive τ (HLL)             → %.2f Mops/s (%v) final τ=%.1f", float64(len(inputs))/dur.Seconds()/1e6, dur.Round(time.Millisecond), tauCtrl.Tau().Current())
	}
}

func TestOcto_AdaptiveTau_DDSketch(t *testing.T) {
	inputs, _ := loadCAIDAF64Inputs(t)
	const numW = 4

	{
		agg, workers, inputChans, deltaCh, err :=
			octosketch.NewDDSketchSystem(octoDDAlpha, 2.0, numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		agg.Run()
		for _, w := range workers {
			w.Run()
		}
		start := time.Now()
		drainOctoFixed(t, agg, workers, inputChans, deltaCh, inputs)
		dur := time.Since(start)
		t.Logf("Fixed  τ=2.0   → %.2f Mops/s (%v)", float64(len(inputs))/dur.Seconds()/1e6, dur.Round(time.Millisecond))
	}

	{
		tauCfg := octosketch.DefaultTauConfig(2.0, octoDeltaBuf)
		agg, workers, inputChans, deltaCh, tauCtrl, err :=
			octosketch.NewDDSketchSystemAdaptive(octoDDAlpha, tauCfg, numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		agg.Run()
		for _, w := range workers {
			w.Run()
		}
		tauCtrl.Run()
		start := time.Now()
		drainOctoAdaptive(t, agg, workers, inputChans, deltaCh, tauCtrl, inputs)
		dur := time.Since(start)
		t.Logf("Adaptive τ∈[1,20] → %.2f Mops/s (%v) final τ=%.1f", float64(len(inputs))/dur.Seconds()/1e6, dur.Round(time.Millisecond), tauCtrl.Tau().Current())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Section 6 — Comprehensive Comparison Report
//
// TestOcto_ComparisonReport measures all four sketches in both standalone and
// OctoSketch modes (insert + query + update) and prints a side-by-side table.
// Run with: go test ./benchmark -run TestOcto_ComparisonReport -v
// ─────────────────────────────────────────────────────────────────────────────

type octoResult struct {
	sketch    string
	mode      string
	op        string
	mops      float64
	p50ns     int64
	p99ns     int64
}

func sampleLatency(f func(), samples int) (p50, p99 int64) {
	lats := make([]int64, samples)
	for i := range lats {
		t0 := time.Now()
		f()
		lats[i] = time.Since(t0).Nanoseconds()
	}
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	p50 = lats[int(float64(samples)*0.50)]
	p99 = lats[int(float64(samples)*0.99)]
	return
}

func hashToIPBytes(h uint64) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(h))
	return b
}

func TestOcto_ComparisonReport(t *testing.T) {
	hashes, n := LoadCAIDAHelper(t)
	inputs := loadCAIDAInputs(t)
	ddInputs, ddValues := loadCAIDAF64Inputs(t)

	const numW = 4
	const samples = 50_000
	var results []octoResult

	// ── CountMin ──────────────────────────────────────────────────────────────

	// Standalone insert
	{
		cms, _ := countminsketch.NewCountMinSketch(octoCMRows, octoCMCols)
		start := time.Now()
		for _, h := range hashes {
			cms.InsertWithHash(h)
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { cms.InsertWithHash(hashes[0]) }, samples)
		results = append(results, octoResult{"CountMin", "Standalone", "Insert", mops, p50, p99})
	}

	// Standalone query
	{
		cms, _ := countminsketch.NewCountMinSketch(octoCMRows, octoCMCols)
		for _, h := range hashes {
			cms.InsertWithHash(h)
		}
		start := time.Now()
		for i, h := range hashes {
			_, _ = cms.QueryWithHash(common.QueryFrequency, hashes[i%n])
			_ = h
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() {
			_, _ = cms.QueryWithHash(common.QueryFrequency, hashes[0])
		}, samples)
		results = append(results, octoResult{"CountMin", "Standalone", "Query", mops, p50, p99})
	}

	// Standalone weighted update
	{
		cms, _ := countminsketch.NewCountMinSketch(octoCMRows, octoCMCols)
		start := time.Now()
		for i, h := range hashes {
			cms.FastInsertManyWithHashValue(h, float64(i%100+1))
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { cms.FastInsertManyWithHashValue(hashes[0], 5) }, samples)
		results = append(results, octoResult{"CountMin", "Standalone", "Update(weighted)", mops, p50, p99})
	}

	// OctoSketch insert (end-to-end)
	{
		agg, workers, inputChans, deltaCh, tauCtrl, err :=
			octosketch.NewCountMinSystemAdaptive(octoCMRows, octoCMCols,
				octosketch.DefaultTauConfig(octoTau, octoDeltaBuf), numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		mops := octoThroughputMops(t, agg, workers, inputChans, deltaCh, tauCtrl, inputs)
		results = append(results, octoResult{"CountMin", fmt.Sprintf("OctoSketch(%dw)", numW), "Insert", mops, 0, 0})
	}

	// OctoSketch query (aggregator, after full drain)
	{
		agg, workers, inputChans, deltaCh, err :=
			octosketch.NewCountMinSystem(octoCMRows, octoCMCols, octoTau, numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		agg.Run()
		for _, w := range workers {
			w.Run()
		}
		drainOctoFixed(t, agg, workers, inputChans, deltaCh, inputs)

		start := time.Now()
		for _, inp := range inputs {
			_ = agg.Query(inp)
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { _ = agg.Query(inputs[0]) }, samples)
		results = append(results, octoResult{"CountMin", fmt.Sprintf("OctoSketch(%dw)", numW), "Query", mops, p50, p99})
	}

	// ── CountSketch ───────────────────────────────────────────────────────────

	{
		cs, _ := countsketch.NewCountSketch(octoCSRows, octoCSCols)
		start := time.Now()
		for _, h := range hashes {
			cs.InsertWithHash(h)
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { cs.InsertWithHash(hashes[0]) }, samples)
		results = append(results, octoResult{"CountSketch", "Standalone", "Insert", mops, p50, p99})
	}

	{
		cs, _ := countsketch.NewCountSketch(octoCSRows, octoCSCols)
		for _, h := range hashes {
			cs.InsertWithHash(h)
		}
		start := time.Now()
		for _, h := range hashes {
			_, _ = cs.QueryWithHash(common.QueryFrequency, h)
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() {
			_, _ = cs.QueryWithHash(common.QueryFrequency, hashes[0])
		}, samples)
		results = append(results, octoResult{"CountSketch", "Standalone", "Query", mops, p50, p99})
	}

	{
		cs, _ := countsketch.NewCountSketch(octoCSRows, octoCSCols)
		start := time.Now()
		for i, h := range hashes {
			cs.InsertWithHashAndValue(h, float64(i%100+1))
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { cs.InsertWithHashAndValue(hashes[0], 5) }, samples)
		results = append(results, octoResult{"CountSketch", "Standalone", "Update(weighted)", mops, p50, p99})
	}

	{
		agg, workers, inputChans, deltaCh, tauCtrl, err :=
			octosketch.NewCountSketchSystemAdaptive(octoCSRows, octoCSCols,
				octosketch.DefaultTauConfig(octoTau, octoDeltaBuf), numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		mops := octoThroughputMops(t, agg, workers, inputChans, deltaCh, tauCtrl, inputs)
		results = append(results, octoResult{"CountSketch", fmt.Sprintf("OctoSketch(%dw)", numW), "Insert", mops, 0, 0})
	}

	{
		agg, workers, inputChans, deltaCh, err :=
			octosketch.NewCountSketchSystem(octoCSRows, octoCSCols, octoTau, numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		agg.Run()
		for _, w := range workers {
			w.Run()
		}
		drainOctoFixed(t, agg, workers, inputChans, deltaCh, inputs)

		start := time.Now()
		for _, inp := range inputs {
			_ = agg.Query(inp)
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { _ = agg.Query(inputs[0]) }, samples)
		results = append(results, octoResult{"CountSketch", fmt.Sprintf("OctoSketch(%dw)", numW), "Query", mops, p50, p99})
	}

	// ── HLL ───────────────────────────────────────────────────────────────────

	{
		h := hll.NewHyperLogLog()
		start := time.Now()
		for _, hash := range hashes {
			h.InsertWithHash(hash)
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { h.InsertWithHash(hashes[0]) }, samples)
		results = append(results, octoResult{"HLL", "Standalone", "Insert", mops, p50, p99})
	}

	{
		h := hll.NewHyperLogLog()
		for _, hash := range hashes {
			h.InsertWithHash(hash)
		}
		start := time.Now()
		for range n {
			_ = h.EstimateCardinality()
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { _ = h.EstimateCardinality() }, 1_000)
		results = append(results, octoResult{"HLL", "Standalone", "Query", mops, p50, p99})
	}

	{
		agg, workers, inputChans, deltaCh, tauCtrl, err :=
			octosketch.NewHLLSystemAdaptive(
				octosketch.DefaultTauConfig(octoTau, octoDeltaBuf), numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		mops := octoThroughputMops(t, agg, workers, inputChans, deltaCh, tauCtrl, inputs)
		results = append(results, octoResult{"HLL", fmt.Sprintf("OctoSketch(%dw)", numW), "Insert", mops, 0, 0})
	}

	{
		probe := inputs[0]
		agg, workers, inputChans, deltaCh, err :=
			octosketch.NewHLLSystem(numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		agg.Run()
		for _, w := range workers {
			w.Run()
		}
		drainOctoFixed(t, agg, workers, inputChans, deltaCh, inputs)

		start := time.Now()
		for range n {
			_ = agg.Query(probe)
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { _ = agg.Query(probe) }, 1_000)
		results = append(results, octoResult{"HLL", fmt.Sprintf("OctoSketch(%dw)", numW), "Query", mops, p50, p99})
	}

	// ── DDSketch ──────────────────────────────────────────────────────────────

	{
		dd := ddsketch.NewDDSketch(octoDDAlpha)
		start := time.Now()
		for _, v := range ddValues {
			dd.Add(v)
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { dd.Add(ddValues[0]) }, samples)
		results = append(results, octoResult{"DDSketch", "Standalone", "Insert", mops, p50, p99})
	}

	{
		dd := ddsketch.NewDDSketch(octoDDAlpha)
		for _, v := range ddValues {
			dd.Add(v)
		}
		start := time.Now()
		for range n {
			_, _ = dd.GetValueAtQuantile(0.5)
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { _, _ = dd.GetValueAtQuantile(0.5) }, samples)
		results = append(results, octoResult{"DDSketch", "Standalone", "Query(P50)", mops, p50, p99})
	}

	{
		dd := ddsketch.NewDDSketch(octoDDAlpha)
		start := time.Now()
		for _, v := range ddValues {
			dd.Add(v)
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { dd.Add(ddValues[0]) }, samples)
		results = append(results, octoResult{"DDSketch", "Standalone", "Update", mops, p50, p99})
	}

	{
		agg, workers, inputChans, deltaCh, tauCtrl, err :=
			octosketch.NewDDSketchSystemAdaptive(octoDDAlpha,
				octosketch.DefaultTauConfig(octoTau, octoDeltaBuf), numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		mops := octoThroughputMops(t, agg, workers, inputChans, deltaCh, tauCtrl, ddInputs)
		results = append(results, octoResult{"DDSketch", fmt.Sprintf("OctoSketch(%dw)", numW), "Insert", mops, 0, 0})
	}

	{
		probe := common.FromF64(0.5) // median query
		agg, workers, inputChans, deltaCh, err :=
			octosketch.NewDDSketchSystem(octoDDAlpha, octoTau, numW, octoDeltaBuf)
		if err != nil {
			t.Fatal(err)
		}
		agg.Run()
		for _, w := range workers {
			w.Run()
		}
		drainOctoFixed(t, agg, workers, inputChans, deltaCh, ddInputs)

		start := time.Now()
		for range n {
			_ = agg.Query(probe)
		}
		dur := time.Since(start)
		mops := float64(n) / dur.Seconds() / 1e6
		p50, p99 := sampleLatency(func() { _ = agg.Query(probe) }, samples)
		results = append(results, octoResult{"DDSketch", fmt.Sprintf("OctoSketch(%dw)", numW), "Query(P50)", mops, p50, p99})
	}

	// ── Print table ───────────────────────────────────────────────────────────

	t.Logf("\n")
	t.Logf("╔══════════════╦═════════════════════╦══════════════════╦══════════════╦══════════╦═══════════╗")
	t.Logf("║ %-12s ║ %-19s ║ %-16s ║ %12s ║ %8s ║ %9s ║",
		"Sketch", "Mode", "Operation", "Mops/s", "P50 (ns)", "P99 (ns)")
	t.Logf("╠══════════════╬═════════════════════╬══════════════════╬══════════════╬══════════╬═══════════╣")
	for _, r := range results {
		p50s, p99s := "-", "-"
		if r.p50ns > 0 {
			p50s = fmt.Sprintf("%d", r.p50ns)
		}
		if r.p99ns > 0 {
			p99s = fmt.Sprintf("%d", r.p99ns)
		}
		t.Logf("║ %-12s ║ %-19s ║ %-16s ║ %12.2f ║ %8s ║ %9s ║",
			r.sketch, r.mode, r.op, r.mops, p50s, p99s)
	}
	t.Logf("╚══════════════╩═════════════════════╩══════════════════╩══════════════╩══════════╩═══════════╝")
	t.Logf("  Stream: %d items | Workers: %d | τ=%.1f (adaptive) | ΔBuf: %d",
		n, numW, octoTau, octoDeltaBuf)
}
