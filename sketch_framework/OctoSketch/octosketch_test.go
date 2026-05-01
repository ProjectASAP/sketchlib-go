package octosketch_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	octosketch "github.com/ProjectASAP/sketchlib-go/sketch_framework/OctoSketch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRows = 5
	testCols = 2048
	testTau  = 10.0
)

// helpers ─────────────────────────────────────────────────────────────────────

// shutdownSystem closes all input channels, waits for all workers to flush,
// then closes deltaCh so the aggregator can drain and exit.
func shutdownSystem(
	inputChans []chan<- *common.SketchInput,
	workers []*octosketch.Worker,
	deltaCh chan octosketch.DeltaUpdate,
) {
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
}

// newFixedWorker creates a Worker with a fixed (non-adaptive) tau around sketch,
// writing to deltaCh, with no input channel (caller calls Process directly).
func newFixedWorker(t *testing.T, sketch octosketch.CellSketch, tau float64, deltaCh chan octosketch.DeltaUpdate) *octosketch.Worker {
	t.Helper()
	fixedTau := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(tau))
	return octosketch.NewWorker(0, sketch, fixedTau, deltaCh, nil)
}

// drainDeltas collects all deltas from deltaCh into aggSketch.
func drainDeltas(deltaCh <-chan octosketch.DeltaUpdate, aggSketch octosketch.CellSketch) {
	for delta := range deltaCh {
		aggSketch.MergeDelta(delta)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Count-Min Sketch tests
// ─────────────────────────────────────────────────────────────────────────────

func TestCountMinCellSketchCompliance(t *testing.T) {
	sketch, err := octosketch.NewCountMinOcto(testRows, testCols)
	require.NoError(t, err)
	var _ octosketch.CellSketch = sketch
	var _ octosketch.OctoSketch = sketch // OctoUpdate, MergeDelta, Estimate
}

func TestCountMinNoUnderestimate(t *testing.T) {
	const tau = 5.0
	const insertions = 500

	deltaCh := make(chan octosketch.DeltaUpdate, 65536)
	sketch, err := octosketch.NewCountMinOcto(testRows, testCols)
	require.NoError(t, err)
	worker := newFixedWorker(t, sketch, tau, deltaCh)

	aggSketch, err := octosketch.NewCountMinOcto(testRows, testCols)
	require.NoError(t, err)

	target := common.FromString("hot-key")
	for range insertions {
		worker.Process(target)
	}
	worker.Flush()
	close(deltaCh)
	drainDeltas(deltaCh, aggSketch)

	est := aggSketch.Estimate(target)
	assert.GreaterOrEqual(t, est, float64(insertions),
		"CMS must never underestimate: want >= %d, got %f", insertions, est)
}

func TestCountMinDeltaThreshold(t *testing.T) {
	deltaCh := make(chan octosketch.DeltaUpdate, 4096)
	sketch, err := octosketch.NewCountMinOcto(testRows, testCols)
	require.NoError(t, err)
	worker := newFixedWorker(t, sketch, testTau, deltaCh)

	key := common.FromString("threshold-test")
	for range int(testTau) - 1 {
		worker.Process(key)
	}
	assert.Empty(t, deltaCh, "no delta expected before threshold")

	worker.Process(key) // crosses τ
	assert.Len(t, deltaCh, testRows, "expected one delta per row at threshold")

	close(deltaCh)
	for delta := range deltaCh {
		assert.Equal(t, testTau, delta.Value)
	}
}

func TestCountMinFlushResiduals(t *testing.T) {
	const insertions = 7

	deltaCh := make(chan octosketch.DeltaUpdate, 4096)
	sketch, err := octosketch.NewCountMinOcto(testRows, testCols)
	require.NoError(t, err)
	worker := newFixedWorker(t, sketch, 100.0, deltaCh) // high τ: never auto-emits

	aggSketch, err := octosketch.NewCountMinOcto(testRows, testCols)
	require.NoError(t, err)

	key := common.FromString("flush-test")
	for range insertions {
		worker.Process(key)
	}
	assert.Empty(t, deltaCh, "no delta expected before flush")

	worker.Flush()
	close(deltaCh)
	drainDeltas(deltaCh, aggSketch)

	assert.GreaterOrEqual(t, aggSketch.Estimate(key), float64(insertions))
}

func TestCountMinMultiWorker(t *testing.T) {
	const numWorkers, perWorker = 4, 250

	deltaCh := make(chan octosketch.DeltaUpdate, 65536)
	aggSketch, err := octosketch.NewCountMinOcto(testRows, testCols)
	require.NoError(t, err)

	key := common.FromString("shared-key")
	var wg sync.WaitGroup
	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := octosketch.NewCountMinOcto(testRows, testCols)
			require.NoError(t, err)
			w := newFixedWorker(t, s, 5.0, deltaCh)
			for range perWorker {
				w.Process(key)
			}
			w.Flush()
		}()
	}
	wg.Wait()
	close(deltaCh)
	drainDeltas(deltaCh, aggSketch)

	assert.GreaterOrEqual(t, aggSketch.Estimate(key), float64(numWorkers*perWorker))
}

