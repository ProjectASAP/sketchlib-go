package asapmsgpack

// MarshalCountSketchCellDelta / UnmarshalCountSketchCellDelta encode a sparse
// CountSketch signed-cell delta as the shared 5-element `[]int64`-cell array
// (see cell_delta.go), byte-identical to asap_sketchlib's
// `CountSketch::compute_delta_msgpack` / `CountSketchDelta::to_msgpack`. Only
// the changed (signed) matrix cells cross the wire; the per-row L2 norms and
// heavy-hitter keys the proto delta carries are dropped from the msgpack form.
//
// This is the sketch delta-transmission counterpart. It is distinct from
// `count_sketch_delta_sparse.go` (`MarshalCountSketchDeltaSparse`), whose
// `[]float64` vals serve the geometric-F2 `C_ref` reference-matrix broadcast.

// MarshalCountSketchCellDelta emits the 5-element CountSketch cell-delta array.
func MarshalCountSketchCellDelta(rows, cols uint64, rowIdx, colIdx []uint32, dCount []int64) ([]byte, error) {
	return marshalCellDeltaInt64(rows, cols, rowIdx, colIdx, dCount)
}

// UnmarshalCountSketchCellDelta is the inverse of MarshalCountSketchCellDelta.
func UnmarshalCountSketchCellDelta(buf []byte) (rows, cols uint64, rowIdx, colIdx []uint32, dCount []int64, err error) {
	return unmarshalCellDeltaInt64(buf)
}
