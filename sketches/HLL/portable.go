package hll

import (
	"fmt"

	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
	"google.golang.org/protobuf/proto"
)

// SerializePortable serializes the DataFusion-style HyperLogLog as a SketchEnvelope.
func (h *HyperLogLog) SerializePortable() (*pb.SketchEnvelope, error) {
	regs := append([]byte(nil), h.Registers.AsSlice()...)
	state := &pb.HyperLogLogState{
		Variant:   pb.HLLVariant_HLL_VARIANT_DATAFUSION,
		Precision: HLLPrecision,
		Registers: regs,
	}
	return hllEnvelope(state), nil
}

// SerializePortable serializes a HyperLogLogVariant (Regular or DataFusion) as a SketchEnvelope.
func (h *HyperLogLogVariant) SerializePortable() (*pb.SketchEnvelope, error) {
	variant := pb.HLLVariant_HLL_VARIANT_REGULAR
	if h.Variant == HLLDataFusion {
		variant = pb.HLLVariant_HLL_VARIANT_DATAFUSION
	}
	regs := append([]byte(nil), h.Registers.AsSlice()...)
	state := &pb.HyperLogLogState{
		Variant:   variant,
		Precision: HLLPrecision,
		Registers: regs,
	}
	return hllEnvelope(state), nil
}

// SerializePortable serializes a HyperLogLogHIP as a SketchEnvelope.
func (h *HyperLogLogHIP) SerializePortable() (*pb.SketchEnvelope, error) {
	regs := append([]byte(nil), h.Registers.AsSlice()...)
	state := &pb.HyperLogLogState{
		Variant:   pb.HLLVariant_HLL_VARIANT_HIP,
		Precision: HLLPrecision,
		Registers: regs,
		HipKxq0:   h.kxq0,
		HipKxq1:   h.kxq1,
		HipEst:    h.est,
	}
	return hllEnvelope(state), nil
}

func hllEnvelope(state *pb.HyperLogLogState) *pb.SketchEnvelope {
	return &pb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &pb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &pb.SketchEnvelope_Hll{
			Hll: state,
		},
	}
}

// SerializeProtoBytes serializes the HyperLogLog as a proto-encoded SketchEnvelope.
func (h *HyperLogLog) SerializeProtoBytes() ([]byte, error) {
	env, err := h.SerializePortable()
	if err != nil {
		return nil, err
	}
	return proto.Marshal(env)
}

// DeserializeHyperLogLogFromProtoBytes restores a HyperLogLog from a proto-encoded SketchEnvelope.
func DeserializeHyperLogLogFromProtoBytes(data []byte) (*HyperLogLog, error) {
	var env pb.SketchEnvelope
	if err := proto.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	st := env.GetHll()
	if st == nil {
		return nil, fmt.Errorf("hll: proto envelope does not contain HyperLogLogState")
	}

	regs := append([]byte(nil), st.GetRegisters()...)
	if len(regs) != HLLRegisterCount {
		return nil, fmt.Errorf("hll: invalid register length %d, expected %d", len(regs), HLLRegisterCount)
	}

	return &HyperLogLog{
		Registers: storage.Vector1DFromSlice(regs),
	}, nil
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
