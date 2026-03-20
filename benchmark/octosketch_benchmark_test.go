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
//	Section 4 — Comparison Benchmarks
//
// OctoSketch pipeline shutdown sequence (from aggregator.go §5):
//  1. Close each worker input channel.
//  2. Wait for all Worker.Done().
//  3. Stop the TauController (if adaptive).
//  4. Close the delta channel.
//  5. Wait for Aggregator.Done().

import (
	"fmt"
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
