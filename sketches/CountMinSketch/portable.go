package countminsketch

import (
	"fmt"

	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	cmpb "github.com/ProjectASAP/sketchlib-go/proto/countminsketch"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
	"google.golang.org/protobuf/proto"
)

// SerializePortable serializes the CountMinSketch into a portable protobuf
// SketchEnvelope. An all-integral Count matrix is encoded as packed sint64
// (zigzag varint), 4–8× smaller than float64 for typical small-integer counter
// values; a matrix with ANY fractional cell (weighted values, or the per-row
// 1/p sampling weight) is encoded LOSSLESSLY as packed float64 (counts_float +
// CounterType FLOAT64) — truncating to int64 would bias every cell toward zero
// and desync the float64 L1/L2 sidecars.
// All three counter matrices (Count, Sum, Sum2) and both norm vectors are included.
func (s *CountMinSketch) SerializePortable() (*envpb.SketchEnvelope, error) {
	n := s.Rows * s.Cols
	countsInt, countsFloat := flattenCMSCounts(s.Count, s.Rows, s.Cols)
	sumFlat := make([]float64, 0, n)
	sum2Flat := make([]float64, 0, n)
	for r := 0; r < s.Rows; r++ {
		sumFlat = append(sumFlat, s.Sum[r]...)
		sum2Flat = append(sum2Flat, s.Sum2[r]...)
	}

	state := &cmpb.CountMinState{
		Rows:       uint32(s.Rows),
		Cols:       uint32(s.Cols),
		SumCounts:  sumFlat,
		Sum2Counts: sum2Flat,
		L1:         append([]float64(nil), s.L1...),
		L2:         append([]float64(nil), s.L2...),
	}
	if countsFloat != nil {
		state.CounterType = commonpb.CounterType_COUNTER_TYPE_FLOAT64
		state.CountsFloat = countsFloat
	} else {
		state.CounterType = commonpb.CounterType_COUNTER_TYPE_INT64
		state.CountsInt = countsInt
	}

	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer:      producerInfo(),
		HashSpec:      portableHashSpec(),
		SampleP:       s.wireSampleP(),
		SketchState:   &envpb.SketchEnvelope_CountMin{CountMin: state},
	}, nil
}

// SerializePortableFO (Frequency-Only) is like SerializePortable but omits
// Sum and Sum2. Use this when all insertions are unweighted (weight = 1) and
// the downstream only queries frequency. The receiver reconstructs
// Sum = Sum2 = Count on deserialization at zero cost.
//
// Reduces CMS payload by ~3× vs full serialization, compounded with the
// sint64 savings for a combined ~10–15× reduction over the legacy float64 format.
func (s *CountMinSketch) SerializePortableFO() (*envpb.SketchEnvelope, error) {
	countsInt, countsFloat := flattenCMSCounts(s.Count, s.Rows, s.Cols)

	state := &cmpb.CountMinState{
		Rows: uint32(s.Rows),
		Cols: uint32(s.Cols),
		// SumCounts and Sum2Counts deliberately omitted.
		// Receiver sets Sum = Sum2 = Count — valid for unweighted streams AND
		// for the per-row 1/p sampled path (all three arrays get the same
		// weight increments).
		L1: append([]float64(nil), s.L1...),
		L2: append([]float64(nil), s.L2...),
	}
	// Lossless float wire when any cell is fractional (see SerializePortable).
	if countsFloat != nil {
		state.CounterType = commonpb.CounterType_COUNTER_TYPE_FLOAT64
		state.CountsFloat = countsFloat
	} else {
		state.CounterType = commonpb.CounterType_COUNTER_TYPE_INT64
		state.CountsInt = countsInt
	}

	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer:      producerInfo(),
		HashSpec:      portableHashSpec(),
		SampleP:       s.wireSampleP(),
		SketchState:   &envpb.SketchEnvelope_CountMin{CountMin: state},
	}, nil
}

// SerializeProtoBytes serializes the CountMinSketch with all three counter
// arrays using sint64 encoding (Opt-2: ~4–8× smaller than float64).
func (s *CountMinSketch) SerializeProtoBytes() ([]byte, error) {
	env, err := s.SerializePortable()
	if err != nil {
		return nil, err
	}
	return proto.Marshal(env)
}

// SerializeProtoBytesFO serializes in Frequency-Only mode: sint64 Count only,
// no Sum/Sum2. For upstream nodes that do unweighted insertions and whose
// downstream only queries frequency (Opt-1 + Opt-2 combined).
func (s *CountMinSketch) SerializeProtoBytesFO() ([]byte, error) {
	env, err := s.SerializePortableFO()
	if err != nil {
		return nil, err
	}
	return proto.Marshal(env)
}

