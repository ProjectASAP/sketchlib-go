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
//	[ counts:array<f64> ]   // counts packed row-major
//
// The matrix dimensions (rows/cols), counter_type and mode all live in the
// metadata, not the payload. Non-rectangular matrices are rejected before any
// bytes are emitted.
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
	e.writeArrayLen(1)
	e.writeArrayLen(int(rowNum * colNum))
	for _, row := range matrix {
		for _, v := range row {
			e.writeFloat64(v)
		}
	}

	metadata := encodeCMSMetadata(uint32(rowNum), uint32(colNum), CMSCounterF64, CMSModeFast)
	return EncodeWrapper(CMSKind, metadata, e.bytes()), nil
}

// CountMinWireState is a fully-specified Count-Min wire state: the two
// structural params (counter type + mode) plus the row-major counter matrix in
// exactly one of the two counter representations. It lets a caller build any
// wire-eligible Count-Min config (i64/f64 × regular/fast) directly from known
// state — the path the cross-language golden parity test exercises for configs
// (e.g. i64/regular) the higher-level MarshalCountMinSketch does not cover.
type CountMinWireState struct {
	Rows uint64
	Cols uint64
	// CounterType is CMSCounterI64 or CMSCounterF64.
	CounterType string
	// Mode is CMSModeRegular or CMSModeFast.
	Mode string
	// CountsI64 is the row-major counter matrix; used iff CounterType is
	// CMSCounterI64. Length MUST equal Rows*Cols.
	CountsI64 []int64
	// CountsF64 is the row-major counter matrix; used iff CounterType is
	// CMSCounterF64. Length MUST equal Rows*Cols.
	CountsF64 []float64
}

// MarshalCountMinSketchState encodes a fully-specified Count-Min wire state into
// a complete ASAPv1 envelope. The payload is the positional array [ counts:array ];
// the matrix dimensions (rows/cols), counter_type and mode all live in the
// metadata. Integer counters are written with rmp_serde's family/width rule
// (non-negative → uint family minimal width, negative → int family), matching
// the Rust reference encoder.
func MarshalCountMinSketchState(s CountMinWireState) ([]byte, error) {
	if s.Mode != CMSModeRegular && s.Mode != CMSModeFast {
		return nil, fmt.Errorf("asapmsgpack: unsupported CMS mode %q", s.Mode)
	}
	n := s.Rows * s.Cols

	e := newEncoder()
	e.writeArrayLen(1)
	e.writeArrayLen(int(n))
	switch s.CounterType {
	case CMSCounterI64:
		if uint64(len(s.CountsI64)) != n {
			return nil, fmt.Errorf(
				"asapmsgpack: CountMin i64 counts length %d != rows*cols %d",
				len(s.CountsI64), n)
		}
		for _, v := range s.CountsI64 {
			e.writeInt(v)
		}
	case CMSCounterF64:
		if uint64(len(s.CountsF64)) != n {
			return nil, fmt.Errorf(
				"asapmsgpack: CountMin f64 counts length %d != rows*cols %d",
				len(s.CountsF64), n)
		}
		for _, v := range s.CountsF64 {
			e.writeFloat64(v)
		}
	default:
		return nil, fmt.Errorf("asapmsgpack: unsupported counter_type %q", s.CounterType)
	}

	metadata := encodeCMSMetadata(uint32(s.Rows), uint32(s.Cols), s.CounterType, s.Mode)
	return EncodeWrapper(CMSKind, metadata, e.bytes()), nil
}

// UnmarshalCountMinSketchState is the inverse of MarshalCountMinSketchState: it
// preserves the counter type and mode (rather than widening i64 to float64) so a
// decode→re-encode round-trip is byte-exact. Fails closed on any envelope,
// metadata or payload inconsistency.
func UnmarshalCountMinSketchState(buf []byte) (CountMinWireState, error) {
	var s CountMinWireState

	kindID, metadata, payload, err := SplitWrapper(buf)
	if err != nil {
		return s, err
	}
	if len(kindID) != len(CMSKind) || kindID[0] != CMSKind[0] || kindID[1] != CMSKind[1] {
		return s, fmt.Errorf("asapmsgpack: kind_id %x is not Count-Min", kindID)
	}
	rows, cols, counterType, mode, err := decodeCMSMetadata(metadata)
	if err != nil {
		return s, err
	}
	if counterType != CMSCounterI64 && counterType != CMSCounterF64 {
		return s, fmt.Errorf("asapmsgpack: unsupported counter_type %q", counterType)
	}
	s.Rows = uint64(rows)
	s.Cols = uint64(cols)
	s.CounterType = counterType
	s.Mode = mode

	d := newDecoder(payload)
	n, err := d.readArrayLen()
	if err != nil {
		return s, err
	}
	if n != 1 {
		return s, fmt.Errorf("asapmsgpack: CountMinSketch expected 1-element array, got %d", n)
	}
	countLen, err := d.readArrayLen()
	if err != nil {
		return s, err
	}
	if uint64(countLen) != s.Rows*s.Cols {
		return s, fmt.Errorf(
			"asapmsgpack: counts length %d != rows*cols %d", countLen, s.Rows*s.Cols)
	}
	if counterType == CMSCounterI64 {
		s.CountsI64 = make([]int64, countLen)
		for i := 0; i < countLen; i++ {
			if s.CountsI64[i], err = d.readInt(); err != nil {
				return s, err
			}
		}
	} else {
		s.CountsF64 = make([]float64, countLen)
		for i := 0; i < countLen; i++ {
			if s.CountsF64[i], err = d.readFloat64(); err != nil {
				return s, err
			}
		}
	}
	if err := d.done(); err != nil {
		return s, err
	}
	return s, nil
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
	rows, cols, counterType, _, err := decodeCMSMetadata(metadata)
	if err != nil {
		return nil, 0, 0, err
	}
	if counterType != CMSCounterF64 && counterType != CMSCounterI64 {
		return nil, 0, 0, fmt.Errorf("asapmsgpack: unsupported counter_type %q", counterType)
	}
	rowNum, colNum = uint64(rows), uint64(cols)

	d := newDecoder(payload)
	n, err := d.readArrayLen()
	if err != nil {
		return nil, 0, 0, err
	}
	if n != 1 {
		return nil, 0, 0, fmt.Errorf(
			"asapmsgpack: CountMinSketch expected 1-element array, got %d", n)
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
