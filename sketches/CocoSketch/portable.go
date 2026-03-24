package cocosketch

import (
	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	cocopb "github.com/ProjectASAP/sketchlib-go/proto/cocosketch"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
)

// SerializePortable serializes the CocoSketch into a portable protobuf SketchEnvelope.
// The bucket table is stored flat in row-major order as three parallel arrays.
func (c *CocoSketch) SerializePortable() (*envpb.SketchEnvelope, error) {
	n := c.d * c.length
	hashes := make([]uint64, n)
	vals := make([]uint64, n)
	hasKeys := make([]bool, n)

	for i := 0; i < c.d; i++ {
		for j := 0; j < c.length; j++ {
			idx := i*c.length + j
			b := c.table[i][j]
			hashes[idx] = b.Hash
			vals[idx] = b.Val
			hasKeys[idx] = b.HasKey
		}
	}

	state := &cocopb.CocoSketchState{
		D:       uint32(c.d),
		Width:   uint32(c.length),
		Hashes:  hashes,
		Vals:    vals,
		HasKeys: hasKeys,
	}

	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &envpb.SketchEnvelope_Coco{
			Coco: state,
		},
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
