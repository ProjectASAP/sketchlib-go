package asapmsgpack

import "fmt"

// Count-Min wire counter types and column-derivation modes carried in the
// metadata (asap_sketchlib/docs/asapv1_wire_format.md §2/§3.2). Count-Min is a
// single kind_id (CMSKind); these two structural params shape the payload.
const (
	// CMSCounterI64 marks integer counters (`counts` elements are integers).
	CMSCounterI64 = "i64"
	// CMSCounterF64 marks float counters (`counts` elements are float64).
	CMSCounterF64 = "f64"

	// CMSModeRegular derives a column from a per-row hash.
	CMSModeRegular = "regular"
	// CMSModeFast derives all columns by bit-slicing one hash.
	CMSModeFast = "fast"
)

// MarshalCountMinSketch encodes a Count-Min sketch into a complete ASAPv1
// envelope (asap_sketchlib/docs/asapv1_wire_format.md §1/§2/§3.2).
//
// The sketchlib-go Count-Min sketch stores float64 counters and derives columns
// by bit-slicing a single hash, so it serializes as the `f64` counter type in
// `fast` mode. The payload is a positional MessagePack array:
//
//	[ rows:uint, cols:uint, counts:array<f64> ]   // counts packed row-major
//
// counter_type and mode live in the metadata, not the payload. Non-rectangular
// matrices are rejected before any bytes are emitted.
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
	e.writeUint(rowNum)
	e.writeUint(colNum)
	e.writeArrayLen(int(rowNum * colNum))
	for _, row := range matrix {
		for _, v := range row {
			e.writeFloat64(v)
		}
	}

	metadata := encodeCMSMetadata(CMSCounterF64, CMSModeFast)
	return EncodeWrapper(CMSKind, metadata, e.bytes()), nil
}

// UnmarshalCountMinSketch is the inverse of MarshalCountMinSketch: it takes a
// complete ASAPv1 envelope and returns the row-major matrix as [][]float64,
// failing closed on any envelope, metadata or payload inconsistency. Integer
// (`i64`) counters are widened to float64; float (`f64`) counters pass through.
func UnmarshalCountMinSketch(buf []byte) (matrix [][]float64, rowNum, colNum uint64, err error) {
	kindID, metadata, payload, err := SplitWrapper(buf)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(kindID) != len(CMSKind) || kindID[0] != CMSKind[0] || kindID[1] != CMSKind[1] {
		return nil, 0, 0, fmt.Errorf("asapmsgpack: kind_id %x is not Count-Min", kindID)
	}
	counterType, _, err := decodeCMSMetadata(metadata)
	if err != nil {
		return nil, 0, 0, err
	}
	if counterType != CMSCounterF64 && counterType != CMSCounterI64 {
		return nil, 0, 0, fmt.Errorf("asapmsgpack: unsupported counter_type %q", counterType)
	}

	d := newDecoder(payload)
	n, err := d.readArrayLen()
	if err != nil {
		return nil, 0, 0, err
	}
	if n != 3 {
		return nil, 0, 0, fmt.Errorf(
			"asapmsgpack: CountMinSketch expected 3-element array, got %d", n)
	}
	if rowNum, err = d.readUint(); err != nil {
		return nil, 0, 0, err
	}
	if colNum, err = d.readUint(); err != nil {
		return nil, 0, 0, err
	}
	countLen, err := d.readArrayLen()
	if err != nil {
		return nil, 0, 0, err
	}
	if uint64(countLen) != rowNum*colNum {
		return nil, 0, 0, fmt.Errorf(
			"asapmsgpack: counts length %d != rows*cols %d", countLen, rowNum*colNum)
	}
	matrix = make([][]float64, rowNum)
	for r := uint64(0); r < rowNum; r++ {
		row := make([]float64, colNum)
		for c := uint64(0); c < colNum; c++ {
			if counterType == CMSCounterF64 {
				if row[c], err = d.readFloat64(); err != nil {
					return nil, 0, 0, err
				}
			} else {
				var iv int64
				if iv, err = d.readInt(); err != nil {
					return nil, 0, 0, err
				}
				row[c] = float64(iv)
			}
		}
		matrix[r] = row
	}
	if err := d.done(); err != nil {
		return nil, 0, 0, err
	}
	return matrix, rowNum, colNum, nil
}
