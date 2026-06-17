package asapmsgpack

// Magic-ID constants for the portable MessagePack wire format.
//
// Every serialized binary produced by a sketch's SerializeMsgpack method is
// prefixed with a single byte that identifies the sketch type, analogous to
// how Prometheus uses magic bytes to discriminate metric types.
//
// Prefix layout:
//
//	[ magic_id: uint8 | <msgpack payload> ]
//
// Magic IDs are stable across versions. Adding a new sketch type requires a
// new constant here; removing or repurposing an existing constant is a
// breaking protocol change. The Rust mirror of this table lives in
// asap_sketchlib/src/message_pack_format/magic_ids.rs.
const (
	// MagicHLL is the magic-ID prefix for HLL sketches (all variants).
	MagicHLL byte = 0x01

	// MagicCountMinSketch is the magic-ID prefix for Count-Min sketches (no heap).
	MagicCountMinSketch byte = 0x02

	// MagicCountMinSketchWithHeap is the magic-ID prefix for Count-Min sketches with a top-k heap.
	MagicCountMinSketchWithHeap byte = 0x03

	// MagicCountSketch is the magic-ID prefix for Count Sketch (signed counters).
	MagicCountSketch byte = 0x04

	// MagicDDSketch is the magic-ID prefix for DDSketch (quantile sketch).
	MagicDDSketch byte = 0x05

	// MagicKLLSketch is the magic-ID prefix for KLL quantile sketches.
	MagicKLLSketch byte = 0x06

	// MagicHydraKLLSketch is the magic-ID prefix for Hydra-KLL sketches.
	MagicHydraKLLSketch byte = 0x07

	// MagicSetAggregator is the magic-ID prefix for set aggregators.
	MagicSetAggregator byte = 0x08

	// MagicDeltaResult is the magic-ID prefix for delta-set aggregator results.
	MagicDeltaResult byte = 0x09
)
