package univmon

import (
	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	cspb "github.com/ProjectASAP/sketchlib-go/proto/countsketch"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
	umpb "github.com/ProjectASAP/sketchlib-go/proto/univmon"
)

// SerializePortable serializes the UnivSketch into a portable protobuf SketchEnvelope.
// Each layer's CountSketchUniv is stored as a CountSketchState with INT64 counters.
// Each layer's TopK heap is stored as a TopKState.
func (us *UnivSketch) SerializePortable() (*envpb.SketchEnvelope, error) {
	layers := make([]*umpb.UnivMonLayer, us.layer)
	for i := 0; i < us.layer; i++ {
		cs := us.cs_layers[i]

		// Flatten int64 count matrix to row-major sint64 slice.
		countsInt := make([]int64, 0, cs.row*cs.col)
		for r := 0; r < cs.row; r++ {
			for _, v := range cs.countStore.RowSlice(r) {
				countsInt = append(countsInt, v)
			}
		}
		l2 := make([]float64, cs.row)
		for r := 0; r < cs.row; r++ {
			l2[r] = float64(cs.l2[r])
		}

		csState := &cspb.CountSketchState{
			Rows:        uint32(cs.row),
			Cols:        uint32(cs.col),
			CounterType: commonpb.CounterType_COUNTER_TYPE_INT64,
			CountsInt:   countsInt,
			L2:          l2,
		}

		hh := us.HH_layers[i]
		var topk *cspb.TopKState
		if hh != nil {
			entries := make([]*cspb.HeapEntry, 0, len(hh.Heap))
			for _, item := range hh.Heap {
				entries = append(entries, &cspb.HeapEntry{
					Key:   item.Key,
					Count: float64(item.Count),
				})
			}
			topk = &cspb.TopKState{
				K:       uint32(hh.K),
				Entries: entries,
			}
		}

		layers[i] = &umpb.UnivMonLayer{
			Sketch: csState,
			Heap:   topk,
		}
	}

	// Use first layer's dimensions as the nominal sketch dims.
	var sketchRows, sketchCols, heapSize uint32
	if us.layer > 0 {
		sketchRows = uint32(us.cs_layers[0].row)
		sketchCols = uint32(us.cs_layers[0].col)
		if us.HH_layers[0] != nil {
			heapSize = uint32(us.HH_layers[0].K)
		}
	}

	state := &umpb.UnivMonState{
		LayerSize:  uint32(us.layer),
		SketchRows: sketchRows,
		SketchCols: sketchCols,
		HeapSize:   heapSize,
		BucketSize: uint64(us.bucket_size),
		Layers:     layers,
	}

	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &envpb.SketchEnvelope_Univmon{
			Univmon: state,
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
