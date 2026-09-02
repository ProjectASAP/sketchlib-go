package asapmsgpack

import "fmt"

// MarshalCountSketchDeltaSparse / UnmarshalCountSketchDeltaSparse encode a sparse
// Count-Sketch cell delta as the 5-element MessagePack array
//
//	[ rows:uint64, cols:uint64, rowIdx:[]uint32, colIdx:[]uint32, vals:[]float64 ]
//
// byte-identical to Rust `rmp_serde::to_vec(&(u64, u64, Vec<u32>, Vec<u32>,
// Vec<f64>))` (compact mode). Used by the geometric-F2 `C_ref` delta broadcast:
// the coordinator ships only the changed cells since the last broadcast, and the
// edge applies them to its cached reference matrix. Parallel index/value arrays
// (not a struct-per-cell) mirror the Rust producer and keep the wire compact.

// MarshalCountSketchDeltaSparse emits the 5-element delta array.
func MarshalCountSketchDeltaSparse(rows, cols uint64, rowIdx, colIdx []uint32, vals []float64) ([]byte, error) {
	if len(rowIdx) != len(colIdx) || len(rowIdx) != len(vals) {
		return nil, fmt.Errorf("asapmsgpack: sparse delta arrays length mismatch (%d/%d/%d)",
			len(rowIdx), len(colIdx), len(vals))
	}
	e := newEncoder()
	e.writeArrayLen(5)
	e.writeUint(rows)
	e.writeUint(cols)
	e.writeArrayLen(len(rowIdx))
	for _, v := range rowIdx {
		e.writeUint(uint64(v))
	}
	e.writeArrayLen(len(colIdx))
	for _, v := range colIdx {
		e.writeUint(uint64(v))
	}
	e.writeArrayLen(len(vals))
	for _, v := range vals {
		e.writeFloat64(v)
	}
	return e.bytes(), nil
}

// UnmarshalCountSketchDeltaSparse is the inverse of MarshalCountSketchDeltaSparse.
func UnmarshalCountSketchDeltaSparse(buf []byte) (rows, cols uint64, rowIdx, colIdx []uint32, vals []float64, err error) {
	d := newDecoder(buf)
	n, err := d.readArrayLen()
	if err != nil {
		return
	}
	if n != 5 {
		return 0, 0, nil, nil, nil, fmt.Errorf("asapmsgpack: sparse delta expected 5-element array, got %d", n)
	}
	if rows, err = d.readUint(); err != nil {
		return
	}
	if cols, err = d.readUint(); err != nil {
		return
	}
	readU32Array := func() ([]uint32, error) {
		m, e := d.readArrayLen()
		if e != nil {
			return nil, e
		}
		out := make([]uint32, m)
		for i := 0; i < m; i++ {
			u, e := d.readUint()
			if e != nil {
				return nil, e
			}
			out[i] = uint32(u)
		}
		return out, nil
	}
	if rowIdx, err = readU32Array(); err != nil {
		return
	}
	if colIdx, err = readU32Array(); err != nil {
		return
	}
	m, err := d.readArrayLen()
	if err != nil {
		return
	}
	vals = make([]float64, m)
	for i := 0; i < m; i++ {
		if vals[i], err = d.readFloat64(); err != nil {
			return
		}
	}
	if len(rowIdx) != len(colIdx) || len(rowIdx) != len(vals) {
		return 0, 0, nil, nil, nil, fmt.Errorf("asapmsgpack: sparse delta decoded length mismatch")
	}
	return
}
