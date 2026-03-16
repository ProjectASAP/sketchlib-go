package countminsketch

import (
	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
)

// SerializePortable serializes the CountMinSketch into a portable protobuf SketchEnvelope.
// The counter matrix is stored flat in row-major order using FLOAT64 counters.
func (s *CountMinSketch) SerializePortable() (*pb.SketchEnvelope, error) {
	// Flatten the count matrix to row-major float64 slice.
	countsFloat := make([]float64, 0, s.Rows*s.Cols)
	sumCounts := make([]float64, 0, s.Rows*s.Cols)
	sum2Counts := make([]float64, 0, s.Rows*s.Cols)
	for r := 0; r < s.Rows; r++ {
		countsFloat = append(countsFloat, s.Count[r]...)
		sumCounts = append(sumCounts, s.Sum[r]...)
		sum2Counts = append(sum2Counts, s.Sum2[r]...)
	}

	l1 := append([]float64(nil), s.L1...)
	l2 := append([]float64(nil), s.L2...)

	state := &pb.CountMinState{
		Rows:        uint32(s.Rows),
		Cols:        uint32(s.Cols),
		CounterType: pb.CounterType_COUNTER_TYPE_FLOAT64,
		CountsFloat: countsFloat,
		SumCounts:   sumCounts,
		Sum2Counts:  sum2Counts,
		L1:          l1,
		L2:          l2,
	}

	return &pb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &pb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &pb.SketchEnvelope_CountMin{
			CountMin: state,
		},
	}, nil
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
