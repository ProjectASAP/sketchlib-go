package asapmsgpack

import "fmt"

// marshalCellDeltaInt64 / unmarshalCellDeltaInt64 encode a sparse
// matrix-cell delta as the 5-element MessagePack array
//
//	[ rows:uint64, cols:uint64, rowIdx:[]uint32, colIdx:[]uint32, dCount:[]int64 ]
//
// byte-identical to Rust
// `rmp_serde::to_vec(&(u64, u64, Vec<u32>, Vec<u32>, Vec<i64>))` (compact
// mode). This is the shared shape emitted by asap_sketchlib's
// `CountMinSketch` / `CountSketch` `compute_delta_msgpack` — signed integer
// cell deltas (rmp_serde encodes non-negative `i64` in the compact unsigned
// form, so a delta of all-positive cells is byte-identical to a `u64` one).
//
// NOTE: this is distinct from `count_sketch_delta_sparse.go`, whose vals are
// `[]float64` — that file serves the geometric-F2 `C_ref` reference-matrix
// broadcast, a separate data flow.
func marshalCellDeltaInt64(rows, cols uint64, rowIdx, colIdx []uint32, dCount []int64) ([]byte, error) {
	if len(rowIdx) != len(colIdx) || len(rowIdx) != len(dCount) {
		return nil, fmt.Errorf("asapmsgpack: cell delta arrays length mismatch (%d/%d/%d)",
			len(rowIdx), len(colIdx), len(dCount))
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
	e.writeArrayLen(len(dCount))
	for _, v := range dCount {
		e.writeInt(v)
	}
	return e.bytes(), nil
}

func unmarshalCellDeltaInt64(buf []byte) (rows, cols uint64, rowIdx, colIdx []uint32, dCount []int64, err error) {
	d := newDecoder(buf)
	n, err := d.readArrayLen()
	if err != nil {
		return
	}
	if n != 5 {
		return 0, 0, nil, nil, nil, fmt.Errorf("asapmsgpack: cell delta expected 5-element array, got %d", n)
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
	dCount = make([]int64, m)
	for i := 0; i < m; i++ {
		if dCount[i], err = d.readInt(); err != nil {
			return
		}
	}

	if len(rowIdx) != len(colIdx) || len(rowIdx) != len(dCount) {
		return 0, 0, nil, nil, nil, fmt.Errorf("asapmsgpack: cell delta decoded length mismatch")
	}
	if err = d.done(); err != nil {
		return 0, 0, nil, nil, nil, err
	}
	return rows, cols, rowIdx, colIdx, dCount, nil
}
