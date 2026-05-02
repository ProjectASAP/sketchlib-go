package octosketch

import "github.com/ProjectASAP/sketchlib-go/common"

// CellSketch is the pluggable sketch interface used by the generic Worker.Process loop.
//
// Every method corresponds to exactly one responsibility in the OctoSketch algorithm.
// The Worker owns the delta channel and the threshold τ; the sketch owns only its
// data structure. This enforces the extensibility contract: no sketch-specific logic
// belongs in the core Worker.
//
// To add a new sketch type, implement this interface. No changes to Worker, Aggregator,
// or AdaptiveTau are needed.
//
// Update rule   → UpdateCell   (e.g. count++ for CMS, ±sign for CS, max(lz) for HLL)
// Merge rule    → MergeDelta   (e.g. addition for CMS/CS, max for HLL)
// Threshold rule → ShouldEmit  (e.g. val >= τ, |val| >= τ, or always-true for HLL)
// Reset rule    → ResetCell    (e.g. cell=0 for CMS/CS, no-op for HLL)
type CellSketch interface {
	// ── Geometry ──────────────────────────────────────────────────────────────

	// NumRows returns the depth of the sketch matrix.
	// For HLL this is 1 (one register-index per hash).
	NumRows() int

	// ColForRow derives the column index for row r from input's pre-computed hash.
	// Implementations must be pure (same input → same col, no side effects).
	ColForRow(input *common.SketchInput, row int) int

	// ── Per-cell operations (called by Worker.Process) ─────────────────────────

	// UpdateCell applies the sketch-specific update at (row, col).
	// Returns (newVal, changed):
	//   newVal  – post-update cell value used by ShouldEmit.
	//   changed – true if the cell actually changed (always true for CMS/CS;
	//             true only when the register increases for HLL).
	UpdateCell(row, col int, input *common.SketchInput) (newVal float64, changed bool)

	// ShouldEmit returns true if newVal warrants a delta emission given threshold τ.
	//   CountMin:    newVal >= τ
	//   CountSketch: |newVal| >= τ
	//   HLL:         always true (called only when changed=true)
	ShouldEmit(newVal, tau float64) bool

	// BuildDelta constructs the DeltaUpdate to send on the delta channel.
	// Called by Worker only when ShouldEmit returned true.
	BuildDelta(row, col int, input *common.SketchInput) DeltaUpdate

	// ResetCell zeros the cell after a delta is emitted.
	// Must be a no-op for sketches with monotone registers (e.g. HLL).
	ResetCell(row, col int)

	// ── Aggregator operations ──────────────────────────────────────────────────

	// MergeDelta applies a received delta to the global sketch state.
	//   CountMin/CountSketch: addition
	//   HLL:                  max
	MergeDelta(delta DeltaUpdate)

	// OctoEstimate returns the current frequency / cardinality estimate.
	// For HLL the input argument is ignored (global cardinality).
	//
	// This is named `OctoEstimate` rather than `Estimate` so that sketches
	// whose canonical estimator does not take an input argument (HLL) can
	// satisfy this interface via a thin shim, while keeping their canonical
	// `Estimate(...)` API aligned with the Rust unified-API naming.
	OctoEstimate(input *common.SketchInput) float64

	// ── Lifecycle ─────────────────────────────────────────────────────────────

	// Flush invokes emit for every pending non-zero cell at end-of-window.
	// Workers call this after their input channel closes to drain sub-τ state.
	// HLL implementations must NOT reset registers inside Flush.
	Flush(emit func(DeltaUpdate))
}
