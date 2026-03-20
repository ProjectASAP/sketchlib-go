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
//	Section 4 — Accuracy Report       (Standalone vs OctoSketch estimate quality after full drain)
//
// OctoSketch pipeline shutdown sequence (from aggregator.go §5):
//  1. Close each worker input channel.
//  2. Wait for all Worker.Done().
//  3. Stop the TauController (if adaptive).
//  4. Close the delta channel.
//  5. Wait for Aggregator.Done().

import (
	"fmt"
	"math"
	"sort"
	"sync"
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
	octoTau      = 16.0 // coarser fixed emission threshold for non-adaptive benchmarks
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

func benchmarkTauConfig(initial float64, deltaBufSize int) octosketch.TauConfig {
	step := initial / 4
	if step < 1 {
		step = 1
	}
	minTau := initial / 2
	if minTau < 1 {
		minTau = 1
	}
	return octosketch.TauConfig{
		Initial:    initial,
		Min:        minTau,
		Max:        initial * 8,
		Step:       step,
		UpperBound: deltaBufSize * 6 / 10,
		LowerBound: deltaBufSize * 1 / 10,
		Interval:   50 * time.Millisecond,
	}
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
var octoCompareWorkers = []int{2, 4, 8}

func benchmarkOctoInsertPipeline(
	b *testing.B,
	inputs []*common.SketchInput,
	numW int,
	newSystem func(int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error),
) {
	n := len(inputs)
	agg, workers, inputChans, deltaCh, err := newSystem(numW)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < benchMinInt(numW, n); i++ {
		workers[i].Process(inputs[i])
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		inputChans[i%numW] <- inputs[i%n]
	}
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

func benchmarkOctoQueryAggregator(
	b *testing.B,
	inputs []*common.SketchInput,
	probe *common.SketchInput,
	numW int,
	newSystem func(int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error),
) {
	agg, workers, inputChans, deltaCh, err := newSystem(numW)
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

func benchmarkOctoUpdatePipeline(
	b *testing.B,
	inputs []*common.SketchInput,
	numW int,
	newSystem func(int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error),
) {
	n := len(inputs)
	agg, workers, inputChans, deltaCh, err := newSystem(numW)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < benchMinInt(numW, n); i++ {
		workers[i].Process(inputs[i])
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	b.SetBytes(8)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		inputChans[i%numW] <- inputs[i%n]
	}
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

func benchmarkOctoMergePipeline(
	b *testing.B,
	deltas []octosketch.DeltaUpdate,
	numProducers int,
	newAgg func(<-chan octosketch.DeltaUpdate) *octosketch.Aggregator,
) {
	deltaCh := make(chan octosketch.DeltaUpdate, octoDeltaBuf)
	agg := newAgg(deltaCh)
	agg.Run()

	startGate := make(chan struct{})
	var wg sync.WaitGroup
	for workerID := 0; workerID < numProducers; workerID++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startGate
			for i := workerID; i < b.N; i += numProducers {
				deltaCh <- deltas[i%len(deltas)]
			}
		}(workerID)
	}
	b.ResetTimer()
	b.ReportAllocs()
	close(startGate)
	wg.Wait()
	b.StopTimer()

	close(deltaCh)
	<-agg.Done()
}

func BenchmarkOcto_Insert_CountMin(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	inputs := loadCAIDAInputs(b)
	b.Run("Standalone", func(b *testing.B) {
		cms, _ := countminsketch.NewCountMinSketch(octoCMRows, octoCMCols)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cms.InsertWithHash(hashes[i%n])
		}
	})
	for _, numW := range octoCompareWorkers {
		b.Run(fmt.Sprintf("OctoSketch_%dWorkers", numW), func(b *testing.B) {
			benchmarkOctoInsertPipeline(b, inputs, numW, func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error) {
				return octosketch.NewCountMinSystem(octoCMRows, octoCMCols, octoTau, numW, octoDeltaBuf)
			})
		})
	}
}

func BenchmarkOcto_Insert_CountSketch(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	inputs := loadCAIDAInputs(b)
	b.Run("Standalone", func(b *testing.B) {
		cs, _ := countsketch.NewCountSketch(octoCSRows, octoCSCols)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cs.InsertWithHash(hashes[i%n])
		}
	})
	for _, numW := range octoCompareWorkers {
		b.Run(fmt.Sprintf("OctoSketch_%dWorkers", numW), func(b *testing.B) {
			benchmarkOctoInsertPipeline(b, inputs, numW, func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error) {
				return octosketch.NewCountSketchSystem(octoCSRows, octoCSCols, octoTau, numW, octoDeltaBuf)
			})
		})
	}
}

