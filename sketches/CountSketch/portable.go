package countsketch

import (
	"fmt"

	"github.com/ProjectASAP/sketchlib-go/common"
	pb "github.com/ProjectASAP/sketchlib-go/proto/sketchlibpb"
	"google.golang.org/protobuf/proto"
)

// SerializePortable serializes the CountSketch into a portable protobuf SketchEnvelope.
// The counter matrix is stored flat in row-major order using FLOAT64 counters (signed).
func (s *CountSketch) SerializePortable() (*pb.SketchEnvelope, error) {
	countsFloat := make([]float64, 0, s.Rows*s.Cols)
	for r := 0; r < s.Rows; r++ {
		countsFloat = append(countsFloat, s.Count[r]...)
	}

	l2 := append([]float64(nil), s.L2...)

	var topk *pb.TopKState
	if s.TopK != nil && len(s.TopK.Heap) > 0 {
		entries := make([]*pb.HeapEntry, 0, len(s.TopK.Heap))
		for _, item := range s.TopK.Heap {
			entries = append(entries, &pb.HeapEntry{
				Key:   item.Key,
				Count: float64(item.Count),
			})
		}
		topk = &pb.TopKState{
			K:       uint32(s.TopK.K),
			Entries: entries,
		}
	}

	state := &pb.CountSketchState{
		Rows:        uint32(s.Rows),
		Cols:        uint32(s.Cols),
		CounterType: pb.CounterType_COUNTER_TYPE_FLOAT64,
		CountsFloat: countsFloat,
		L2:          l2,
		Topk:        topk,
	}

	return &pb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &pb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &pb.SketchEnvelope_CountSketch{
			CountSketch: state,
		},
	}, nil
}

// SerializeProtoBytes serializes the CountSketch as a proto-encoded SketchEnvelope.
func (s *CountSketch) SerializeProtoBytes() ([]byte, error) {
	env, err := s.SerializePortable()
	if err != nil {
		return nil, err
	}
	return proto.Marshal(env)
}

// DeserializeCountSketchFromProtoBytes restores a CountSketch from a proto-encoded SketchEnvelope.
func DeserializeCountSketchFromProtoBytes(data []byte) (*CountSketch, error) {
	var env pb.SketchEnvelope
	if err := proto.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	st := env.GetCountSketch()
	if st == nil {
		return nil, fmt.Errorf("countsketch: proto envelope does not contain CountSketchState")
	}

	rows := int(st.GetRows())
	cols := int(st.GetCols())
	if rows <= 0 || cols <= 0 {
		return nil, fmt.Errorf("countsketch: invalid dimensions %d×%d", rows, cols)
	}

	flat := st.GetCountsFloat()
	expected := rows * cols
	if len(flat) != expected {
		return nil, fmt.Errorf("countsketch: unexpected matrix size")
	}

	count := make([][]float64, rows)
	for r := 0; r < rows; r++ {
		count[r] = append([]float64(nil), flat[r*cols:(r+1)*cols]...)
	}

	l2 := append([]float64(nil), st.GetL2()...)

	var topk *common.TopKHeap
	if pbTopK := st.GetTopk(); pbTopK != nil && len(pbTopK.GetEntries()) > 0 {
		k := int(pbTopK.GetK())
		if k <= 0 {
			k = TOPK_SIZE
		}
		topk = common.NewTopKHeap(k)
		for _, e := range pbTopK.GetEntries() {
			topk.Heap = append(topk.Heap, common.Item{
				Key:   e.GetKey(),
				Count: int64(e.GetCount()),
			})
		}
	}

	cs := &CountSketch{
		Rows:  rows,
		Cols:  cols,
		Count: count,
		L2:    l2,
		TopK:  topk,
	}
	if err := cs.rehydrateStorage(); err != nil {
		return nil, err
	}
	return cs, nil
}

func portableHashSpec() *pb.HashSpec {
	return &pb.HashSpec{
		Algorithm:          pb.HashAlgorithm_HASH_ALGORITHM_XXH3_64,
		CanonicalSeedIndex: uint32(common.CanonicalHashSeed),
		SeedList: []uint64{
			0xcafe3553, 0xade3415118, 0x8cc70208, 0x2f024b2b, 0x451a3df5,
			0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f,
			0x9b05688c, 0x1f83d9ab, 0x5be0cd19, 0xcbbb9d5d, 0x629a292a,
			0x9159015a, 0x152fecd8, 0x67332667, 0x8eb44a87, 0xdb0c2e0d,
		},
		SeedDerivation: pb.SeedDerivation_SEED_DERIVATION_PACKED,
	}
}
