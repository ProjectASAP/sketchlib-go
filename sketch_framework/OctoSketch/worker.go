package octosketch

import "github.com/ProjectASAP/sketchlib-go/common"

const defaultDeltaBatchSize = 64

type deltaBatch struct {
	deltas []DeltaUpdate
}

func newDeltaBatch() *deltaBatch {
	return &deltaBatch{deltas: make([]DeltaUpdate, 0, defaultDeltaBatchSize)}
}

type optimizedProcessor interface {
	ProcessInput(input *common.SketchInput, tau float64, emit func(DeltaUpdate))
}

// Worker maintains a per-core CellSketch, processes stream items via the generic
// Process loop, and emits DeltaUpdates to the shared delta channel when local
// counters cross the adaptive threshold τ.
//
// The Worker owns the channel reference and threshold — the sketch owns only its
// data structure. This separation enforces the extensibility contract.
//
// Lifecycle:
//  1. Create with NewWorker.
//  2. Call Run() to start the goroutine, OR call Process() directly from tests.
//  3. Send items to the input channel (or call Process directly).
//  4. Close the input channel to signal end-of-stream.
//  5. Wait on Done() for the goroutine to flush and exit.
type Worker struct {
	id        int
	sketch    CellSketch
	tau       *AdaptiveTau
	deltaCh   chan<- DeltaUpdate
	batchCh   chan<- *deltaBatch
	recycleCh <-chan *deltaBatch
	inputCh   <-chan *common.SketchInput
	doneCh    chan struct{}
	batchBuf  *deltaBatch
	// emitFn is w.emitDelta bound once at construction to avoid a 16-byte
	// closure allocation on every call to Process.
	emitFn func(DeltaUpdate)
}

// NewWorker creates a Worker.
//   - sketch: any CellSketch implementation (CountMinOcto, CountSketchOcto, HLLOcto, …)
//   - tau: shared AdaptiveTau (use FixedTauConfig for deterministic τ)
//   - deltaCh: send-only end of the shared delta channel
//   - inputCh: stream of incoming sketch items
func NewWorker(
	id int,
	sketch CellSketch,
	tau *AdaptiveTau,
	deltaCh chan<- DeltaUpdate,
	inputCh <-chan *common.SketchInput,
) *Worker {
	w := &Worker{
		id:      id,
		sketch:  sketch,
		tau:     tau,
		deltaCh: deltaCh,
		inputCh: inputCh,
		doneCh:  make(chan struct{}),
	}
	w.emitFn = w.emitDelta
	return w
}

func newBatchedWorker(
	id int,
	sketch CellSketch,
	tau *AdaptiveTau,
	batchCh chan<- *deltaBatch,
	recycleCh <-chan *deltaBatch,
	inputCh <-chan *common.SketchInput,
) *Worker {
	w := &Worker{
		id:        id,
		sketch:    sketch,
		tau:       tau,
		batchCh:   batchCh,
		recycleCh: recycleCh,
		inputCh:   inputCh,
		doneCh:    make(chan struct{}),
		batchBuf:  <-recycleCh,
	}
	w.emitFn = w.emitDelta
	return w
}

func (w *Worker) emitDelta(delta DeltaUpdate) {
	if w.batchCh == nil {
		w.deltaCh <- delta
		return
	}
	w.batchBuf.deltas = append(w.batchBuf.deltas, delta)
	if len(w.batchBuf.deltas) >= cap(w.batchBuf.deltas) {
		w.flushBatch()
	}
}

func (w *Worker) flushBatch() {
	if w.batchCh == nil || len(w.batchBuf.deltas) == 0 {
		return
	}
	w.batchCh <- w.batchBuf
	w.batchBuf = <-w.recycleCh
}

// Process is the generic OctoSketch worker loop (spec §4).
//
// For each hash row:
//  1. Derive the column index from input's hash.
//  2. Apply the sketch-specific cell update (UpdateCell).
//  3. If the cell actually changed AND newVal crosses τ → emit delta + reset cell.
//
// No sketch-specific logic lives here. The threshold rule (ShouldEmit), update rule
// (UpdateCell), and reset rule (ResetCell) are all delegated to the CellSketch.
func (w *Worker) Process(input *common.SketchInput) {
	if input == nil {
		return
	}
	tau := w.tau.Current()
	if fast, ok := w.sketch.(optimizedProcessor); ok {
		fast.ProcessInput(input, tau, w.emitFn)
		return
	}
	for row := range w.sketch.NumRows() {
		col := w.sketch.ColForRow(input, row)
		newVal, changed := w.sketch.UpdateCell(row, col, input)
		if changed && w.sketch.ShouldEmit(newVal, tau) {
			w.emitDelta(w.sketch.BuildDelta(row, col, input))
			w.sketch.ResetCell(row, col)
		}
	}
}

// Flush drains residual sub-τ cells from the local sketch by invoking the sketch's
// Flush with the worker's delta channel as the emit target.
// Workers call this when their input stream ends (before closing the delta channel).
func (w *Worker) Flush() {
	w.sketch.Flush(w.emitFn)
	w.flushBatch()
}

// Run launches the worker goroutine.
// It calls Process for each item from inputCh, then Flush after the channel closes.
// Done() is closed after Flush() completes.
func (w *Worker) Run() {
	go func() {
		defer close(w.doneCh)
		if w.batchCh != nil {
			defer close(w.batchCh)
		}
		for input := range w.inputCh {
			w.Process(input)
		}
		w.Flush()
	}()
}

// Done returns a channel closed after the goroutine exits.
func (w *Worker) Done() <-chan struct{} { return w.doneCh }

// ID returns the worker's numeric identifier.
func (w *Worker) ID() int { return w.id }
