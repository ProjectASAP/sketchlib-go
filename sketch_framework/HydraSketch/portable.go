package hydrasketch

import (
	"fmt"

	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	hydrapb "github.com/ProjectASAP/sketchlib-go/proto/hydra"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
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

// cellToProto converts a HydraCounter into a HydraCell proto by delegating to
// the inner sketch's SerializePortable. The counter wrapper structs are in the
// same package so their private .s field is accessible.
func cellToProto(c HydraCounter) (*hydrapb.HydraCell, error) {
	switch ct := c.(type) {
	case *countMinCounter:
		env, err := ct.s.SerializePortable()
		if err != nil {
			return nil, err
		}
		return &hydrapb.HydraCell{
			Sketch: &hydrapb.HydraCell_CountMin{CountMin: env.GetCountMin()},
		}, nil

	case *countSketchCounter:
		env, err := ct.s.SerializePortable()
		if err != nil {
			return nil, err
		}
		return &hydrapb.HydraCell{
			Sketch: &hydrapb.HydraCell_CountSketch{CountSketch: env.GetCountSketch()},
		}, nil

	case *hllCounter:
		env, err := ct.s.SerializePortable()
		if err != nil {
			return nil, err
		}
		return &hydrapb.HydraCell{
			Sketch: &hydrapb.HydraCell_Hll{Hll: env.GetHll()},
		}, nil

	case *kllCounter:
		env, err := ct.s.SerializePortable()
		if err != nil {
			return nil, err
		}
		return &hydrapb.HydraCell{
			Sketch: &hydrapb.HydraCell_Kll{Kll: env.GetKll()},
		}, nil

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
