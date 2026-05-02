package octosketch

import (
	"errors"
	"sync"

	"github.com/ProjectASAP/sketchlib-go/common"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
	ddsketch "github.com/ProjectASAP/sketchlib-go/sketches/DDSketch"
	hll "github.com/ProjectASAP/sketchlib-go/sketches/HLL"
)

// Aggregator maintains the global sketch and applies DeltaUpdates from all Workers.
//
// Lifecycle:
//  1. Create with NewAggregator.
//  2. Call Run() to start the goroutine.
//  3. Workers send deltas to the shared delta channel.
//  4. After all Workers are Done(), close the delta channel.
//  5. Wait on Done() for the aggregator goroutine to drain and exit.
//  6. Call Query() for estimates.
type Aggregator struct {
	coreID        int
	sketch        CellSketch
	deltaCh       <-chan DeltaUpdate
	shardSketches []CellSketch
	batchChans    []<-chan *deltaBatch
	recycleChans  []chan<- *deltaBatch
	doneCh        chan struct{}
}

// NewAggregator creates an Aggregator backed by sketch that reads deltas from deltaCh.
func NewAggregator(sketch CellSketch, deltaCh <-chan DeltaUpdate) *Aggregator {
	return &Aggregator{
		coreID:  0,
		sketch:  sketch,
		deltaCh: deltaCh,
		doneCh:  make(chan struct{}),
	}
}

func newShardedAggregator(
	sketch CellSketch,
	shardSketches []CellSketch,
	batchChans []<-chan *deltaBatch,
	recycleChans []chan<- *deltaBatch,
) *Aggregator {
	return &Aggregator{
		coreID:        len(shardSketches),
		sketch:        sketch,
		shardSketches: shardSketches,
		batchChans:    batchChans,
		recycleChans:  recycleChans,
		doneCh:        make(chan struct{}),
	}
}

// Run launches the aggregator goroutine (spec §5).
// Drains deltaCh, applying each delta via sketch.MergeDelta.
// Done() is closed after the channel is fully drained.
func (a *Aggregator) Run() {
	if len(a.batchChans) > 0 {
		go func() {
			defer close(a.doneCh)

			// One goroutine per shard: no reflect.Select, no per-iteration allocs,
			// O(1) channel receive per batch regardless of worker count.
			var wg sync.WaitGroup
			wg.Add(len(a.batchChans))
			for i, batchCh := range a.batchChans {
				shardSketch := a.shardSketches[i]
				recycleCh := a.recycleChans[i]
				go func(batchCh <-chan *deltaBatch, shardSketch CellSketch, recycleCh chan<- *deltaBatch) {
					defer wg.Done()
					for batch := range batchCh {
						for _, delta := range batch.deltas {
							shardSketch.MergeDelta(delta)
						}
						batch.deltas = batch.deltas[:0]
						recycleCh <- batch
					}
				}(batchCh, shardSketch, recycleCh)
			}
			wg.Wait()

			for _, shard := range a.shardSketches {
				if err := mergeCellSketch(a.sketch, shard); err != nil {
					return
				}
			}
		}()
		return
	}

	go func() {
		unlock := lockThreadToCore(a.coreID)
		defer unlock()
		defer close(a.doneCh)
		for delta := range a.deltaCh {
			a.sketch.MergeDelta(delta)
		}
	}()
}

// Done returns a channel closed after the aggregator goroutine exits.
func (a *Aggregator) Done() <-chan struct{} { return a.doneCh }

// Query returns the sketch estimate for input.
// For CMS / CountSketch: frequency estimate.
// For HLL: global cardinality (input is ignored).
// Call only after Done() is closed, or with external synchronization.
func (a *Aggregator) Query(input *common.SketchInput) float64 {
	return a.sketch.OctoEstimate(input)
}

