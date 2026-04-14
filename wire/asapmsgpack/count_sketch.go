package asapmsgpack

import "fmt"

// MarshalCountSketch emits the MessagePack payload that the Rust
// consumer's `sketch_core::count_sketch::CountSketch::deserialize_msgpack`
// accepts when the modified-OTLP `CountSketchDataPoint.encoding` is
// set to `COUNT_SKETCH_ENCODING_MSGPACK = 3`.
//
// Wire format (rmp_serde compact mode on Rust `CountSketch`):
//
//	[
//	  rowNum : uint64
//	  colNum : uint64
//	  matrix : [][]float64   // row-major, shape [rows][cols]
//	]
//
// Note: the field order differs from CountMinSketch (which puts the
// matrix first). This mirrors the Rust struct declaration order and is
// intentional — PR I's Rust decoder expects this exact layout.
func MarshalCountSketch(rowNum, colNum uint64, matrix [][]float64) ([]byte, error) {
	if uint64(len(matrix)) != rowNum {
		return nil, fmt.Errorf(
			"asapmsgpack: CountSketch matrix has %d rows, expected %d",
			len(matrix), rowNum)
	}
	for r, row := range matrix {
		if uint64(len(row)) != colNum {
			return nil, fmt.Errorf(
				"asapmsgpack: CountSketch row %d has %d cols, expected %d",
				r, len(row), colNum)
		}
	}
	e := newEncoder()
	e.writeArrayLen(3)
	e.writeUint(rowNum)
	e.writeUint(colNum)
	e.writeFloat64Matrix(matrix)
	return e.bytes(), nil
}

// UnmarshalCountSketch is the inverse of MarshalCountSketch.
func UnmarshalCountSketch(buf []byte) (rowNum, colNum uint64, matrix [][]float64, err error) {
	d := newDecoder(buf)
	n, err := d.readArrayLen()
	if err != nil {
		return 0, 0, nil, err
	}
	if n != 3 {
		return 0, 0, nil, fmt.Errorf(
			"asapmsgpack: CountSketch expected 3-element array, got %d", n)
	}
	rowNum, err = d.readUint()
	if err != nil {
		return 0, 0, nil, err
	}
	colNum, err = d.readUint()
	if err != nil {
		return 0, 0, nil, err
	}
	matrix, err = d.readFloat64Matrix()
	if err != nil {
		return 0, 0, nil, err
	}
	if err := d.done(); err != nil {
		return 0, 0, nil, err
	}
	return rowNum, colNum, matrix, nil
}
