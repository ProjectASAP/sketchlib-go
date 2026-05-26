package asapmsgpack

// DDSketchState mirrors the Rust `sketch_core::dd_sketch::DdSketch`
// struct field-for-field. Field order is load-bearing: it MUST match
// the Rust declaration order so rmp_serde's array-of-fields layout
// round-trips byte-for-byte.
//
// The DataPoint-level metric scalars (count/sum/min/max) are no longer
// carried on the wire: count is recovered by summing store_counts and
// min/max are derived from the bucket distribution on the receiver.
type DDSketchState struct {
	Alpha       float64
	StoreCounts []uint64
	StoreOffset int32
}

// MarshalDDSketch emits the MessagePack payload that the Rust
// consumer's `sketch_core::dd_sketch::DdSketch::deserialize_msgpack`
// accepts when the modified-OTLP `DDSketchDataPoint.encoding` is set to
// `DDSKETCH_ENCODING_MSGPACK = 3`.
//
// Wire format (rmp_serde compact mode on Rust `DdSketch`):
//
//	[
//	  alpha        : float64
//	  store_counts : []uint64
//	  store_offset : int32
//	]
//
// Merge semantics on the receiver: two sketches with the same alpha
// are merged by aligning bucket arrays along store_offset and summing
// element-wise. count is recovered by summing the bucket counts and
// min/max are derived from the bucket distribution.
func MarshalDDSketch(s DDSketchState) ([]byte, error) {
	e := newEncoder()
	e.writeArrayLen(3)
	e.writeFloat64(s.Alpha)
	e.writeArrayLen(len(s.StoreCounts))
	for _, v := range s.StoreCounts {
		e.writeUint(v)
	}
	e.writeInt(int64(s.StoreOffset))
	return e.bytes(), nil
}

// UnmarshalDDSketch is the inverse of MarshalDDSketch.
func UnmarshalDDSketch(buf []byte) (DDSketchState, error) {
	var s DDSketchState
	d := newDecoder(buf)
	n, err := d.readArrayLen()
	if err != nil {
		return s, err
	}
	if n != 3 {
		return s, errWrongLen("DDSketch", 3, n)
	}
	if s.Alpha, err = d.readFloat64(); err != nil {
		return s, err
	}
	bucketCount, err := d.readArrayLen()
	if err != nil {
		return s, err
	}
	s.StoreCounts = make([]uint64, bucketCount)
	for i := 0; i < bucketCount; i++ {
		if s.StoreCounts[i], err = d.readUint(); err != nil {
			return s, err
		}
	}
	off, err := d.readInt()
	if err != nil {
		return s, err
	}
	s.StoreOffset = int32(off)
	if err := d.done(); err != nil {
		return s, err
	}
	return s, nil
}
