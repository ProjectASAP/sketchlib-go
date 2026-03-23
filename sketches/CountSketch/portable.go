package countsketch

import (
	"github.com/ProjectASAP/sketchlib-go/common"
	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	cspb "github.com/ProjectASAP/sketchlib-go/proto/countsketch"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
)

// SerializePortable serializes the CountSketch into a portable protobuf SketchEnvelope.
// The counter matrix is stored flat in row-major order using FLOAT64 counters (signed).
func (s *CountSketch) SerializePortable() (*envpb.SketchEnvelope, error) {
	countsFloat := make([]float64, 0, s.Rows*s.Cols)
	for r := 0; r < s.Rows; r++ {
		countsFloat = append(countsFloat, s.Count[r]...)
	}

	l2 := append([]float64(nil), s.L2...)

	var topk *cspb.TopKState
	if s.TopK != nil && len(s.TopK.Heap) > 0 {
		entries := make([]*cspb.HeapEntry, 0, len(s.TopK.Heap))
		for _, item := range s.TopK.Heap {
			entries = append(entries, &cspb.HeapEntry{
				Key:   item.Key,
				Count: float64(item.Count),
			})
		}
		topk = &cspb.TopKState{
			K:       uint32(s.TopK.K),
			Entries: entries,
		}
	}

	state := &cspb.CountSketchState{
		Rows:        uint32(s.Rows),
		Cols:        uint32(s.Cols),
		CounterType: commonpb.CounterType_COUNTER_TYPE_FLOAT64,
		CountsFloat: countsFloat,
		L2:          l2,
		Topk:        topk,
	}

	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &envpb.SketchEnvelope_CountSketch{
			CountSketch: state,
		},
	}, nil
}

func portableHashSpec() *commonpb.HashSpec {
	return &commonpb.HashSpec{
		Algorithm:          commonpb.HashAlgorithm_HASH_ALGORITHM_XXH3_64,
		CanonicalSeedIndex: uint32(common.CanonicalHashSeed),
		SeedList: []uint64{
			0xcafe3553, 0xade3415118, 0x8cc70208, 0x2f024b2b, 0x451a3df5,
			0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f,
			0x9b05688c, 0x1f83d9ab, 0x5be0cd19, 0xcbbb9d5d, 0x629a292a,
			0x9159015a, 0x152fecd8, 0x67332667, 0x8eb44a87, 0xdb0c2e0d,
		},
		SeedDerivation: commonpb.SeedDerivation_SEED_DERIVATION_PACKED,
	}
}
