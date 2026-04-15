package ddsketch

import (
	"math"

	"github.com/ProjectASAP/sketchlib-go/wire/asapmsgpack"
)

// SerializeMsgpack emits the MessagePack wire format ASAPQuery-backend
// decodes when the modified-OTLP `DDSketchDataPoint.encoding` is
// `DDSKETCH_ENCODING_MSGPACK = 3` (cross-language contract with
// `sketch_core::dd_sketch::DdSketch::deserialize_msgpack`).
//
// The sketch's `alpha` is not stored directly — DDSketch caches `gamma`
// and `invLogGamma` for performance and recomputes on load. We recover
// alpha from gamma via `alpha = (gamma - 1) / (gamma + 1)` so the Rust
// side can rebuild its own mapping.
//
// Bucket counts come directly from `store.counts` (which is a
// `Vec<u64>` in the Rust wire shape) and `store.offset` is the
// absolute-index base. Empty sketches emit an empty `store_counts`
// slice with `store_offset = 0`, matching the Rust constructor's
// empty-state defaults.
func (d *DDSketch) SerializeMsgpack() ([]byte, error) {
	// Recover alpha from the stored gamma:
	//   gamma = (1 + alpha) / (1 - alpha)
	//   → alpha = (gamma - 1) / (gamma + 1)
	alpha := (d.mapping.gamma - 1.0) / (d.mapping.gamma + 1.0)

	var (
		storeCounts []uint64
		storeOffset int32
	)
	if !d.store.IsEmpty() {
		// AsSlice() returns an internal view; clone it so the caller
		// can't mutate the sketch's backing storage through the
		// returned payload.
		src := d.store.counts.AsSlice()
		storeCounts = append([]uint64(nil), src...)
		storeOffset = d.store.offset
	}

	// The Rust `DdSketch::new(alpha)` constructor seeds `min = +Inf`
	// and `max = -Inf`. Our zero-value Go sketch starts with the same
	// sentinels (see `NewDDSketch`), so empty sketches round-trip
	// cleanly. If the caller constructs via a struct literal and
	// forgets to set min/max, we fall back to the same sentinels.
	min := d.min
	max := d.max
	if d.count == 0 {
		if math.IsNaN(min) || !math.IsInf(min, 1) {
			min = math.Inf(1)
		}
		if math.IsNaN(max) || !math.IsInf(max, -1) {
			max = math.Inf(-1)
		}
	}

	return asapmsgpack.MarshalDDSketch(asapmsgpack.DDSketchState{
		Alpha:       alpha,
		StoreCounts: storeCounts,
		StoreOffset: storeOffset,
		Count:       d.count,
		Sum:         d.sum,
		Min:         min,
		Max:         max,
	})
}