func TestCountMinSystemWiring(t *testing.T) {
	const insertions = 200

	agg, workers, inputChans, deltaCh, err := octosketch.NewCountMinSystem(
		testRows, testCols, 8.0, 3, 8192,
	)
	require.NoError(t, err)
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	key := common.FromString("wiring-test")
	for i := range insertions {
		inputChans[i%len(inputChans)] <- key
	}
	shutdownSystem(inputChans, workers, deltaCh)
	<-agg.Done()

	assert.GreaterOrEqual(t, agg.Query(key), float64(insertions))
}

func TestCountMinAdaptiveSystem(t *testing.T) {
	const insertions = 500

	agg, workers, inputChans, deltaCh, tauCtrl, err := octosketch.NewCountMinSystemAdaptive(
		testRows, testCols,
		octosketch.DefaultTauConfig(5.0, 8192),
		3, 8192,
	)
	require.NoError(t, err)

	agg.Run()
	for _, w := range workers {
		w.Run()
	}
	tauCtrl.Run()

	key := common.FromString("adaptive-test")
	for i := range insertions {
		inputChans[i%len(inputChans)] <- key
	}
	tauCtrl.Stop()
	shutdownSystem(inputChans, workers, deltaCh)
	<-agg.Done()

	assert.GreaterOrEqual(t, agg.Query(key), float64(insertions))
}

func TestCountMinConstructorValidation(t *testing.T) {
	_, err := octosketch.NewCountMinOcto(0, testCols)
	assert.Error(t, err)
	_, err = octosketch.NewCountMinOcto(testRows, 0)
	assert.Error(t, err)
}

// ─────────────────────────────────────────────────────────────────────────────
// Count Sketch tests
// ─────────────────────────────────────────────────────────────────────────────

func TestCountSketchCellSketchCompliance(t *testing.T) {
	sketch, err := octosketch.NewCountSketchOcto(testRows, testCols)
	require.NoError(t, err)
	var _ octosketch.CellSketch = sketch
	var _ octosketch.OctoSketch = sketch
}

func TestCountSketchSignedDelta(t *testing.T) {
	const tau = 5.0
	deltaCh := make(chan octosketch.DeltaUpdate, 4096)
	sketch, err := octosketch.NewCountSketchOcto(testRows, testCols)
	require.NoError(t, err)
	worker := newFixedWorker(t, sketch, tau, deltaCh)

	key := common.FromString("signed-delta-key")
	for range int(tau) * 2 {
		worker.Process(key)
	}
	worker.Flush()
	close(deltaCh)

	for delta := range deltaCh {
		abs := delta.Value
		if abs < 0 {
			abs = -abs
		}
		assert.LessOrEqual(t, abs, tau,
			"|delta.Value| must not exceed tau after reset")
	}
}

func TestCountSketchThreshold(t *testing.T) {
	const tau = 6.0
	deltaCh := make(chan octosketch.DeltaUpdate, 4096)
	sketch, err := octosketch.NewCountSketchOcto(testRows, testCols)
	require.NoError(t, err)
	worker := newFixedWorker(t, sketch, tau, deltaCh)

	key := common.FromString("cs-threshold-test")
	triggered := false
	for range int(tau) * 3 {
		worker.Process(key)
		if len(deltaCh) > 0 {
			triggered = true
			break
		}
	}
	assert.True(t, triggered, "expected at least one delta by tau*3 insertions")
}

