package countminsketch

import (
	"fmt"

	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
	"google.golang.org/protobuf/proto"
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

// SerializeProtoBytes serializes the CountMinSketch as a proto-encoded SketchEnvelope.
func (s *CountMinSketch) SerializeProtoBytes() ([]byte, error) {
	env, err := s.SerializePortable()
	if err != nil {
		return nil, err
	}
	return proto.Marshal(env)
}

// DeserializeCountMinSketchFromProtoBytes restores a CountMinSketch from a proto-encoded SketchEnvelope.
func DeserializeCountMinSketchFromProtoBytes(data []byte) (*CountMinSketch, error) {
	var env pb.SketchEnvelope
	if err := proto.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	st := env.GetCountMin()
	if st == nil {
		return nil, fmt.Errorf("countminsketch: proto envelope does not contain CountMinState")
	}

	rows := int(st.GetRows())
	cols := int(st.GetCols())
	if rows <= 0 || cols <= 0 {
		return nil, fmt.Errorf("countminsketch: invalid dimensions %d×%d", rows, cols)
	}

	flat := st.GetCountsFloat()
	sumFlat := st.GetSumCounts()
	sum2Flat := st.GetSum2Counts()
	expected := rows * cols
	if len(flat) != expected || len(sumFlat) != expected || len(sum2Flat) != expected {
		return nil, fmt.Errorf("countminsketch: unexpected matrix size")
	}

	count := make([][]float64, rows)
	sumC := make([][]float64, rows)
	sum2C := make([][]float64, rows)
	for r := 0; r < rows; r++ {
		count[r] = append([]float64(nil), flat[r*cols:(r+1)*cols]...)
		sumC[r] = append([]float64(nil), sumFlat[r*cols:(r+1)*cols]...)
		sum2C[r] = append([]float64(nil), sum2Flat[r*cols:(r+1)*cols]...)
	}

	l1 := append([]float64(nil), st.GetL1()...)
	l2 := append([]float64(nil), st.GetL2()...)

	s := &CountMinSketch{
		Rows:  rows,
		Cols:  cols,
		Count: count,
		Sum:   sumC,
		Sum2:  sum2C,
		L1:    l1,
		L2:    l2,
	}
	if err := s.rehydrateStorage(); err != nil {
		return nil, err
	}
	return s, nil
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
