package kll

import (
	"fmt"

	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
	"google.golang.org/protobuf/proto"
)

// SerializePortable serializes the KLLSketch into a portable protobuf SketchEnvelope.
func (s *KLLSketch) SerializePortable() (*pb.SketchEnvelope, error) {
	// Convert []int levels to []uint32.
	levels := make([]uint32, len(s.levels))
	for i, v := range s.levels {
		levels[i] = uint32(v)
	}

	items := append([]float64(nil), s.items...)

	state := &pb.KLLState{
		K:         uint32(s.k),
		M:         uint32(s.m),
		NumLevels: uint32(s.numLevels),
		Levels:    levels,
		Items:     items,
		Coin: &pb.CoinState{
			State:         s.co.state,
			BitCache:      s.co.bitCache,
			RemainingBits: uint32(s.co.remainingBits),
		},
	}

	return &pb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &pb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &pb.SketchEnvelope_Kll{
			Kll: state,
		},
	}, nil
}

// SerializeProtoBytes serializes the KLLSketch as a proto-encoded SketchEnvelope.
func (s *KLLSketch) SerializeProtoBytes() ([]byte, error) {
	env, err := s.SerializePortable()
	if err != nil {
		return nil, err
	}
	return proto.Marshal(env)
}

// DeserializeKLLSketchFromProtoBytes restores a KLLSketch from a proto-encoded SketchEnvelope.
func DeserializeKLLSketchFromProtoBytes(data []byte) (*KLLSketch, error) {
	var env pb.SketchEnvelope
	if err := proto.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	st := env.GetKll()
	if st == nil {
		return nil, fmt.Errorf("kll: proto envelope does not contain KLLState")
	}

	k := int(st.GetK())
	m := int(st.GetM())
	if k <= 0 || m <= 0 {
		return nil, fmt.Errorf("kll: invalid parameters k=%d m=%d", k, m)
	}

	pbLevels := st.GetLevels()
	levels := make([]int, len(pbLevels))
	for i, v := range pbLevels {
		levels[i] = int(v)
	}
	if len(levels) < 2 {
		return nil, fmt.Errorf("kll: invalid levels length %d", len(levels))
	}

	items := append([]float64(nil), st.GetItems()...)
	if levels[len(levels)-1] != len(items) {
		return nil, fmt.Errorf("kll: invalid item layout")
	}

	var co coin
	if pbCoin := st.GetCoin(); pbCoin != nil {
		co = coin{
			state:         normalizeSeed(pbCoin.GetState()),
			bitCache:      pbCoin.GetBitCache(),
			remainingBits: uint8(pbCoin.GetRemainingBits()),
		}
	} else {
		co = newCoin()
	}

	sketch := &KLLSketch{
		items:     items,
		levels:    levels,
		k:         k,
		m:         m,
		numLevels: len(levels) - 1,
		co:        co,
	}
	sketch.bindStoresFromSlices()
	sketch.rebuildCapacityCache()
	return sketch, nil
}

// portableHashSpec returns the standard HashSpec for sketchlib-go.
func portableHashSpec() *pb.HashSpec {
	return &pb.HashSpec{
		Algorithm:          pb.HashAlgorithm_HASH_ALGORITHM_XXH3_64,
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
		SeedDerivation: pb.SeedDerivation_SEED_DERIVATION_PACKED,
	}
}