func TestCountSketchAggregatorSignedAdd(t *testing.T) {
	aggSketch, err := octosketch.NewCountSketchOcto(testRows, testCols)
	require.NoError(t, err)
	aggSketch.MergeDelta(octosketch.DeltaUpdate{Row: 0, Col: 0, Value: 10.0})
	aggSketch.MergeDelta(octosketch.DeltaUpdate{Row: 0, Col: 0, Value: -4.0})
	_ = aggSketch.Estimate(common.FromString("any-key"))
}

func TestCountSketchFlushResiduals(t *testing.T) {
	const insertions = 7
	deltaCh := make(chan octosketch.DeltaUpdate, 4096)
	sketch, err := octosketch.NewCountSketchOcto(testRows, testCols)
	require.NoError(t, err)
	worker := newFixedWorker(t, sketch, 100.0, deltaCh)
	aggSketch, err := octosketch.NewCountSketchOcto(testRows, testCols)
	require.NoError(t, err)

	key := common.FromString("cs-flush-test")
	for range insertions {
		worker.Process(key)
	}
	assert.Empty(t, deltaCh)

	worker.Flush()
	close(deltaCh)
	drainDeltas(deltaCh, aggSketch)

	est := aggSketch.Estimate(key)
	assert.InDelta(t, float64(insertions), est, float64(insertions)*0.5)
}

func TestCountSketchMultiWorker(t *testing.T) {
	const numWorkers, perWorker = 4, 200
	deltaCh := make(chan octosketch.DeltaUpdate, 65536)
	aggSketch, err := octosketch.NewCountSketchOcto(testRows, testCols)
	require.NoError(t, err)

	key := common.FromString("cs-multi-key")
	var wg sync.WaitGroup
	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := octosketch.NewCountSketchOcto(testRows, testCols)
			require.NoError(t, err)
			w := newFixedWorker(t, s, 5.0, deltaCh)
			for range perWorker {
				w.Process(key)
			}
			w.Flush()
		}()
	}
	wg.Wait()
	close(deltaCh)
	drainDeltas(deltaCh, aggSketch)

	total := float64(numWorkers * perWorker)
	est := aggSketch.Estimate(key)
	assert.InDelta(t, total, est, total*0.3)
}

func TestCountSketchSystemWiring(t *testing.T) {
	const insertions = 150
	agg, workers, inputChans, deltaCh, err := octosketch.NewCountSketchSystem(
		testRows, testCols, 8.0, 3, 8192,
	)
	require.NoError(t, err)
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	key := common.FromString("cs-wiring-test")
	for i := range insertions {
		inputChans[i%len(inputChans)] <- key
	}
	shutdownSystem(inputChans, workers, deltaCh)
	<-agg.Done()

	est := agg.Query(key)
	assert.InDelta(t, float64(insertions), est, float64(insertions)*0.3)
}

func TestCountSketchConstructorValidation(t *testing.T) {
	_, err := octosketch.NewCountSketchOcto(0, testCols)
	assert.Error(t, err, "zero rows")
	_, err = octosketch.NewCountSketchOcto(testRows, 0)
	assert.Error(t, err, "zero cols")
	_, err = octosketch.NewCountSketchOcto(testRows, 3) // non-power-of-two
	assert.Error(t, err, "non-power-of-two")
}

// ─────────────────────────────────────────────────────────────────────────────
// HyperLogLog tests
// ─────────────────────────────────────────────────────────────────────────────

func TestHLLCellSketchCompliance(t *testing.T) {
	sketch := octosketch.NewHLLOcto()
	var _ octosketch.CellSketch = sketch
	var _ octosketch.OctoSketch = sketch
}

func TestHLLRegisterMaxMerge(t *testing.T) {
	agg := octosketch.NewHLLOcto()
	agg.MergeDelta(octosketch.DeltaUpdate{Row: 0, Col: 42, Value: 3.0})
	agg.MergeDelta(octosketch.DeltaUpdate{Row: 0, Col: 42, Value: 7.0})
	agg.MergeDelta(octosketch.DeltaUpdate{Row: 0, Col: 42, Value: 5.0}) // ignored: lower
	est := float64(agg.Estimate())
	assert.Greater(t, est, float64(0))
}