// ─────────────────────────────────────────────────────────────────────────────
// Convenience constructors (fixed τ)
// ─────────────────────────────────────────────────────────────────────────────
//
// Shutdown sequence for all system constructors:
//  1. Close each input channel.
//  2. Wait for all Worker.Done() channels.
//  3. Close the returned deltaCh.
//  4. Wait for Aggregator.Done().

func NewCountMinSystem(
	rows, cols int,
	tau float64,
	numWorkers, deltaBufSize int,
) (*Aggregator, []*Worker, []chan<- *common.SketchInput, chan DeltaUpdate, error) {
	agg, workers, inputChans, deltaCh, _, err := NewCountMinSystemAdaptive(
		rows, cols, FixedTauConfig(tau), numWorkers, deltaBufSize,
	)
	return agg, workers, inputChans, deltaCh, err
}

func NewCountSketchSystem(
	rows, cols int,
	tau float64,
	numWorkers, deltaBufSize int,
) (*Aggregator, []*Worker, []chan<- *common.SketchInput, chan DeltaUpdate, error) {
	agg, workers, inputChans, deltaCh, _, err := NewCountSketchSystemAdaptive(
		rows, cols, FixedTauConfig(tau), numWorkers, deltaBufSize,
	)
	return agg, workers, inputChans, deltaCh, err
}

func NewHLLSystem(
	numWorkers, deltaBufSize int,
) (*Aggregator, []*Worker, []chan<- *common.SketchInput, chan DeltaUpdate, error) {
	agg, workers, inputChans, deltaCh, _, err := NewHLLSystemAdaptive(
		FixedTauConfig(1), numWorkers, deltaBufSize,
	)
	return agg, workers, inputChans, deltaCh, err
}

// ─────────────────────────────────────────────────────────────────────────────
// Adaptive constructors (with TauController)
// ─────────────────────────────────────────────────────────────────────────────
//
// The returned *TauController is NOT started automatically.
// Call tauCtrl.Run() after starting Workers to begin adaptive adjustment.
// Call tauCtrl.Stop() before closing the delta channel.