func BenchmarkOcto_Insert_HLL(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	inputs := loadCAIDAInputs(b)
	b.Run("Standalone", func(b *testing.B) {
		h := hll.NewHyperLogLog()
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			h.InsertWithHash(hashes[i%n])
		}
	})
	for _, numW := range octoCompareWorkers {
		b.Run(fmt.Sprintf("OctoSketch_%dWorkers", numW), func(b *testing.B) {
			benchmarkOctoInsertPipeline(b, inputs, numW, func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error) {
				return octosketch.NewHLLSystem(numW, octoDeltaBuf)
			})
		})
	}
}

func BenchmarkOcto_Insert_DDSketch(b *testing.B) {
	inputs, values := loadCAIDAF64Inputs(b)
	n := len(values)
	b.Run("Standalone", func(b *testing.B) {
		dd := ddsketch.NewDDSketch(octoDDAlpha)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			dd.Add(values[i%n])
		}
	})
	for _, numW := range octoCompareWorkers {
		b.Run(fmt.Sprintf("OctoSketch_%dWorkers", numW), func(b *testing.B) {
			benchmarkOctoInsertPipeline(b, inputs, numW, func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error) {
				return octosketch.NewDDSketchSystem(octoDDAlpha, octoTau, numW, octoDeltaBuf)
			})
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Section 2 — Query Throughput
// Sketches are pre-filled before the timer starts.
// ─────────────────────────────────────────────────────────────────────────────

func BenchmarkOcto_Query_CountMin(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	inputs := loadCAIDAInputs(b)
	b.Run("Standalone", func(b *testing.B) {
		cms, _ := countminsketch.NewCountMinSketch(octoCMRows, octoCMCols)
		for _, h := range hashes {
			cms.InsertWithHash(h)
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = cms.QueryWithHash(common.QueryFrequency, hashes[i%n])
		}
	})
	for _, numW := range octoCompareWorkers {
		b.Run(fmt.Sprintf("OctoSketch_%dWorkers", numW), func(b *testing.B) {
			benchmarkOctoQueryAggregator(b, inputs, inputs[0], numW, func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error) {
				return octosketch.NewCountMinSystem(octoCMRows, octoCMCols, octoTau, numW, octoDeltaBuf)
			})
		})
	}
}

func BenchmarkOcto_Query_CountSketch(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	inputs := loadCAIDAInputs(b)
	b.Run("Standalone", func(b *testing.B) {
		cs, _ := countsketch.NewCountSketch(octoCSRows, octoCSCols)
		for _, h := range hashes {
			cs.InsertWithHash(h)
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = cs.QueryWithHash(common.QueryFrequency, hashes[i%n])
		}
	})
	for _, numW := range octoCompareWorkers {
		b.Run(fmt.Sprintf("OctoSketch_%dWorkers", numW), func(b *testing.B) {
			benchmarkOctoQueryAggregator(b, inputs, inputs[0], numW, func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error) {
				return octosketch.NewCountSketchSystem(octoCSRows, octoCSCols, octoTau, numW, octoDeltaBuf)
			})
		})
	}
}

func BenchmarkOcto_Query_HLL(b *testing.B) {
	hashes, _ := LoadCAIDAHelper(b)
	inputs := loadCAIDAInputs(b)
	probe := inputs[0]
	b.Run("Standalone", func(b *testing.B) {
		h := hll.NewHyperLogLog()
		for _, hash := range hashes {
			h.InsertWithHash(hash)
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = h.EstimateCardinality()
		}
	})
	for _, numW := range octoCompareWorkers {
		b.Run(fmt.Sprintf("OctoSketch_%dWorkers", numW), func(b *testing.B) {
			benchmarkOctoQueryAggregator(b, inputs, probe, numW, func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error) {
				return octosketch.NewHLLSystem(numW, octoDeltaBuf)
			})
		})
	}
}

