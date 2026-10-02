package elasticsketch

import (
	"fmt"
	"math"

	"github.com/ProjectASAP/sketchlib-go/common/storage"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// The ASAPv1 encoding of an ElasticSketch's light layer: int32 counters,
// columns hashed per row (mode "regular"), at most one row per standard seed.
const (
	wireLightCounterType = "i32"
	wireLightMode        = "regular"
	maxWireLightRows     = 20
)

// checkWireGeometry rejects a geometry the encoding cannot carry and returns
// the light layer's cell count.
func checkWireGeometry(heavyBuckets, lightRows, lightCols uint64) (int, error) {
	if heavyBuckets == 0 || lightRows == 0 || lightCols == 0 {
		return 0, fmt.Errorf("elasticsketch: ASAPv1 dimensions must be non-zero: heavy_buckets=%d, light=%dx%d", heavyBuckets, lightRows, lightCols)
	}
	if lightRows > maxWireLightRows {
		return 0, fmt.Errorf("elasticsketch: ASAPv1 carries at most %d light rows, got %d", maxWireLightRows, lightRows)
	}
	if heavyBuckets > math.MaxInt32 {
		return 0, fmt.Errorf("elasticsketch: heavy bucket count %d exceeds int32", heavyBuckets)
	}
	if lightCols > math.MaxUint32 {
		return 0, fmt.Errorf("elasticsketch: light cols %d exceeds u32", lightCols)
	}
	if lightCols > uint64(math.MaxInt)/lightRows {
		return 0, fmt.Errorf("elasticsketch: light layer %dx%d overflows a cell count", lightRows, lightCols)
	}
	return int(lightRows * lightCols), nil
}

// MarshalASAPv1 encodes the heavy buckets, the stale-copy flag and the light
// counters as an ASAPv1 Elastic envelope. A free bucket encodes as nil, so one
// that still names a flow is an error.
func (es *ElasticSketch) MarshalASAPv1() ([]byte, error) {
	es.mu.Lock()
	defer es.mu.Unlock()

	rows, cols := es.light.Rows(), es.light.Cols()
	if _, err := checkWireGeometry(uint64(len(es.heavy)), uint64(rows), uint64(cols)); err != nil {
		return nil, err
	}
	if es.bktlen != len(es.heavy) {
		return nil, fmt.Errorf("elasticsketch: bktlen %d != heavy table length %d", es.bktlen, len(es.heavy))
	}
	for i := range es.heavy {
		if es.heavy[i].vacant() && es.heavy[i].FlowID != "" {
			return nil, fmt.Errorf("elasticsketch: heavy bucket %d is free but names a flow", i)
		}
	}

	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	md.Uint("heavy_buckets", uint64(len(es.heavy)))
	md.Uint("light_rows", uint64(rows))
	md.Uint("light_cols", uint64(cols))
	md.Str("light_counter_type", wireLightCounterType)
	md.Str("light_mode", wireLightMode)

	p := asapv1.NewEncoder()
	p.Array(6)
	p.Array(len(es.heavy))
	for i := range es.heavy {
		if es.heavy[i].vacant() {
			p.Nil()
		} else {
			p.Str(es.heavy[i].FlowID)
		}
	}
	p.Array(len(es.heavy))
	for i := range es.heavy {
		p.Int(int64(es.heavy[i].VotePos))
	}
	p.Array(len(es.heavy))
	for i := range es.heavy {
		p.Int(int64(es.heavy[i].VoteNeg))
	}
	p.Array(len(es.heavy))
	for i := range es.heavy {
		p.Bool(es.heavy[i].Eviction)
	}
	p.Bool(es.staleCopies)
	p.Array(rows * cols)
	for r := 0; r < rows; r++ {
		for _, v := range es.light.RowSlice(r) {
			p.Int(int64(v))
		}
	}
	return asapv1.Marshal(asapv1.KindElastic, md, p)
}

// UnmarshalASAPv1 replaces es with the sketch in an ASAPv1 Elastic envelope.
// A bucket's flow id is nil exactly when its positive vote is zero.
func (es *ElasticSketch) UnmarshalASAPv1(b []byte) error {
	md, p, err := asapv1.Open(b, asapv1.KindElastic)
	if err != nil {
		return err
	}
	md.ExpectVersion(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	heavyBuckets := md.Uint32("heavy_buckets")
	rows := md.Uint32("light_rows")
	cols := md.Uint32("light_cols")
	md.ExpectStr("light_counter_type", wireLightCounterType)
	md.ExpectStr("light_mode", wireLightMode)
	if err := md.Finish(); err != nil {
		return err
	}
	cells, err := checkWireGeometry(uint64(heavyBuckets), uint64(rows), uint64(cols))
	if err != nil {
		return err
	}

	p.ExpectArray(6)
	n := p.Array()
	var flowIDs []*string
	if p.Err() == nil {
		flowIDs = make([]*string, n)
		for i := range flowIDs {
			if !p.Nil() {
				id := p.Str()
				flowIDs[i] = &id
			}
		}
	}
	votePos := asapv1.DecodeInts[int32](p)
	voteNeg := asapv1.DecodeInts[int32](p)
	n = p.Array()
	var evictions []bool
	if p.Err() == nil {
		evictions = make([]bool, n)
		for i := range evictions {
			evictions[i] = p.Bool()
		}
	}
	staleCopies := p.Bool()
	counts := asapv1.DecodeInts[int32](p)
	if err := p.Finish(); err != nil {
		return err
	}

	nb := int(heavyBuckets)
	if len(flowIDs) != nb || len(votePos) != nb || len(voteNeg) != nb || len(evictions) != nb {
		return fmt.Errorf("elasticsketch: heavy payload lengths (flow_ids %d, vote_pos %d, vote_neg %d, evictions %d) != heavy_buckets %d",
			len(flowIDs), len(votePos), len(voteNeg), len(evictions), nb)
	}
	if len(counts) != cells {
		return fmt.Errorf("elasticsketch: %d light counts for %dx%d", len(counts), rows, cols)
	}
	heavy := make([]HeavyBucket, nb)
	for i := range heavy {
		if (flowIDs[i] == nil) != (votePos[i] == 0) {
			return fmt.Errorf("elasticsketch: heavy bucket %d has a flow id and a vote_pos that disagree on occupancy", i)
		}
		if flowIDs[i] != nil {
			heavy[i].FlowID = *flowIDs[i]
		}
		heavy[i].VotePos = votePos[i]
		heavy[i].VoteNeg = voteNeg[i]
		heavy[i].Eviction = evictions[i]
	}
	light, err := storage.Vector2DFromFn(int(rows), int(cols), func(r, c int) int32 {
		return counts[r*int(cols)+c]
	})
	if err != nil {
		return err
	}

	es.mu.Lock()
	defer es.mu.Unlock()
	es.heavy = heavy
	es.bktlen = nb
	es.light = light
	es.staleCopies = staleCopies
	return nil
}
