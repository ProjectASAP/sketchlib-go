// Package octosketch implements the OctoSketch multi-core sketch aggregation framework.
//
// OctoSketch uses Δ-based (delta) updates: each Worker maintains a local sketch,
// accumulates counts until a threshold τ is reached, then emits a DeltaUpdate to
// the shared channel. The Aggregator applies incoming deltas to a global sketch and
// serves queries.
package octosketch

import "github.com/ProjectASAP/sketchlib-go/common"

// DeltaUpdate is re-exported from common for backward compatibility.
// All packages that import octosketch can continue using octosketch.DeltaUpdate
// interchangeably with common.DeltaUpdate.
type DeltaUpdate = common.DeltaUpdate

// OctoSketch is the core interface for sketches participating in the framework.
// A Worker-side implementation handles Insert and emits DeltaUpdates internally.
// An Aggregator-side implementation handles MergeDelta and answers Estimate queries.
type OctoSketch interface {
	// Insert processes one stream item, updating local counters.
	// On the Worker side this may emit DeltaUpdates when τ is reached.
	Insert(input *common.SketchInput)

	// MergeDelta applies a cell-level delta to the sketch.
	// On the Aggregator side this accumulates worker deltas into the global sketch.
	MergeDelta(delta DeltaUpdate)

	// Estimate returns the estimated frequency (or other statistic) for input.
	Estimate(input *common.SketchInput) float64
}
