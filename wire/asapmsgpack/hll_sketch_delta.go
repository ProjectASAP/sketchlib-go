package asapmsgpack

import "fmt"

// MarshalHLLDelta / UnmarshalHLLDelta encode a sparse HLL register delta as
// the 2-element MessagePack array
//
//	[ regIdx:[]uint32, regVal:[]uint8 ]
//
// byte-identical to Rust `rmp_serde::to_vec(&(Vec<u32>, Vec<u8>))` (compact
// mode) — the layout emitted by asap_sketchlib's
// `HllSketch::compute_delta_msgpack` / `HllSketchDelta::to_msgpack`. Each
// pair is `(register index, new max value)`; HLL merges register-wise by
// MAX, so a delta carries only registers that increased. `regVal` rides as
// an array-of-int (rmp_serde's `Vec<u8>` shape), NOT msgpack `bin`.

// MarshalHLLDelta emits the 2-element register-delta array.
func MarshalHLLDelta(regIdx []uint32, regVal []uint8) ([]byte, error) {
	if len(regIdx) != len(regVal) {
		return nil, fmt.Errorf("asapmsgpack: HLL delta arrays length mismatch (%d/%d)",
			len(regIdx), len(regVal))
	}
	e := newEncoder()
	e.writeArrayLen(2)
	e.writeArrayLen(len(regIdx))
	for _, v := range regIdx {
		e.writeUint(uint64(v))
	}
	e.writeU8Array(regVal)
	return e.bytes(), nil
}

// UnmarshalHLLDelta is the inverse of MarshalHLLDelta.
func UnmarshalHLLDelta(buf []byte) (regIdx []uint32, regVal []uint8, err error) {
	d := newDecoder(buf)
	n, err := d.readArrayLen()
	if err != nil {
		return
	}
	if n != 2 {
		return nil, nil, fmt.Errorf("asapmsgpack: HLL delta expected 2-element array, got %d", n)
	}

	ni, err := d.readArrayLen()
	if err != nil {
		return
	}
	regIdx = make([]uint32, ni)
	for i := 0; i < ni; i++ {
		v, e := d.readUint()
		if e != nil {
			return nil, nil, e
		}
		regIdx[i] = uint32(v)
	}

	regVal, err = d.readU8Array()
	if err != nil {
		return nil, nil, err
	}
	if len(regIdx) != len(regVal) {
		return nil, nil, fmt.Errorf("asapmsgpack: HLL delta decoded length mismatch")
	}
	if err = d.done(); err != nil {
		return nil, nil, err
	}
	return regIdx, regVal, nil
}
