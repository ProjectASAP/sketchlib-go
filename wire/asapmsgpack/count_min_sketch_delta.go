package asapmsgpack

// MarshalCountMinDelta / UnmarshalCountMinDelta encode a sparse Count-Min
// cell delta as the shared 5-element `[]int64`-cell array (see
// cell_delta.go), byte-identical to asap_sketchlib's
// `CountMinSketch::compute_delta_msgpack` / `CountMinSketchDelta::to_msgpack`.
// Only the changed matrix cells cross the wire; the per-row L1/L2 norms and
// heavy-hitter keys the proto delta carries are dropped from the msgpack form.

// MarshalCountMinDelta emits the 5-element Count-Min cell-delta array.
func MarshalCountMinDelta(rows, cols uint64, rowIdx, colIdx []uint32, dCount []int64) ([]byte, error) {
	return marshalCellDeltaInt64(rows, cols, rowIdx, colIdx, dCount)
}

// UnmarshalCountMinDelta is the inverse of MarshalCountMinDelta.
func UnmarshalCountMinDelta(buf []byte) (rows, cols uint64, rowIdx, colIdx []uint32, dCount []int64, err error) {
	return unmarshalCellDeltaInt64(buf)
}