func BenchmarkOcto_Query_DDSketch(b *testing.B) {
	_, values := loadCAIDAF64Inputs(b)
	inputs, _ := loadCAIDAF64Inputs(b)
	probe := common.FromF64(0.5)
	b.Run("Standalone", func(b *testing.B) {
		dd := ddsketch.NewDDSketch(octoDDAlpha)
		for _, v := range values {
			dd.Add(v)
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = dd.GetValueAtQuantile(0.5)
		}
	})
	for _, numW := range octoCompareWorkers {
		b.Run(fmt.Sprintf("OctoSketch_%dWorkers", numW), func(b *testing.B) {
			benchmarkOctoQueryAggregator(b, inputs, probe, numW, func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error) {
				return octosketch.NewDDSketchSystem(octoDDAlpha, octoTau, numW, octoDeltaBuf)
			})
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Section 3 — Update (Weighted Insert) Throughput
// ─────────────────────────────────────────────────────────────────────────────

func BenchmarkOcto_Update_CountMin(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	inputs := loadCAIDAInputs(b)
	b.Run("Standalone_Weighted", func(b *testing.B) {
		cms, _ := countminsketch.NewCountMinSketch(octoCMRows, octoCMCols)
		b.SetBytes(8)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cms.FastInsertManyWithHashValue(hashes[i%n], float64(i%100+1))
		}
	})
	for _, numW := range octoCompareWorkers {
		b.Run(fmt.Sprintf("OctoSketch_%dWorkers", numW), func(b *testing.B) {
			benchmarkOctoUpdatePipeline(b, inputs, numW, func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error) {
				return octosketch.NewCountMinSystem(octoCMRows, octoCMCols, octoTau, numW, octoDeltaBuf)
			})
		})
	}
}

func BenchmarkOcto_Update_CountSketch(b *testing.B) {
	hashes, n := LoadCAIDAHelper(b)
	inputs := loadCAIDAInputs(b)
	b.Run("Standalone_Weighted", func(b *testing.B) {
		cs, _ := countsketch.NewCountSketch(octoCSRows, octoCSCols)
		b.SetBytes(8)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cs.InsertWithHashAndValue(hashes[i%n], float64(i%100+1))
		}
	})
	for _, numW := range octoCompareWorkers {
		b.Run(fmt.Sprintf("OctoSketch_%dWorkers", numW), func(b *testing.B) {
			benchmarkOctoUpdatePipeline(b, inputs, numW, func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error) {
				return octosketch.NewCountSketchSystem(octoCSRows, octoCSCols, octoTau, numW, octoDeltaBuf)
			})
		})
	}
}

