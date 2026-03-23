package elasticsketch

import (
	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	cmpb "github.com/ProjectASAP/sketchlib-go/proto/countminsketch"
	elasticpb "github.com/ProjectASAP/sketchlib-go/proto/elasticsketch"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
)

// SerializePortable serializes the ElasticSketch into a portable protobuf SketchEnvelope.
// The heavy buckets are stored as parallel arrays; the light CountMin layer as CountMinState.
func (es *ElasticSketch) SerializePortable() (*envpb.SketchEnvelope, error) {
	es.mu.Lock()
	defer es.mu.Unlock()

	n := len(es.heavy)
	flowIDs := make([]string, n)
	votePos := make([]int32, n)
	voteNeg := make([]int32, n)
	evictions := make([]bool, n)

	for i, b := range es.heavy {
		flowIDs[i] = b.FlowID
		votePos[i] = int32(b.VotePos)
		voteNeg[i] = int32(b.VoteNeg)
		evictions[i] = b.Eviction
	}

	// Serialize the light CountMin layer as a flat float64 matrix.
	rows := es.light.Rows()
	cols := es.light.Cols()
	countsFloat := make([]float64, 0, rows*cols)
	for r := 0; r < rows; r++ {
		countsFloat = append(countsFloat, es.light.RowSlice(r)...)
	}

	light := &cmpb.CountMinState{
		Rows:        uint32(rows),
		Cols:        uint32(cols),
		CounterType: commonpb.CounterType_COUNTER_TYPE_FLOAT64,
		CountsFloat: countsFloat,
	}

	state := &elasticpb.ElasticState{
		BucketCount: uint32(n),
		FlowIds:     flowIDs,
		VotePos:     votePos,
		VoteNeg:     voteNeg,
		Evictions:   evictions,
		Light:       light,
	}

	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &envpb.SketchEnvelope_Elastic{
			Elastic: state,
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