// DeserializeCountMinSketchFromProtoBytes restores a CountMinSketch from a
// proto-encoded SketchEnvelope. Supports:
//   - sint64 counts_int (new, preferred)
//   - float64 counts_float (legacy, backward-compatible)
//
// When Sum/Sum2 are absent (Frequency-Only payloads) they are reconstructed
// as Sum = Sum2 = Count, which is exact for unweighted streams.
func DeserializeCountMinSketchFromProtoBytes(data []byte) (*CountMinSketch, error) {
	var env envpb.SketchEnvelope
	if err := proto.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	st := env.GetCountMin()
	if st == nil {
		return nil, fmt.Errorf("countminsketch: proto envelope does not contain CountMinState")
	}

	rows := int(st.GetRows())
	cols := int(st.GetCols())
	if rows <= 0 || cols <= 0 {
		return nil, fmt.Errorf("countminsketch: invalid dimensions %d×%d", rows, cols)
	}
	expected := rows * cols

	// Decode Count: prefer sint64, fall back to float64 for older payloads.
	var flat []float64
	if intC := st.GetCountsInt(); len(intC) == expected {
		flat = int64ToFloat64(intC)
	} else if floatC := st.GetCountsFloat(); len(floatC) == expected {
		flat = floatC
	} else {
		return nil, fmt.Errorf("countminsketch: counts size mismatch (int=%d float=%d want=%d)",
			len(st.GetCountsInt()), len(st.GetCountsFloat()), expected)
	}

	// Decode Sum and Sum2; reconstruct from Count when absent (FO payload).
	sumFlat := copyOrFallback(st.GetSumCounts(), flat, expected)
	sum2Flat := copyOrFallback(st.GetSum2Counts(), flat, expected)

	count := make([][]float64, rows)
	sumC := make([][]float64, rows)
	sum2C := make([][]float64, rows)
	for r := 0; r < rows; r++ {
		count[r] = append([]float64(nil), flat[r*cols:(r+1)*cols]...)
		sumC[r] = append([]float64(nil), sumFlat[r*cols:(r+1)*cols]...)
		sum2C[r] = append([]float64(nil), sum2Flat[r*cols:(r+1)*cols]...)
	}

	s := &CountMinSketch{
		Rows:  rows,
		Cols:  cols,
		Count: count,
		Sum:   sumC,
		Sum2:  sum2C,
		L1:    append([]float64(nil), st.GetL1()...),
		L2:    append([]float64(nil), st.GetL2()...),
	}
	if err := s.rehydrateStorage(); err != nil {
		return nil, err
	}
	return s, nil
}

// copyOrFallback returns src when len(src) == expected, otherwise returns a
// copy of fallback. Used to handle absent Sum/Sum2 in Frequency-Only payloads.
func copyOrFallback(src, fallback []float64, expected int) []float64 {
	if len(src) == expected {
		return src
	}
	out := make([]float64, expected)
	copy(out, fallback)
	return out
}

// int64ToFloat64 converts []int64 → []float64.
func int64ToFloat64(src []int64) []float64 {
	out := make([]float64, len(src))
	for i, v := range src {
		out[i] = float64(v)
	}
	return out
}

func producerInfo() *commonpb.ProducerInfo {
	return &commonpb.ProducerInfo{Library: "sketchlib-go", Version: "0.1.0"}
}

// portableHashSpec returns the standard HashSpec for sketchlib-go.
func portableHashSpec() *commonpb.HashSpec {
	return &commonpb.HashSpec{
		Algorithm:          commonpb.HashAlgorithm_HASH_ALGORITHM_XXH3_64,
		CanonicalSeedIndex: 5,
		SeedList: []uint64{
			0xcafe3553, 0xade3415118, 0x8cc70208, 0x2f024b2b, 0x451a3df5,
			0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f,
			0x9b05688c, 0x1f83d9ab, 0x5be0cd19, 0xcbbb9d5d, 0x629a292a,
			0x9159015a, 0x152fecd8, 0x67332667, 0x8eb44a87, 0xdb0c2e0d,
		},
		SeedDerivation: commonpb.SeedDerivation_SEED_DERIVATION_PACKED,
	}
}

// flattenCMSCounts flattens the Count matrix for the wire. If every cell is
// integral it returns (ints, nil) — the compact sint64 encoding. If any cell is
// fractional it returns (nil, floats) — the lossless float64 encoding.
func flattenCMSCounts(count [][]float64, rows, cols int) ([]int64, []float64) {
	n := rows * cols
	ints := make([]int64, 0, n)
	for r := 0; r < rows; r++ {
		for _, v := range count[r] {
			iv, ok := integralCellDelta(v)
			if !ok {
				floats := make([]float64, 0, n)
				for r2 := 0; r2 < rows; r2++ {
					floats = append(floats, count[r2]...)
				}
				return nil, floats
			}
			ints = append(ints, iv)
		}
	}
	return ints, nil
}
