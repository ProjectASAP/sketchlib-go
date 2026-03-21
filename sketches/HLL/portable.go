package hll

import (
	"fmt"

	"github.com/ProjectASAP/sketchlib-go/common/storage"
	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	hllpb "github.com/ProjectASAP/sketchlib-go/proto/hll"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
	"google.golang.org/protobuf/proto"
)

// SerializePortable serializes the DataFusion-style HyperLogLog as a SketchEnvelope.
func (h *HyperLogLog) SerializePortable() (*envpb.SketchEnvelope, error) {
	regs := append([]byte(nil), h.Registers.AsSlice()...)
	state := &hllpb.HyperLogLogState{
		Variant:   hllpb.HLLVariant_HLL_VARIANT_DATAFUSION,
		Precision: HLLPrecision,
		Registers: regs,
	}
	return hllEnvelope(state), nil
}

// SerializePortable serializes a HyperLogLogVariant (Regular or DataFusion) as a SketchEnvelope.
func (h *HyperLogLogVariant) SerializePortable() (*envpb.SketchEnvelope, error) {
	variant := hllpb.HLLVariant_HLL_VARIANT_REGULAR
	if h.Variant == HLLDataFusion {
		variant = hllpb.HLLVariant_HLL_VARIANT_DATAFUSION
	}
	regs := append([]byte(nil), h.Registers.AsSlice()...)
	state := &hllpb.HyperLogLogState{
		Variant:   variant,
		Precision: HLLPrecision,
		Registers: regs,
	}
	return hllEnvelope(state), nil
}

// SerializePortable serializes a HyperLogLogHIP as a SketchEnvelope.
func (h *HyperLogLogHIP) SerializePortable() (*envpb.SketchEnvelope, error) {
	regs := append([]byte(nil), h.Registers.AsSlice()...)
	state := &hllpb.HyperLogLogState{
		Variant:   hllpb.HLLVariant_HLL_VARIANT_HIP,
		Precision: HLLPrecision,
		Registers: regs,
		HipKxq0:   h.kxq0,
		HipKxq1:   h.kxq1,
		HipEst:    h.est,
	}
	return hllEnvelope(state), nil
}

func hllEnvelope(state *hllpb.HyperLogLogState) *envpb.SketchEnvelope {
	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &envpb.SketchEnvelope_Hll{
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
	var env envpb.SketchEnvelope
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