func TestHLLEmitOnIncrease(t *testing.T) {
	deltaCh := make(chan octosketch.DeltaUpdate, 4096)
	sketch := octosketch.NewHLLOcto()
	worker := newFixedWorker(t, sketch, 1.0, deltaCh)

	key := common.FromString("hll-emit-test")
	worker.Process(key)
	assert.Equal(t, 1, len(deltaCh), "first insert must emit exactly one delta")

	prev := len(deltaCh)
	worker.Process(key) // same hash → same lz → no change
	assert.Equal(t, prev, len(deltaCh), "re-inserting same key must not emit another delta")
}

func TestHLLCardinalityEstimate(t *testing.T) {
	const cardinality = 10_000
	deltaCh := make(chan octosketch.DeltaUpdate, 65536)
	sketch := octosketch.NewHLLOcto()
	worker := newFixedWorker(t, sketch, 1.0, deltaCh)
	agg := octosketch.NewHLLOcto()

	for i := range cardinality {
		worker.Process(common.FromString(fmt.Sprintf("item-%d", i)))
	}
	worker.Flush()
	close(deltaCh)
	drainDeltas(deltaCh, agg)

	est := float64(agg.Estimate())
	assert.InDelta(t, float64(cardinality), est, float64(cardinality)*0.05)
}

func TestHLLFlushSyncsAggregator(t *testing.T) {
	const cardinality = 1000
	deltaCh := make(chan octosketch.DeltaUpdate, 65536)
	sketch := octosketch.NewHLLOcto()
	worker := newFixedWorker(t, sketch, 1.0, deltaCh)

	for i := range cardinality {
		worker.Process(common.FromString(fmt.Sprintf("sync-%d", i)))
	}
	// Drain real-time deltas (simulate late-joining aggregator)
	for len(deltaCh) > 0 {
		<-deltaCh
	}

	worker.Flush()
	close(deltaCh)

	lateAgg := octosketch.NewHLLOcto()
	drainDeltas(deltaCh, lateAgg)

	assert.InDelta(t, float64(cardinality), float64(lateAgg.Estimate()), float64(cardinality)*0.05)
}

func TestHLLMultiWorkerCardinality(t *testing.T) {
	const numWorkers, perWorker = 4, 2000
	deltaCh := make(chan octosketch.DeltaUpdate, 65536)
	agg := octosketch.NewHLLOcto()

	var wg sync.WaitGroup
	for workerID := range numWorkers {
		wg.Add(1)
		workerID := workerID
		go func() {
			defer wg.Done()
			s := octosketch.NewHLLOcto()
			w := newFixedWorker(t, s, 1.0, deltaCh)
			for i := range perWorker {
				w.Process(common.FromString(fmt.Sprintf("w%d-%d", workerID, i)))
			}
			w.Flush()
		}()
	}
	wg.Wait()
	close(deltaCh)
	drainDeltas(deltaCh, agg)

	total := float64(numWorkers * perWorker)
	assert.InDelta(t, total, float64(agg.Estimate()), total*0.05)
}

func TestHLLSystemWiring(t *testing.T) {
	const numWorkers, perWorker = 3, 1000
	agg, workers, inputChans, deltaCh, err := octosketch.NewHLLSystem(numWorkers, 32768)
	require.NoError(t, err)
	agg.Run()
	for _, w := range workers {
		w.Run()
	}

	for workerIdx := range numWorkers {
		for i := range perWorker {
			inputChans[workerIdx] <- common.FromString(fmt.Sprintf("sys-w%d-%d", workerIdx, i))
		}
	}
	shutdownSystem(inputChans, workers, deltaCh)
	<-agg.Done()

	total := float64(numWorkers * perWorker)
	assert.InDelta(t, total, agg.Query(nil), total*0.05)
}

// ─────────────────────────────────────────────────────────────────────────────
// AdaptiveTau tests
// ─────────────────────────────────────────────────────────────────────────────

func TestAdaptiveTauIncreaseOnHighQueue(t *testing.T) {
	cfg := octosketch.TauConfig{
		Initial: 10, Min: 1, Max: 100, Step: 1,
		TargetQueue: 5, Deadband: 0,
	}
	tau := octosketch.NewAdaptiveTau(cfg)
	tau.Adjust(10) // Qhat = 10 + (10-10) = 10 > Q
	assert.Equal(t, 11.0, tau.Current())
}

