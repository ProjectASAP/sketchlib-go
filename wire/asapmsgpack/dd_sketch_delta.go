package asapmsgpack

import "fmt"

// MarshalDDSketchDelta / UnmarshalDDSketchDelta encode a sparse DDSketch
// bucket delta as the 2-element MessagePack array
//
//	[ idx:[]int32, dCount:[]uint64 ]
//
// byte-identical to Rust `rmp_serde::to_vec(&(Vec<i32>, Vec<u64>))` (compact
// mode) — the layout emitted by asap_sketchlib's
// `DdSketch::compute_delta_msgpack` / `DdSketchDelta::to_msgpack`. `idx`
// holds absolute bucket indices (signed; a store can start at a negative
// index), `dCount` the additive per-bucket count deltas. Parallel arrays
// (not a struct-per-bucket) keep the wire compact and mirror the Rust
// producer.

// MarshalDDSketchDelta emits the 2-element bucket-delta array.
func MarshalDDSketchDelta(idx []int32, dCount []uint64) ([]byte, error) {
	if len(idx) != len(dCount) {
		return nil, fmt.Errorf("asapmsgpack: DDSketch delta arrays length mismatch (%d/%d)",
			len(idx), len(dCount))
	}
	e := newEncoder()
	e.writeArrayLen(2)
	e.writeArrayLen(len(idx))
	for _, v := range idx {
		e.writeInt(int64(v))
	}
	e.writeArrayLen(len(dCount))
	for _, v := range dCount {
		e.writeUint(v)
	}
	return e.bytes(), nil
}

// UnmarshalDDSketchDelta is the inverse of MarshalDDSketchDelta.
func UnmarshalDDSketchDelta(buf []byte) (idx []int32, dCount []uint64, err error) {
	d := newDecoder(buf)
	n, err := d.readArrayLen()
	if err != nil {
		return
	}
	if n != 2 {
		return nil, nil, fmt.Errorf("asapmsgpack: DDSketch delta expected 2-element array, got %d", n)
	}

	ni, err := d.readArrayLen()
	if err != nil {
		return
	}
	idx = make([]int32, ni)
	for i := 0; i < ni; i++ {
		v, e := d.readInt()
		if e != nil {
			return nil, nil, e
		}
		idx[i] = int32(v)
	}

	nc, err := d.readArrayLen()
	if err != nil {
		return
	}
	dCount = make([]uint64, nc)
	for i := 0; i < nc; i++ {
		v, e := d.readUint()
		if e != nil {
			return nil, nil, e
		}
		dCount[i] = v
	}

	if len(idx) != len(dCount) {
		return nil, nil, fmt.Errorf("asapmsgpack: DDSketch delta decoded length mismatch")
	}
	if err = d.done(); err != nil {
		return nil, nil, err
	}
	return idx, dCount, nil
}
