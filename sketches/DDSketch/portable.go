package ddsketch

import (
	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	ddpb "github.com/ProjectASAP/sketchlib-go/proto/ddsketch"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
)

// SerializePortable serializes the DDSketch into a portable protobuf SketchEnvelope.
//
// Only alpha is transmitted; the consumer recomputes gamma and invLogGamma:
//
//	alpha = (gamma - 1) / (gamma + 1)
//	gamma = (1 + alpha) / (1 - alpha)
func (d *DDSketch) SerializePortable() (*envpb.SketchEnvelope, error) {
	// Derive alpha from the stored gamma.
	gamma := d.mapping.gamma
	alpha := (gamma - 1.0) / (gamma + 1.0)

	var storeCounts []uint64
	if d.store.counts != nil {
		storeCounts = append([]uint64(nil), d.store.counts.AsSlice()...)
	}

	state := &ddpb.DDSketchState{
		Alpha:       alpha,
		StoreCounts: storeCounts,
		StoreOffset: d.store.offset,
		Count:       d.count,
		Sum:         d.sum,
		Min:         d.min,
		Max:         d.max,
	}

	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &envpb.SketchEnvelope_Ddsketch{
			Ddsketch: state,
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