// NewCountMinSystemAdaptive wires up numWorkers Count-Min Workers and one
// Aggregator with a shared AdaptiveTau and TauController.
func NewCountMinSystemAdaptive(
	rows, cols int,
	tauCfg TauConfig,
	numWorkers, deltaBufSize int,
) (*Aggregator, []*Worker, []chan<- *common.SketchInput, chan DeltaUpdate, *TauController, error) {
	ensureDedicatedThreads(numWorkers + 1)
	sharedTau := NewAdaptiveTau(tauCfg)
	deltaCh := make(chan DeltaUpdate, deltaBufSize)

	aggSketch, err := NewCountMinOcto(rows, cols)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	workers, inputChans, shardSketches, batchChans, recycleChans, err := buildBatchedWorkers(numWorkers, sharedTau, func() (CellSketch, error) {
		return NewCountMinOcto(rows, cols)
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	agg := newShardedAggregator(aggSketch, shardSketches, readonlyBatchChans(batchChans), writeonlyBatchChans(recycleChans))
	tauCtrl := NewTauController(sharedTau, batchQueueDepth(batchChans))

	return agg, workers, inputChans, deltaCh, tauCtrl, nil
}

// NewCountSketchSystemAdaptive wires up numWorkers Count Sketch Workers and one
// Aggregator with a shared AdaptiveTau and TauController.
// cols must be a power of two.
func NewCountSketchSystemAdaptive(
	rows, cols int,
	tauCfg TauConfig,
	numWorkers, deltaBufSize int,
) (*Aggregator, []*Worker, []chan<- *common.SketchInput, chan DeltaUpdate, *TauController, error) {
	ensureDedicatedThreads(numWorkers + 1)
	sharedTau := NewAdaptiveTau(tauCfg)
	deltaCh := make(chan DeltaUpdate, deltaBufSize)

	aggSketch, err := NewCountSketchOcto(rows, cols)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	workers, inputChans, shardSketches, batchChans, recycleChans, err := buildBatchedWorkers(numWorkers, sharedTau, func() (CellSketch, error) {
		return NewCountSketchOcto(rows, cols)
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	agg := newShardedAggregator(aggSketch, shardSketches, readonlyBatchChans(batchChans), writeonlyBatchChans(recycleChans))
	tauCtrl := NewTauController(sharedTau, batchQueueDepth(batchChans))

	return agg, workers, inputChans, deltaCh, tauCtrl, nil
}

// NewHLLSystemAdaptive wires up numWorkers HLL Workers and one Aggregator.
// τ is not meaningful for HLL (ShouldEmit always returns true when a register
// increases), but AdaptiveTau is still wired for API consistency.
func NewHLLSystemAdaptive(
	tauCfg TauConfig,
	numWorkers, deltaBufSize int,
) (*Aggregator, []*Worker, []chan<- *common.SketchInput, chan DeltaUpdate, *TauController, error) {
	ensureDedicatedThreads(numWorkers + 1)
	sharedTau := NewAdaptiveTau(tauCfg)
	deltaCh := make(chan DeltaUpdate, deltaBufSize)

	aggSketch := NewHLLOcto()

	workers, inputChans, shardSketches, batchChans, recycleChans, err := buildBatchedWorkers(numWorkers, sharedTau, func() (CellSketch, error) {
		return NewHLLOcto(), nil
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	agg := newShardedAggregator(aggSketch, shardSketches, readonlyBatchChans(batchChans), writeonlyBatchChans(recycleChans))
	tauCtrl := NewTauController(sharedTau, batchQueueDepth(batchChans))

	return agg, workers, inputChans, deltaCh, tauCtrl, nil
}

// buildBatchedWorkers creates one batched worker and one aggregator shard per worker.
func buildBatchedWorkers(
	numWorkers int,
	tau *AdaptiveTau,
	newSketch func() (CellSketch, error),
) ([]*Worker, []chan<- *common.SketchInput, []CellSketch, []chan *deltaBatch, []chan *deltaBatch, error) {
	workers := make([]*Worker, numWorkers)
	inputChans := make([]chan<- *common.SketchInput, numWorkers)
	shardSketches := make([]CellSketch, numWorkers)
	batchChans := make([]chan *deltaBatch, numWorkers)
	recycleChans := make([]chan *deltaBatch, numWorkers)
	batchQueueSize := DefaultBatchPoolSize
	for i := range numWorkers {
		workerSketch, err := newSketch()
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		shardSketch, err := newSketch()
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		batchCh := make(chan *deltaBatch, batchQueueSize)
		recycleCh := make(chan *deltaBatch, batchQueueSize+1)
		for j := 0; j < cap(recycleCh); j++ {
			recycleCh <- newDeltaBatch()
		}
		inputCh := make(chan *common.SketchInput, 1024)
		shardSketches[i] = shardSketch
		batchChans[i] = batchCh
		recycleChans[i] = recycleCh
		inputChans[i] = inputCh
		workers[i] = newBatchedWorker(i, workerSketch, tau, batchCh, recycleCh, inputCh)
	}
	return workers, inputChans, shardSketches, batchChans, recycleChans, nil
}

// NewDDSketchSystem wires up numWorkers DDSketch Workers and one Aggregator
// with a fixed threshold τ.
//
// alpha ∈ (0,1) is the DDSketch relative-error guarantee parameter.
// tau is the per-bucket emission threshold (OctoSketch delay bound).
//
// Insert values via common.FromF64(value); query via common.FromF64(phi).
func NewDDSketchSystem(
	alpha float64,
	tau float64,
	numWorkers, deltaBufSize int,
) (*Aggregator, []*Worker, []chan<- *common.SketchInput, chan DeltaUpdate, error) {
	agg, workers, inputChans, deltaCh, _, err := NewDDSketchSystemAdaptive(
		alpha, FixedTauConfig(tau), numWorkers, deltaBufSize,
	)
	return agg, workers, inputChans, deltaCh, err
}

// NewDDSketchSystemAdaptive wires up numWorkers DDSketch Workers and one Aggregator
// with a shared AdaptiveTau and TauController.
//
// The returned *TauController is NOT started automatically.
// Call tauCtrl.Run() after starting Workers; call tauCtrl.Stop() before closing deltaCh.
func NewDDSketchSystemAdaptive(
	alpha float64,
	tauCfg TauConfig,
	numWorkers, deltaBufSize int,
) (*Aggregator, []*Worker, []chan<- *common.SketchInput, chan DeltaUpdate, *TauController, error) {
	ensureDedicatedThreads(numWorkers + 1)
	sharedTau := NewAdaptiveTau(tauCfg)
	deltaCh := make(chan DeltaUpdate, deltaBufSize)

	aggSketch, err := NewDDSketchOcto(alpha)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	workers, inputChans, shardSketches, batchChans, recycleChans, err := buildBatchedWorkers(numWorkers, sharedTau, func() (CellSketch, error) {
		return NewDDSketchOcto(alpha)
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	agg := newShardedAggregator(aggSketch, shardSketches, readonlyBatchChans(batchChans), writeonlyBatchChans(recycleChans))
	tauCtrl := NewTauController(sharedTau, batchQueueDepth(batchChans))

	return agg, workers, inputChans, deltaCh, tauCtrl, nil
}

func readonlyBatchChans(batchChans []chan *deltaBatch) []<-chan *deltaBatch {
	readonly := make([]<-chan *deltaBatch, len(batchChans))
	for i := range batchChans {
		readonly[i] = batchChans[i]
	}
	return readonly
}

func writeonlyBatchChans(batchChans []chan *deltaBatch) []chan<- *deltaBatch {
	writeonly := make([]chan<- *deltaBatch, len(batchChans))
	for i := range batchChans {
		writeonly[i] = batchChans[i]
	}
	return writeonly
}

func batchQueueDepth(batchChans []chan *deltaBatch) func() int {
	return func() int {
		depth := 0
		for _, ch := range batchChans {
			depth += len(ch)
		}
		return depth
	}
}

func mergeCellSketch(dst, src CellSketch) error {
	mergeDst, ok := dst.(common.Sketch)
	if !ok {
		return errors.New("octosketch: destination sketch does not implement common.Sketch")
	}
	mergeSrc, ok := src.(common.Sketch)
	if !ok {
		return errors.New("octosketch: source sketch does not implement common.Sketch")
	}
	return mergeDst.Merge(mergeSrc)
}

// ─────────────────────────────────────────────────────────────────────────────
// Factory functions — return standalone sketch types directly.
// These satisfy both CellSketch and OctoSketch interfaces.
// ─────────────────────────────────────────────────────────────────────────────

// NewCountMinOcto creates a CountMinSketch suitable for use as a Worker or Aggregator.
func NewCountMinOcto(rows, cols int) (*countminsketch.CountMinSketch, error) {
	return countminsketch.NewCountMinSketch(rows, cols)
}

// NewCountSketchOcto creates a CountSketch suitable for use as a Worker or Aggregator.
// cols must be a power of two.
func NewCountSketchOcto(rows, cols int) (*countsketch.CountSketch, error) {
	return countsketch.NewCountSketch(rows, cols)
}

// NewHLLOcto creates a HyperLogLog suitable for use as a Worker or Aggregator.
func NewHLLOcto() *hll.HyperLogLog {
	return hll.NewHyperLogLog()
}

// NewDDSketchOcto creates a DDSketch suitable for use as a Worker or Aggregator.
// alpha ∈ (0,1) is the relative-error guarantee parameter.
func NewDDSketchOcto(alpha float64) (*ddsketch.DDSketch, error) {
	if alpha <= 0 || alpha >= 1 {
		return nil, errors.New("NewDDSketchOcto: alpha must be in (0,1)")
	}
	return ddsketch.NewDDSketch(alpha), nil
}
