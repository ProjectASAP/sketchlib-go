package asapmsgpack

// Magic-ID constants for the portable MessagePack wire format.
//
// Every serialized binary produced by a sketch's SerializeMsgpack method is
// wrapped in the ASAPv1 envelope (see wire/asapmsgpack/wrapper.go):
//
//	[ b"ASAPv1" | version:u8 | kind_id_len:u8 | kind_id:bytes | metadata_len:u32 | metadata:msgpack | msgpack_payload ]
//
// The constants below are the 1-byte kind_id values for each portable sketch
// type. Magic IDs are stable — once assigned, a value is never reused or
// repurposed. Adding a new sketch type requires a new constant here and a
// corresponding entry in asap_sketchlib/src/message_pack_format/magic_ids.rs.
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

// ASAPv1 two-byte kind_id values `[family, variant]`
// (asap_sketchlib/docs/asapv1_wire_format.md §1). kind_id names the sketch's
// *algorithm identity* only — structural params (HLL precision, CMS counter
// type / mode) live in the metadata. These are the mirrored, single-source-of-
// truth ids for the ASAPv1-aligned HLL and Count-Min payloads; the family byte
// matches the 1-byte Magic* constants above, with the variant byte added.
var (
	// HLLKindClassic is HLL with the Classic ("Regular") estimator.
	HLLKindClassic = []byte{MagicHLL, 0x01}
	// HLLKindErtlMLE is HLL with the Ertl-MLE ("Datafusion") estimator.
	HLLKindErtlMLE = []byte{MagicHLL, 0x02}
	// HLLKindHIP is HLL with the HIP estimator.
	HLLKindHIP = []byte{MagicHLL, 0x03}

	// CMSKind is the single Count-Min kind_id (counter type + mode are metadata).
	CMSKind = []byte{MagicCountMinSketch, 0x00}
)