func TestAdaptiveTauDecreaseOnLowQueue(t *testing.T) {
	cfg := octosketch.TauConfig{
		Initial: 10, Min: 1, Max: 100, Step: 1,
		TargetQueue: 5, Deadband: 0,
	}
	tau := octosketch.NewAdaptiveTau(cfg)
	tau.Adjust(0) // Qhat = 0 + (0-0) = 0 < Q
	assert.Equal(t, 9.0, tau.Current())
}

func TestAdaptiveTauClamping(t *testing.T) {
	cfg := octosketch.TauConfig{
		Initial: 1, Min: 1, Max: 5, Step: 10,
		TargetQueue: 1, Deadband: 0,
	}
	tau := octosketch.NewAdaptiveTau(cfg)
	tau.Adjust(99) // very large predicted queue
	assert.Equal(t, 5.0, tau.Current(), "tau must be clamped to Max")
}

func TestAdaptiveTauTrendPrediction(t *testing.T) {
	cfg := octosketch.TauConfig{
		Initial: 10, Min: 1, Max: 100, Step: 1,
		TargetQueue: 10, Deadband: 0,
	}
	tau := octosketch.NewAdaptiveTau(cfg)
	tau.Adjust(8) // first sample, Qhat = 8 -> decrease
	assert.Equal(t, 9.0, tau.Current())
	tau.Adjust(9) // Qhat = 9 + (9-8) = 10 -> hold
	assert.Equal(t, 9.0, tau.Current())
	tau.Adjust(10) // Qhat = 10 + (10-9) = 11 -> increase
	assert.Equal(t, 10.0, tau.Current())
}

func TestFixedTauNeverChanges(t *testing.T) {
	tau := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(42.0))
	tau.Adjust(0)
	tau.Adjust(99999)
	assert.Equal(t, 42.0, tau.Current())
}

// ─────────────────────────────────────────────────────────────────────────────
// Shared / cross-sketch tests
// ─────────────────────────────────────────────────────────────────────────────

func TestNilInputSafety(t *testing.T) {
	deltaCh := make(chan octosketch.DeltaUpdate, 8)
	fixedTau := octosketch.NewAdaptiveTau(octosketch.FixedTauConfig(testTau))

	cm, _ := octosketch.NewCountMinOcto(testRows, testCols)
	wCM := octosketch.NewWorker(0, cm, fixedTau, deltaCh, nil)
	wCM.Process(nil) // must not panic

	cs, _ := octosketch.NewCountSketchOcto(testRows, testCols)
	wCS := octosketch.NewWorker(1, cs, fixedTau, deltaCh, nil)
	wCS.Process(nil)

	hll := octosketch.NewHLLOcto()
	wHLL := octosketch.NewWorker(2, hll, fixedTau, deltaCh, nil)
	wHLL.Process(nil)

	// Aggregator nil-input estimates
	cmAgg, _ := octosketch.NewCountMinOcto(testRows, testCols)
	csAgg, _ := octosketch.NewCountSketchOcto(testRows, testCols)
	assert.Equal(t, float64(0), cmAgg.Estimate(nil))
	assert.Equal(t, float64(0), csAgg.Estimate(nil))
	_ = octosketch.NewHLLOcto().Estimate()
}

func TestBoundsCheckAllSketches(t *testing.T) {
	cmAgg, _ := octosketch.NewCountMinOcto(testRows, testCols)
	csAgg, _ := octosketch.NewCountSketchOcto(testRows, testCols)
	hllAgg := octosketch.NewHLLOcto()

	bad := []octosketch.DeltaUpdate{
		{Row: -1, Col: 0, Value: 99},
		{Row: 0, Col: -1, Value: 99},
		{Row: testRows + 10, Col: 0, Value: 99},
		{Row: 0, Col: testCols + 10, Value: 99},
	}
	for _, d := range bad {
		cmAgg.MergeDelta(d)
		csAgg.MergeDelta(d)
	}
	hllAgg.MergeDelta(octosketch.DeltaUpdate{Row: 0, Col: -1, Value: 3})
	hllAgg.MergeDelta(octosketch.DeltaUpdate{Row: 0, Col: 99999, Value: 3})
}