func BenchmarkOcto_Update_DDSketch(b *testing.B) {
	inputs, values := loadCAIDAF64Inputs(b)
	n := len(values)
	b.Run("Standalone", func(b *testing.B) {
		dd := ddsketch.NewDDSketch(octoDDAlpha)
		b.SetBytes(8)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			dd.Add(values[i%n])
		}
	})
	for _, numW := range octoCompareWorkers {
		b.Run(fmt.Sprintf("OctoSketch_%dWorkers", numW), func(b *testing.B) {
			benchmarkOctoUpdatePipeline(b, inputs, numW, func(numW int) (*octosketch.Aggregator, []*octosketch.Worker, []chan<- *common.SketchInput, chan octosketch.DeltaUpdate, error) {
				return octosketch.NewDDSketchSystem(octoDDAlpha, octoTau, numW, octoDeltaBuf)
			})
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Section 4 — Accuracy Report
//
// Each test builds a standalone sketch and an OctoSketch pipeline that both
// consume the full CAIDA stream, then compares their estimates against exact
// ground truth.  Both paths see identical inputs; the only structural
// difference is the τ-buffering in OctoSketch workers.
//
// Metrics reported per test:
//   Standalone error  — relative error of the baseline sketch vs ground truth
//   OctoSketch error  — relative error of the aggregator vs ground truth
//   Divergence        — |octo_est − baseline_est| / max(baseline_est, 1)
//
// After a full pipeline drain (all workers flushed, aggregator drained) the
// OctoSketch convergence guarantee states the aggregator is equivalent to a
// single-machine sketch.  Divergence should therefore be at or near zero.
// ─────────────────────────────────────────────────────────────────────────────

const octoAccuracyWorkers = 4

// octoRelErr returns |est − truth| / max(truth, 1).
func octoRelErr(est, truth float64) float64 {
	d := est - truth
	if d < 0 {
		d = -d
	}
	denom := truth
	if denom < 1 {
		denom = 1
	}
	return d / denom
}

// octoFreqEntry pairs a representative *SketchInput with its exact stream count.
type octoFreqEntry struct {
	inp   *common.SketchInput
	count int
}

// octoHeavyHitters builds a frequency map from inputs and returns the top-n
// entries sorted by descending count.
func octoHeavyHitters(inputs []*common.SketchInput, n int) []*octoFreqEntry {
	m := make(map[uint64]*octoFreqEntry, len(inputs))
	for _, inp := range inputs {
		if e, ok := m[inp.Hash]; ok {
			e.count++
		} else {
			m[inp.Hash] = &octoFreqEntry{inp: inp, count: 1}
		}
	}
	all := make([]*octoFreqEntry, 0, len(m))
	for _, e := range m {
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].count > all[j].count })
	if n > len(all) {
		n = len(all)
	}
	return all[:n]
}

// ─── CountMin ────────────────────────────────────────────────────────────────

// TestOctoAccuracy_CountMin compares frequency estimates from the standalone
// CountMinSketch against the OctoSketch aggregator over the top-200 heavy
// hitters in the CAIDA stream.
func TestOctoAccuracy_CountMin(t *testing.T) {
	inputs := loadCAIDAInputs(t)

	probes := octoHeavyHitters(inputs, 200)

	// Standalone baseline: insert every item directly.
	baseline, _ := countminsketch.NewCountMinSketch(octoCMRows, octoCMCols)
	for _, inp := range inputs {
		baseline.Insert(inp)
	}

	// OctoSketch pipeline: drain all items then query the aggregator.
	agg, workers, inputChans, deltaCh, err := octosketch.NewCountMinSystem(
		octoCMRows, octoCMCols, octoTau, octoAccuracyWorkers, octoDeltaBuf)
	if err != nil {
		t.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}
	drainOctoFixed(t, agg, workers, inputChans, deltaCh, inputs)

	// Compute ARE / max-RE over the probe set.
	var baseARE, octoARE, divARE float64
	var baseMaxRE, octoMaxRE, divMaxRE float64
	for _, e := range probes {
		truth := float64(e.count)
		bEst := baseline.Estimate(e.inp)
		oEst := agg.Query(e.inp)

		bRE := octoRelErr(bEst, truth)
		oRE := octoRelErr(oEst, truth)
		dRE := octoRelErr(oEst, bEst)

		baseARE += bRE
		octoARE += oRE
		divARE += dRE
		if bRE > baseMaxRE {
			baseMaxRE = bRE
		}
		if oRE > octoMaxRE {
			octoMaxRE = oRE
		}
		if dRE > divMaxRE {
			divMaxRE = dRE
		}
	}
	n := float64(len(probes))
	baseARE /= n
	octoARE /= n
	divARE /= n

	t.Log("══════════════════════════════════════════════════════════")
	t.Logf(" OctoSketch Accuracy — CountMin  (%d workers, τ=%.0f)", octoAccuracyWorkers, octoTau)
	t.Logf(" Stream: %d items  |  Probes: top-%d heavy hitters", len(inputs), len(probes))
	t.Logf(" Rows=%-2d  Cols=%-6d", octoCMRows, octoCMCols)
	t.Log("──────────────────────────────────────────────────────────")
	t.Logf(" Standalone  ARE: %6.3f%%   max-RE: %6.3f%%", baseARE*100, baseMaxRE*100)
	t.Logf(" OctoSketch  ARE: %6.3f%%   max-RE: %6.3f%%", octoARE*100, octoMaxRE*100)
	t.Logf(" Divergence  ARE: %6.3f%%   max-RE: %6.3f%%", divARE*100, divMaxRE*100)
	t.Log("══════════════════════════════════════════════════════════")

	// After a full drain divergence must not exceed 2× the standalone sketch's
	// own error, plus a small absolute tolerance for very low-count keys.
	limit := 2*baseARE + 0.01
	if divARE > limit {
		t.Errorf("convergence failure: divergence ARE %.4f%% > 2×standalone ARE %.4f%%",
			divARE*100, baseARE*100)
	}
}

// ─── CountSketch ─────────────────────────────────────────────────────────────

// TestOctoAccuracy_CountSketch mirrors TestOctoAccuracy_CountMin for the signed
// CountSketch estimator.
func TestOctoAccuracy_CountSketch(t *testing.T) {
	inputs := loadCAIDAInputs(t)

	probes := octoHeavyHitters(inputs, 200)

	baseline, _ := countsketch.NewCountSketch(octoCSRows, octoCSCols)
	for _, inp := range inputs {
		baseline.Insert(inp)
	}

	agg, workers, inputChans, deltaCh, err := octosketch.NewCountSketchSystem(
		octoCSRows, octoCSCols, octoTau, octoAccuracyWorkers, octoDeltaBuf)
	if err != nil {
		t.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}
	drainOctoFixed(t, agg, workers, inputChans, deltaCh, inputs)

	var baseARE, octoARE, divARE float64
	var baseMaxRE, octoMaxRE, divMaxRE float64
	for _, e := range probes {
		truth := float64(e.count)
		bEst := math.Abs(baseline.Estimate(e.inp))
		oEst := math.Abs(agg.Query(e.inp))

		bRE := octoRelErr(bEst, truth)
		oRE := octoRelErr(oEst, truth)
		dRE := octoRelErr(oEst, bEst)

		baseARE += bRE
		octoARE += oRE
		divARE += dRE
		if bRE > baseMaxRE {
			baseMaxRE = bRE
		}
		if oRE > octoMaxRE {
			octoMaxRE = oRE
		}
		if dRE > divMaxRE {
			divMaxRE = dRE
		}
	}
	n := float64(len(probes))
	baseARE /= n
	octoARE /= n
	divARE /= n

	t.Log("══════════════════════════════════════════════════════════")
	t.Logf(" OctoSketch Accuracy — CountSketch  (%d workers, τ=%.0f)", octoAccuracyWorkers, octoTau)
	t.Logf(" Stream: %d items  |  Probes: top-%d heavy hitters", len(inputs), len(probes))
	t.Logf(" Rows=%-2d  Cols=%-6d", octoCSRows, octoCSCols)
	t.Log("──────────────────────────────────────────────────────────")
	t.Logf(" Standalone  ARE: %6.3f%%   max-RE: %6.3f%%", baseARE*100, baseMaxRE*100)
	t.Logf(" OctoSketch  ARE: %6.3f%%   max-RE: %6.3f%%", octoARE*100, octoMaxRE*100)
	t.Logf(" Divergence  ARE: %6.3f%%   max-RE: %6.3f%%", divARE*100, divMaxRE*100)
	t.Log("══════════════════════════════════════════════════════════")

	limit := 2*baseARE + 0.01
	if divARE > limit {
		t.Errorf("convergence failure: divergence ARE %.4f%% > 2×standalone ARE %.4f%%",
			divARE*100, baseARE*100)
	}
}

// ─── HLL ─────────────────────────────────────────────────────────────────────

// TestOctoAccuracy_HLL compares the HyperLogLog cardinality estimate from the
// standalone sketch and the OctoSketch aggregator against the exact cardinality
// counted from the CAIDA stream.
func TestOctoAccuracy_HLL(t *testing.T) {
	inputs := loadCAIDAInputs(t)

	// Exact cardinality via a set of unique input hashes.
	unique := make(map[uint64]struct{}, len(inputs))
	for _, inp := range inputs {
		unique[inp.Hash] = struct{}{}
	}
	trueCard := float64(len(unique))

	// Standalone baseline.
	baseline := hll.NewHyperLogLog()
	for _, inp := range inputs {
		baseline.Insert(inp)
	}
	baseEst := float64(baseline.EstimateCardinality())

	// OctoSketch pipeline.
	agg, workers, inputChans, deltaCh, err := octosketch.NewHLLSystem(
		octoAccuracyWorkers, octoDeltaBuf)
	if err != nil {
		t.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}
	drainOctoFixed(t, agg, workers, inputChans, deltaCh, inputs)
	octoEst := agg.Query(inputs[0]) // input ignored by HLL.Estimate

	baseRE := octoRelErr(baseEst, trueCard)
	octoRE := octoRelErr(octoEst, trueCard)
	divRE := octoRelErr(octoEst, baseEst)

	t.Log("══════════════════════════════════════════════════════════")
	t.Logf(" OctoSketch Accuracy — HLL  (%d workers)", octoAccuracyWorkers)
	t.Logf(" Stream: %d items  |  Precision: %d", len(inputs), hll.HLLPrecision)
	t.Log("──────────────────────────────────────────────────────────")
	t.Logf(" True cardinality:    %d", int(trueCard))
	t.Logf(" Standalone estimate: %d   RE: %.4f%%", int(baseEst), baseRE*100)
	t.Logf(" OctoSketch estimate: %d   RE: %.4f%%", int(octoEst), octoRE*100)
	t.Logf(" Divergence (octo vs standalone):   %.4f%%", divRE*100)
	t.Log("══════════════════════════════════════════════════════════")

	// HLL theoretical standard error at precision 14 is ~0.81%.
	// Both sketches must stay within 3σ (~2.5%) and OctoSketch must not add
	// meaningful error beyond the standalone.
	if baseRE > 0.025 {
		t.Errorf("standalone HLL error %.4f%% exceeds 2.5%% threshold", baseRE*100)
	}
	if octoRE > 0.025 {
		t.Errorf("OctoSketch HLL error %.4f%% exceeds 2.5%% threshold", octoRE*100)
	}
	limit := 2*baseRE + 0.005
	if divRE > limit {
		t.Errorf("convergence failure: divergence %.4f%% > 2×standalone error %.4f%%",
			divRE*100, baseRE*100)
	}
}

// ─── DDSketch ─────────────────────────────────────────────────────────────────

// TestOctoAccuracy_DDSketch compares quantile estimates from the standalone
// DDSketch and the OctoSketch aggregator against exact quantiles computed from
// the sorted CAIDA float64 values.
func TestOctoAccuracy_DDSketch(t *testing.T) {
	inputs, values := loadCAIDAF64Inputs(t)

	// Exact quantile lookup from sorted values.
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	exactQuantile := func(q float64) float64 {
		idx := int(math.Ceil(q*float64(len(sorted)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		return sorted[idx]
	}

	// Standalone baseline.
	baseline := ddsketch.NewDDSketch(octoDDAlpha)
	for _, v := range values {
		baseline.Add(v)
	}

	// OctoSketch pipeline.
	agg, workers, inputChans, deltaCh, err := octosketch.NewDDSketchSystem(
		octoDDAlpha, octoTau, octoAccuracyWorkers, octoDeltaBuf)
	if err != nil {
		t.Fatal(err)
	}
	agg.Run()
	for _, w := range workers {
		w.Run()
	}
	drainOctoFixed(t, agg, workers, inputChans, deltaCh, inputs)

	type quantileRow struct {
		q        float64
		exact    float64
		baseEst  float64
		octoEst  float64
		baseRE   float64
		octoRE   float64
		divRE    float64
	}

	quantiles := []float64{0.50, 0.75, 0.90, 0.99}
	rows := make([]quantileRow, len(quantiles))
	for i, q := range quantiles {
		exact := exactQuantile(q)
		bEst, _ := baseline.GetValueAtQuantile(q)
		oEst := agg.Query(common.FromF64(q))

		rows[i] = quantileRow{
			q:       q,
			exact:   exact,
			baseEst: bEst,
			octoEst: oEst,
			baseRE:  octoRelErr(bEst, exact),
			octoRE:  octoRelErr(oEst, exact),
			divRE:   octoRelErr(oEst, bEst),
		}
	}

	t.Log("══════════════════════════════════════════════════════════════════════")
	t.Logf(" OctoSketch Accuracy — DDSketch  (%d workers, τ=%.0f, α=%.2f)",
		octoAccuracyWorkers, octoTau, octoDDAlpha)
	t.Logf(" Stream: %d items", len(inputs))
	t.Log("──────────────────────────────────────────────────────────────────────")
	t.Logf(" %-6s  %-10s  %-10s  %-10s  %-8s  %-8s  %-8s",
		"Pctile", "Exact", "Standalone", "OctoSketch", "Base-RE%", "Octo-RE%", "Div%")
	t.Log("──────────────────────────────────────────────────────────────────────")
	for _, r := range rows {
		t.Logf(" p%-5.0f  %-10.3f  %-10.3f  %-10.3f  %6.3f%%   %6.3f%%   %6.3f%%",
			r.q*100, r.exact, r.baseEst, r.octoEst,
			r.baseRE*100, r.octoRE*100, r.divRE*100)
	}
	t.Log("══════════════════════════════════════════════════════════════════════")

	// DDSketch relative-error guarantee is α=1% per bucket. Both sketches must
	// stay within that bound; divergence must not exceed 2× standalone error.
	for _, r := range rows {
		if r.baseRE > octoDDAlpha*2 {
			t.Errorf("p%.0f standalone RE %.4f%% exceeds 2×α bound",
				r.q*100, r.baseRE*100)
		}
		if r.octoRE > octoDDAlpha*2 {
			t.Errorf("p%.0f OctoSketch RE %.4f%% exceeds 2×α bound",
				r.q*100, r.octoRE*100)
		}
		limit := 2*r.baseRE + 0.01
		if r.divRE > limit {
			t.Errorf("p%.0f convergence failure: divergence %.4f%% > 2×standalone RE %.4f%%",
				r.q*100, r.divRE*100, r.baseRE*100)
		}
	}
}
