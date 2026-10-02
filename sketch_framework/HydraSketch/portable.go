package hydrasketch

import (
	"errors"
	"fmt"
	"math"

	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	cmpb "github.com/ProjectASAP/sketchlib-go/proto/countminsketch"
	cspb "github.com/ProjectASAP/sketchlib-go/proto/countsketch"
	hydrapb "github.com/ProjectASAP/sketchlib-go/proto/hydra"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
)

// SerializePortable serializes the Hydra sketch into a portable protobuf SketchEnvelope.
// Each cell is serialized according to its concrete counter type.
func (h *Hydra) SerializePortable() (*envpb.SketchEnvelope, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.cells) == 0 {
		return nil, fmt.Errorf("hydra: no cells to serialize")
	}

	counterType := h.cells[0].CounterType()
	protoCounterType := hydraCounterTypeToProto(counterType)

	cells := make([]*hydrapb.HydraCell, len(h.cells))
	for i, c := range h.cells {
		cell, err := cellToProto(c)
		if err != nil {
			return nil, fmt.Errorf("hydra cell[%d]: %w", i, err)
		}
		cells[i] = cell
	}

	var bigCell *hydrapb.HydraCell
	if h.bigCounter != nil {
		var err error
		bigCell, err = cellToProto(h.bigCounter)
		if err != nil {
			return nil, fmt.Errorf("hydra big_counter: %w", err)
		}
	}

	state := &hydrapb.HydraState{
		RowNum:        uint32(h.D),
		ColNum:        uint32(h.W),
		CounterType:   protoCounterType,
		Cells:         cells,
		BigCounter:    bigCell,
		SeedIndex:     uint32(h.seedHydra),
		EnableTopk:    h.enableTopK,
		FanoutSubkeys: h.fanoutSubkeys,
	}

	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &envpb.SketchEnvelope_Hydra{
			Hydra: state,
		},
	}, nil
}

func hydraCounterTypeToProto(ct HydraCounterType) hydrapb.HydraCounterType {
	switch ct {
	case HydraCounterCM:
		return hydrapb.HydraCounterType_HYDRA_COUNTER_TYPE_COUNT_MIN
	case HydraCounterCS:
		return hydrapb.HydraCounterType_HYDRA_COUNTER_TYPE_COUNT_SKETCH
	case HydraCounterHLL:
		return hydrapb.HydraCounterType_HYDRA_COUNTER_TYPE_HLL
	case HydraCounterKLL:
		return hydrapb.HydraCounterType_HYDRA_COUNTER_TYPE_KLL
	case HydraCounterUniversal:
		return hydrapb.HydraCounterType_HYDRA_COUNTER_TYPE_UNIVMON
	default:
		return hydrapb.HydraCounterType_HYDRA_COUNTER_TYPE_UNSPECIFIED
	}
}

// countMinCellState returns a Count-Min cell as a CountMinState, with integer
// counts when every count is integral and float counts otherwise.
func countMinCellState(s *countminsketch.CountMinSketch) *cmpb.CountMinState {
	st := &cmpb.CountMinState{Rows: uint32(s.Rows), Cols: uint32(s.Cols), L1: append([]float64(nil), s.L1...)}
	var counts []float64
	for r := range s.Rows {
		counts = append(counts, s.Count[r]...)
		st.SumCounts = append(st.SumCounts, s.Sum[r]...)
		st.Sum2Counts = append(st.Sum2Counts, s.Sum2[r]...)
	}
	ints := make([]int64, len(counts))
	for i, v := range counts {
		if v != math.Trunc(v) || v >= 1<<63 || v < -(1<<63) {
			st.CounterType = commonpb.CounterType_COUNTER_TYPE_FLOAT64
			st.CountsFloat = counts
			return st
		}
		ints[i] = int64(v)
	}
	st.CounterType = commonpb.CounterType_COUNTER_TYPE_INT64
	st.CountsInt = ints
	return st
}

// cellToProto converts a HydraCounter into a HydraCell proto: Count-Min and
// Count Sketch cells through countMinCellState and countSketchState, every
// other cell through the inner sketch's SerializePortable.
func cellToProto(c HydraCounter) (*hydrapb.HydraCell, error) {
	switch ct := c.(type) {
	case *countMinCounter:
		return &hydrapb.HydraCell{
			Sketch: &hydrapb.HydraCell_CountMin{CountMin: countMinCellState(ct.s)},
		}, nil

	case *countSketchCounter:
		return &hydrapb.HydraCell{
			Sketch: &hydrapb.HydraCell_CountSketch{CountSketch: countSketchState(ct.s)},
		}, nil

	case *hllCounter:
		return nil, errors.New("hydra: HLL cells have no protobuf encoding")

	case *kllCounter:
		return nil, fmt.Errorf("KLL cells have no protobuf encoding")

	case *univCounter:
		env, err := ct.s.SerializePortable()
		if err != nil {
			return nil, err
		}
		return &hydrapb.HydraCell{
			Sketch: &hydrapb.HydraCell_Univmon{Univmon: env.GetUnivmon()},
		}, nil

	default:
		return nil, fmt.Errorf("unknown HydraCounter type %T", c)
	}
}

// countSketchState flattens a Count Sketch cell row-major: sint64 counters
// when every cell is an integer in int64 range, float64 counters otherwise.
func countSketchState(s *countsketch.CountSketch) *cspb.CountSketchState {
	st := &cspb.CountSketchState{
		Rows:        uint32(s.Rows),
		Cols:        uint32(s.Cols),
		CounterType: commonpb.CounterType_COUNTER_TYPE_INT64,
		L2:          append([]float64(nil), s.L2...),
	}
	for _, row := range s.Count[:s.Rows] {
		for _, v := range row[:s.Cols] {
			if v != math.Trunc(v) || v < math.MinInt64 || v >= math.MaxInt64 {
				st.CounterType = commonpb.CounterType_COUNTER_TYPE_FLOAT64
			}
			st.CountsFloat = append(st.CountsFloat, v)
		}
	}
	if st.CounterType == commonpb.CounterType_COUNTER_TYPE_INT64 {
		st.CountsInt = make([]int64, len(st.CountsFloat))
		for i, v := range st.CountsFloat {
			st.CountsInt[i] = int64(v)
		}
		st.CountsFloat = nil
	}
	if s.SS != nil && s.SS.Len() > 0 {
		st.HhKeys = s.SS.Candidates()
	}
	return st
}

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
