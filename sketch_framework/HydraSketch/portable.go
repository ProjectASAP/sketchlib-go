package hydrasketch

import (
	"fmt"

	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
)

// SerializePortable serializes the Hydra sketch into a portable protobuf SketchEnvelope.
// Each cell is serialized according to its concrete counter type.
func (h *Hydra) SerializePortable() (*pb.SketchEnvelope, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.cells) == 0 {
		return nil, fmt.Errorf("hydra: no cells to serialize")
	}

	counterType := h.cells[0].CounterType()
	protoCounterType := hydraCounterTypeToProto(counterType)

	cells := make([]*pb.HydraCell, len(h.cells))
	for i, c := range h.cells {
		cell, err := cellToProto(c)
		if err != nil {
			return nil, fmt.Errorf("hydra cell[%d]: %w", i, err)
		}
		cells[i] = cell
	}

	var bigCell *pb.HydraCell
	if h.bigCounter != nil {
		var err error
		bigCell, err = cellToProto(h.bigCounter)
		if err != nil {
			return nil, fmt.Errorf("hydra big_counter: %w", err)
		}
	}

	state := &pb.HydraState{
		RowNum:        uint32(h.D),
		ColNum:        uint32(h.W),
		CounterType:   protoCounterType,
		Cells:         cells,
		BigCounter:    bigCell,
		SeedIndex:     uint32(h.seedHydra),
		EnableTopk:    h.enableTopK,
		FanoutSubkeys: h.fanoutSubkeys,
	}

	return &pb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &pb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &pb.SketchEnvelope_Hydra{
			Hydra: state,
		},
	}, nil
}

func hydraCounterTypeToProto(ct HydraCounterType) pb.HydraCounterType {
	switch ct {
	case HydraCounterCM:
		return pb.HydraCounterType_HYDRA_COUNTER_TYPE_COUNT_MIN
	case HydraCounterCS:
		return pb.HydraCounterType_HYDRA_COUNTER_TYPE_COUNT_SKETCH
	case HydraCounterHLL:
		return pb.HydraCounterType_HYDRA_COUNTER_TYPE_HLL
	case HydraCounterKLL:
		return pb.HydraCounterType_HYDRA_COUNTER_TYPE_KLL
	case HydraCounterUniversal:
		return pb.HydraCounterType_HYDRA_COUNTER_TYPE_UNIVMON
	default:
		return pb.HydraCounterType_HYDRA_COUNTER_TYPE_UNSPECIFIED
	}
}

// cellToProto converts a HydraCounter into a HydraCell proto by delegating to
// the inner sketch's SerializePortable. The counter wrapper structs are in the
// same package so their private .s field is accessible.
func cellToProto(c HydraCounter) (*pb.HydraCell, error) {
	switch ct := c.(type) {
	case *countMinCounter:
		env, err := ct.s.SerializePortable()
		if err != nil {
			return nil, err
		}
		return &pb.HydraCell{
			Sketch: &pb.HydraCell_CountMin{CountMin: env.GetCountMin()},
		}, nil

	case *countSketchCounter:
		env, err := ct.s.SerializePortable()
		if err != nil {
			return nil, err
		}
		return &pb.HydraCell{
			Sketch: &pb.HydraCell_CountSketch{CountSketch: env.GetCountSketch()},
		}, nil

	case *hllCounter:
		env, err := ct.s.SerializePortable()
		if err != nil {
			return nil, err
		}
		return &pb.HydraCell{
			Sketch: &pb.HydraCell_Hll{Hll: env.GetHll()},
		}, nil

	case *kllCounter:
		env, err := ct.s.SerializePortable()
		if err != nil {
			return nil, err
		}
		return &pb.HydraCell{
			Sketch: &pb.HydraCell_Kll{Kll: env.GetKll()},
		}, nil

	case *univCounter:
		env, err := ct.s.SerializePortable()
		if err != nil {
			return nil, err
		}
		return &pb.HydraCell{
			Sketch: &pb.HydraCell_Univmon{Univmon: env.GetUnivmon()},
		}, nil

	default:
		return nil, fmt.Errorf("unknown HydraCounter type %T", c)
	}
}

func portableHashSpec() *pb.HashSpec {
	return &pb.HashSpec{
		Algorithm:          pb.HashAlgorithm_HASH_ALGORITHM_XXH3_64,
		CanonicalSeedIndex: 5,
		SeedList: []uint64{
			0xcafe3553, 0xade3415118, 0x8cc70208, 0x2f024b2b, 0x451a3df5,
			0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f,
			0x9b05688c, 0x1f83d9ab, 0x5be0cd19, 0xcbbb9d5d, 0x629a292a,
			0x9159015a, 0x152fecd8, 0x67332667, 0x8eb44a87, 0xdb0c2e0d,
		},
		SeedDerivation: pb.SeedDerivation_SEED_DERIVATION_PACKED,
	}
}
