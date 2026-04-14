package asapmsgpack

import "fmt"

// MarshalCountMinSketch emits the MessagePack payload that the Rust
// consumer's `sketch_core::count_min::CountMinSketch::deserialize_msgpack`
// accepts when the modified-OTLP `CountMinSketchDataPoint.encoding` is
// set to `COUNT_MIN_SKETCH_ENCODING_MSGPACK = 3`.
//
// Wire format (rmp_serde compact mode on Rust `WireFormat`):
//
//	[
//	  matrix  : [][]float64   // row-major, shape [rows][cols]
//	  rowNum  : uint64
//	  colNum  : uint64
//	]
//
// Matrix elements are the Count[r][c] cell values cast to float64 (to
// match the legacy Arroyo producer's wire shape, which is what the Rust
// `CountMinSketch::deserialize_msgpack` was originally written to
// accept). Non-rectangular matrices are rejected before any bytes are
// emitted.
func MarshalCountMinSketch(rowNum, colNum uint64, matrix [][]float64) ([]byte, error) {
	if uint64(len(matrix)) != rowNum {
		return nil, fmt.Errorf(
			"asapmsgpack: CountMinSketch matrix has %d rows, expected %d",
			len(matrix), rowNum)
	}
	for r, row := range matrix {
		if uint64(len(row)) != colNum {
			return nil, fmt.Errorf(
				"asapmsgpack: CountMinSketch row %d has %d cols, expected %d",
				r, len(row), colNum)
		}
	}
	e := newEncoder()
	e.writeArrayLen(3)
	e.writeFloat64Matrix(matrix)
	e.writeUint(rowNum)
	e.writeUint(colNum)
	return e.bytes(), nil
}

// UnmarshalCountMinSketch is the inverse of MarshalCountMinSketch.
// Primarily exists for round-trip tests; production consumers use the
// Rust side's `deserialize_msgpack`.
func UnmarshalCountMinSketch(buf []byte) (matrix [][]float64, rowNum, colNum uint64, err error) {
	d := newDecoder(buf)
	n, err := d.readArrayLen()
	if err != nil {
		return nil, 0, 0, err
	}
	if n != 3 {
		return nil, 0, 0, fmt.Errorf(
			"asapmsgpack: CountMinSketch expected 3-element array, got %d", n)
	}
	matrix, err = d.readFloat64Matrix()
	if err != nil {
		return nil, 0, 0, err
	}
	rowNum, err = d.readUint()
	if err != nil {
		return nil, 0, 0, err
	}
	colNum, err = d.readUint()
	if err != nil {
		return nil, 0, 0, err
	}
	if err := d.done(); err != nil {
		return nil, 0, 0, err
	}
	return matrix, rowNum, colNum, nil
}
