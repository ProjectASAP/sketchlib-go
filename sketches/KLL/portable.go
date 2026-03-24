package kll

import (
	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	kllpb "github.com/ProjectASAP/sketchlib-go/proto/kll"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
)

// SerializePortable serializes the KLLSketch into a portable protobuf SketchEnvelope.
func (s *KLLSketch) SerializePortable() (*envpb.SketchEnvelope, error) {
	// Convert []int levels to []uint32.
	levels := make([]uint32, len(s.levels))
	for i, v := range s.levels {
		levels[i] = uint32(v)
	}

	items := append([]float64(nil), s.items...)

	state := &kllpb.KLLState{
		K:         uint32(s.k),
		M:         uint32(s.m),
		NumLevels: uint32(s.numLevels),
		Levels:    levels,
		Items:     items,
		Coin: &kllpb.CoinState{
			State:         s.co.state,
			BitCache:      s.co.bitCache,
			RemainingBits: uint32(s.co.remainingBits),
		},
	}

	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &envpb.SketchEnvelope_Kll{
			Kll: state,
		},
	}, nil
}

// portableHashSpec returns the standard HashSpec for sketchlib-go.
func portableHashSpec() *commonpb.HashSpec {
	return &commonpb.HashSpec{
		Algorithm:          commonpb.HashAlgorithm_HASH_ALGORITHM_XXH3_64,
		CanonicalSeedIndex: 5,
		SeedList: []uint64{
			0xcafe3553,
			0xade3415118,
			0x8cc70208,
			0x2f024b2b,
			0x451a3df5,
			0x6a09e667,
			0xbb67ae85,
			0x3c6ef372,
			0xa54ff53a,
			0x510e527f,
			0x9b05688c,
			0x1f83d9ab,
			0x5be0cd19,
			0xcbbb9d5d,
			0x629a292a,
			0x9159015a,
			0x152fecd8,
			0x67332667,
			0x8eb44a87,
			0xdb0c2e0d,
		},
		SeedDerivation: commonpb.SeedDerivation_SEED_DERIVATION_PACKED,
	}
}
