package hll

import (
	"fmt"

	"github.com/ProjectASAP/sketchlib-go/common/storage"
	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	hllpb "github.com/ProjectASAP/sketchlib-go/proto/hll"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
	"google.golang.org/protobuf/proto"
)

// setStateRegisters fills st with either the SPARSE (registers_sparse, tag 7)
// or the DENSE (registers, tag 3) encoding of regs, chosen by the
// SparseCrossoverNonZero threshold. Both representations reconstruct the same
// dense register array; emitting sparse only below the crossover keeps the
// payload small for low/medium cardinality without ever inflating it above it.
//
// Rollout note: this is safe to enable only once every consumer's decoder can
// read tag 7 (backend-decode-first) — see PR body. The proto field is additive
// so the wire format itself stays backward-compatible regardless.
func setStateRegisters(st *hllpb.HyperLogLogState, regs []uint8) {
	if countNonZero(regs) < SparseCrossoverNonZero {
		st.RegistersSparse = encodeSparseRegisters(regs)
		return
	}
	st.Registers = append([]byte(nil), regs...)
}

// SerializePortable serializes the DataFusion-style HyperLogLog as a SketchEnvelope.
func (h *HyperLogLog) SerializePortable() (*envpb.SketchEnvelope, error) {
	state := &hllpb.HyperLogLogState{
		Variant:   hllpb.HLLVariant_HLL_VARIANT_DATAFUSION,
		Precision: HLLPrecision,
	}
	// RegisterSlice() materialises the dense array for a sparse instance without
	// promoting, so the emitted proto is byte-identical to a dense instance with
	// the same registers (the existing encoder + cross-language wire format are
	// untouched).
	setStateRegisters(state, h.RegisterSlice())
	return hllEnvelope(state, h.wireSampleP()), nil
}

// SerializePortable serializes a HyperLogLogVariant (Regular or DataFusion) as a SketchEnvelope.
func (h *HyperLogLogVariant) SerializePortable() (*envpb.SketchEnvelope, error) {
	variant := hllpb.HLLVariant_HLL_VARIANT_REGULAR
	if h.Variant == HLLDataFusion {
		variant = hllpb.HLLVariant_HLL_VARIANT_DATAFUSION
	}
	state := &hllpb.HyperLogLogState{
		Variant:   variant,
		Precision: HLLPrecision,
	}
	setStateRegisters(state, h.Registers.AsSlice())
	return hllEnvelope(state, 0.0), nil
}

// SerializePortable serializes a HyperLogLogHIP as a SketchEnvelope.
func (h *HyperLogLogHIP) SerializePortable() (*envpb.SketchEnvelope, error) {
	state := &hllpb.HyperLogLogState{
		Variant:   hllpb.HLLVariant_HLL_VARIANT_HIP,
		Precision: HLLPrecision,
		HipKxq0:   h.kxq0,
		HipKxq1:   h.kxq1,
		HipEst:    h.est,
	}
	setStateRegisters(state, h.Registers.AsSlice())
	return hllEnvelope(state, 0.0), nil
}

// hllEnvelope wraps an HLL state in a SketchEnvelope. sampleP is stamped on the
// envelope's sample_p field (0.0 = unset = exact, byte-identical to the
// pre-sampling format).
func hllEnvelope(state *hllpb.HyperLogLogState, sampleP float64) *envpb.SketchEnvelope {
	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SampleP:  sampleP,
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

	regs, err := registersFromState(st)
	if err != nil {
		return nil, err
	}
	if len(regs) != HLLRegisterCount {
		return nil, fmt.Errorf("hll: invalid register length %d, expected %d", len(regs), HLLRegisterCount)
	}

	return &HyperLogLog{
		Registers: storage.Vector1DFromSlice(regs),
	}, nil
}

// registersFromState dual-reads the register array from a HyperLogLogState,
// accepting BOTH wire encodings. The SPARSE field (registers_sparse, tag 7)
// takes priority when present; otherwise the DENSE field (registers, tag 3) is
// used. Both reconstruct the identical dense register array.
func registersFromState(st *hllpb.HyperLogLogState) ([]uint8, error) {
	if sp := st.GetRegistersSparse(); sp != nil {
		return decodeSparseRegisters(sp)
	}
	return append([]byte(nil), st.GetRegisters()...), nil
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
