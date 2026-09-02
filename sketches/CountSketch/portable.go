package countsketch

import (
	"fmt"

	"github.com/ProjectASAP/sketchlib-go/common"
	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	cspb "github.com/ProjectASAP/sketchlib-go/proto/countsketch"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
	"google.golang.org/protobuf/proto"
)

// SerializePortable serializes the CountSketch into a portable protobuf SketchEnvelope.
// Opt-2: an all-integral counter matrix is encoded as packed sint64 (zigzag
// varint), giving 4–8× size reduction over float64 for typical small-integer
// counter values. A matrix with ANY fractional cell (weighted values, or the
// per-row 1/p sampling weight) is encoded LOSSLESSLY as packed float64
// (counts_float + CounterType FLOAT64) instead — truncating to int64 would bias
// every cell toward zero and desync the float64 L2 sidecar.
// Opt-4: heavy-hitter candidates are serialized as plain string keys from the
// Space-Saving tracker; downstream queries the merged CS for accurate counts.
func (s *CountSketch) SerializePortable() (*envpb.SketchEnvelope, error) {
	countsInt, countsFloat := flattenCounts(s.Count, s.Rows, s.Cols)

	l2 := append([]float64(nil), s.L2...)

	// Opt-4: emit candidate key strings from Space-Saving tracker.
	// No counts forwarded — downstream queries the merged CS matrix.
	var hhKeys []string
	if s.SS != nil && s.SS.Len() > 0 {
		hhKeys = s.SS.Candidates()
	}

	state := &cspb.CountSketchState{
		Rows:   uint32(s.Rows),
		Cols:   uint32(s.Cols),
		L2:     l2,
		HhKeys: hhKeys,
		// Topk intentionally omitted (stale upstream-local counts replaced by hh_keys).
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
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &envpb.SketchEnvelope_CountSketch{
			CountSketch: state,
		},
	}, nil
}

// flattenCounts flattens a rows×cols count matrix for the wire. If every cell
// is integral it returns (ints, nil) — the compact sint64 encoding. If any cell
// is fractional it returns (nil, floats) — the lossless float64 encoding.
func flattenCounts(count [][]float64, rows, cols int) ([]int64, []float64) {
	n := rows * cols
	ints := make([]int64, 0, n)
	for r := 0; r < rows; r++ {
		for _, v := range count[r] {
			iv, ok := integralCellDelta(v)
			if !ok {
				// Fractional cell → restart as float64 (rare path, one rescan).
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

// SerializeProtoBytes serializes the CountSketch as a proto-encoded SketchEnvelope.
func (s *CountSketch) SerializeProtoBytes() ([]byte, error) {
	env, err := s.SerializePortable()
	if err != nil {
		return nil, err
	}
	return proto.Marshal(env)
}

// DeserializeCountSketchFromProtoBytes restores a CountSketch from a proto-encoded
// SketchEnvelope. Supports:
//   - sint64 counts_int (new, preferred — Opt-2)
//   - float64 counts_float (legacy, backward-compatible)
//
// Heavy-hitter candidates from hh_keys are used to rebuild the TopK heap by
// querying the restored CS matrix (globally-merged estimates). Legacy topk
// entries are used as fallback when hh_keys is absent.
func DeserializeCountSketchFromProtoBytes(data []byte) (*CountSketch, error) {
	var env envpb.SketchEnvelope
	if err := proto.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	st := env.GetCountSketch()
	if st == nil {
		return nil, fmt.Errorf("countsketch: proto envelope does not contain CountSketchState")
	}

	rows := int(st.GetRows())
	cols := int(st.GetCols())
	if rows <= 0 || cols <= 0 {
		return nil, fmt.Errorf("countsketch: invalid dimensions %d×%d", rows, cols)
	}
	expected := rows * cols

	// Decode Count: prefer sint64, fall back to float64 for older payloads.
	var flat []float64
	if intC := st.GetCountsInt(); len(intC) == expected {
		flat = csInt64ToFloat64(intC)
	} else if floatC := st.GetCountsFloat(); len(floatC) == expected {
		flat = floatC
	} else {
		return nil, fmt.Errorf("countsketch: counts size mismatch (int=%d float=%d want=%d)",
			len(st.GetCountsInt()), len(st.GetCountsFloat()), expected)
	}

	count := make([][]float64, rows)
	for r := 0; r < rows; r++ {
		count[r] = append([]float64(nil), flat[r*cols:(r+1)*cols]...)
	}

	l2 := append([]float64(nil), st.GetL2()...)

	cs := &CountSketch{
		Rows:  rows,
		Cols:  cols,
		Count: count,
		L2:    l2,
	}
	if err := cs.rehydrateStorage(); err != nil {
		return nil, err
	}

	// Rebuild TopK: prefer hh_keys (Opt-4) over legacy topk entries.
	if hhKeys := st.GetHhKeys(); len(hhKeys) > 0 {
		for _, key := range hhKeys {
			est, _ := cs.QueryWithHash(common.QueryFrequency, common.Hash64([]byte(key)))
			cs.TopK.Update(key, int64(est))
		}
	} else if pbTopK := st.GetTopk(); pbTopK != nil && len(pbTopK.GetEntries()) > 0 {
		// Legacy path: restore from old-style topk entries (stale upstream counts).
		k := int(pbTopK.GetK())
		if k <= 0 {
			k = TOPK_SIZE
		}
		cs.TopK = common.NewTopKHeap(k)
		for _, e := range pbTopK.GetEntries() {
			cs.TopK.Update(e.GetKey(), int64(e.GetCount()))
		}
	}

	return cs, nil
}

// csInt64ToFloat64 converts []int64 → []float64.
func csInt64ToFloat64(src []int64) []float64 {
	out := make([]float64, len(src))
	for i, v := range src {
		out[i] = float64(v)
	}
	return out
}

func portableHashSpec() *commonpb.HashSpec {
	return &commonpb.HashSpec{
		Algorithm:          commonpb.HashAlgorithm_HASH_ALGORITHM_XXH3_64,
		CanonicalSeedIndex: uint32(common.CanonicalHashSeed),
		SeedList: []uint64{
			0xcafe3553, 0xade3415118, 0x8cc70208, 0x2f024b2b, 0x451a3df5,
			0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f,
			0x9b05688c, 0x1f83d9ab, 0x5be0cd19, 0xcbbb9d5d, 0x629a292a,
			0x9159015a, 0x152fecd8, 0x67332667, 0x8eb44a87, 0xdb0c2e0d,
		},
		SeedDerivation: commonpb.SeedDerivation_SEED_DERIVATION_PACKED,
	}
}
