package ddsketch

import (
	"errors"
	"fmt"

	"github.com/ProjectASAP/sketchlib-go/common/storage"
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

	payload, err := asapmsgpack.MarshalDDSketch(asapmsgpack.DDSketchState{
		Alpha:       alpha,
		StoreCounts: storeCounts,
		StoreOffset: storeOffset,
	})
	if err != nil {
		return nil, err
	}
	return append([]byte{asapmsgpack.MagicDDSketch}, payload...), nil
}

// DeserializeMsgpack rebuilds a DDSketch from the cross-language
// MessagePack wire format produced by SerializeMsgpack (and by Rust's
// `sketch_core::dd_sketch::DdSketch::serialize_msgpack`). Mirrors
// Rust's `deserialize_msgpack(bytes) -> Result<Self>`.
func DeserializeMsgpack(buf []byte) (*DDSketch, error) {
	if len(buf) == 0 || buf[0] != asapmsgpack.MagicDDSketch {
		got := byte(0)
		if len(buf) > 0 {
			got = buf[0]
		}
		return nil, fmt.Errorf("ddsketch: msgpack magic-ID mismatch: expected 0x%02x, got 0x%02x", asapmsgpack.MagicDDSketch, got)
	}
	state, err := asapmsgpack.UnmarshalDDSketch(buf[1:])
	if err != nil {
		return nil, fmt.Errorf("ddsketch: msgpack decode: %w", err)
	}
	if !(state.Alpha > 0 && state.Alpha < 1) {
		return nil, errors.New("ddsketch: alpha must be in (0, 1)")
	}
	mapping := NewIndexMapping(state.Alpha)
	var buckets Buckets
	if len(state.StoreCounts) > 0 {
		cp := append([]uint64(nil), state.StoreCounts...)
		buckets = Buckets{
			counts: storage.Vector1DFromVec(cp),
			offset: state.StoreOffset,
		}
	}
	// count/min/max are no longer carried on the wire; recompute them from
	// the bucket distribution. sum is unrecoverable and is left at zero.
	count, minVal, maxVal := deriveScalarsFromBuckets(&buckets, mapping)
	return &DDSketch{
		mapping: mapping,
		store:   buckets,
		count:   count,
		sum:     0,
		min:     minVal,
		max:     maxVal,
	}, nil
}
