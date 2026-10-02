package elasticsketch

import (
	"bytes"
	"math"
	"slices"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

var goldenHeavy = []HeavyBucket{
	{FlowID: "", VotePos: 0, VoteNeg: 0, Eviction: true},
	{FlowID: "10.0.0.1:443>192.168.10.20:5123", VotePos: 127, VoteNeg: 128, Eviction: false},
	{FlowID: "", VotePos: 1, VoteNeg: 65535, Eviction: true},
	{FlowID: "10.0.0.1:443>192.168.10.20:51234", VotePos: math.MaxInt32, VoteNeg: 256, Eviction: true},
}

var goldenLight = [2][4]int32{
	{0, 255, 65536, math.MaxInt32},
	{-1, -33, -32768, math.MinInt32},
}

// withGoldenState sets the fixtures' heavy buckets and light cells on a
// 4-bucket, 2x4 sketch.
func withGoldenState(t *testing.T, es *ElasticSketch) *ElasticSketch {
	t.Helper()
	if len(es.heavy) != len(goldenHeavy) || es.light.Rows() != 2 || es.light.Cols() != 4 {
		t.Fatalf("sketch is %d buckets over %dx%d", len(es.heavy), es.light.Rows(), es.light.Cols())
	}
	copy(es.heavy, goldenHeavy)
	for r := range goldenLight {
		copy(es.light.RowSlice(r), goldenLight[r][:])
	}
	return es
}

func newSketch(t *testing.T, buckets, rows, cols int) *ElasticSketch {
	t.Helper()
	es, err := New(Config{BucketCount: buckets, LightRows: rows, LightCols: cols})
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func lightCells(es *ElasticSketch) []int32 {
	var out []int32
	for r := 0; r < es.light.Rows(); r++ {
		out = append(out, es.light.RowSlice(r)...)
	}
	return out
}

func sameState(got, want *ElasticSketch) bool {
	return got.bktlen == want.bktlen && got.staleCopies == want.staleCopies &&
		slices.Equal(got.heavy, want.heavy) &&
		got.light.Rows() == want.light.Rows() && got.light.Cols() == want.light.Cols() &&
		slices.Equal(lightCells(got), lightCells(want))
}

func TestASAPv1Golden(t *testing.T) {
	asapv1test.CheckGolden(t, "elastic_4b_2x4", withGoldenState(t, newSketch(t, 4, 2, 4)), sameState)

	expanded := newSketch(t, 2, 2, 4)
	if err := expanded.ExpandHeavy(); err != nil {
		t.Fatal(err)
	}
	asapv1test.CheckGolden(t, "elastic_4b_2x4_stale", withGoldenState(t, expanded), sameState)
}

func TestASAPv1FreeBucketIsNilAndEmptyFlowIsStr(t *testing.T) {
	es := newSketch(t, 4, 2, 8)
	es.InsertN("", 3)
	idx := es.bucketIndex("")
	b, err := es.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	_, _, payload, err := asapv1.Split(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x96, 0x94}
	for i := range 4 {
		if i == idx {
			want = append(want, 0xa0)
		} else {
			want = append(want, 0xc0)
		}
	}
	if !bytes.HasPrefix(payload, want) {
		t.Fatalf("payload starts % x, want % x", payload[:len(want)], want)
	}
	var back ElasticSketch
	if err := back.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	if got := back.Query(""); got != 3 {
		t.Fatalf(`Query("") = %d after round trip, want 3`, got)
	}
}

func TestASAPv1StaleCopiesRoundTrip(t *testing.T) {
	es := newSketch(t, 4, 2, 64)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		es.InsertN(id, 10)
	}
	want := es.HeavyHitters(1)
	if len(want) == 0 {
		t.Fatal("no heavy hitters")
	}
	if err := es.ExpandHeavy(); err != nil {
		t.Fatal(err)
	}
	b, err := es.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	var back ElasticSketch
	if err := back.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	if !sameState(&back, es) || !back.staleCopies {
		t.Fatal("decoded state differs")
	}
	if got := back.HeavyHitters(1); !slices.Equal(got, want) {
		t.Fatalf("heavy hitters %v after expand and round trip, want %v", got, want)
	}
}

// rawElastic is an Elastic envelope written field by field.
type rawElastic struct {
	heavyBuckets, rows, cols uint64
	counterType, mode        string
	extraKey                 bool
	flowIDs                  []*string
	votePos, voteNeg         []int64
	evictions                []bool
	stale                    bool
	counts                   []int64
}

func str(s string) *string { return &s }

func goodRaw() rawElastic {
	return rawElastic{
		heavyBuckets: 2, rows: 1, cols: 2,
		counterType: "i32", mode: "regular",
		flowIDs:   []*string{nil, str("a")},
		votePos:   []int64{0, 3},
		voteNeg:   []int64{0, 1},
		evictions: []bool{true, false},
		counts:    []int64{4, 5},
	}
}

func (r rawElastic) bytes(t *testing.T) []byte {
	t.Helper()
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	md.Uint("heavy_buckets", r.heavyBuckets)
	md.Uint("light_rows", r.rows)
	md.Uint("light_cols", r.cols)
	md.Str("light_counter_type", r.counterType)
	if r.mode != "" {
		md.Str("light_mode", r.mode)
	}
	if r.extraKey {
		md.Uint("extra", 1)
	}
	p := asapv1.NewEncoder()
	p.Array(6)
	p.Array(len(r.flowIDs))
	for _, id := range r.flowIDs {
		if id == nil {
			p.Nil()
		} else {
			p.Str(*id)
		}
	}
	asapv1.EncodeInts(p, r.votePos)
	asapv1.EncodeInts(p, r.voteNeg)
	p.Array(len(r.evictions))
	for _, e := range r.evictions {
		p.Bool(e)
	}
	p.Bool(r.stale)
	asapv1.EncodeInts(p, r.counts)
	b, err := asapv1.Marshal(asapv1.KindElastic, md, p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestASAPv1RejectsMalformed(t *testing.T) {
	good := goodRaw().bytes(t)
	var probe ElasticSketch
	if err := probe.UnmarshalASAPv1(good); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	kind, metadata, payload, err := asapv1.Split(good)
	if err != nil {
		t.Fatal(err)
	}
	trailing, err := asapv1.Encode(kind, metadata, append(slices.Clip(payload), 0xc0))
	if err != nil {
		t.Fatal(err)
	}
	other, err := asapv1.Encode(asapv1.KindCountMin, metadata, payload)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{"other kind": other, "trailing bytes": trailing}
	for name, mutate := range map[string]func(r *rawElastic){
		"counter type i64":   func(r *rawElastic) { r.counterType = "i64" },
		"mode fast":          func(r *rawElastic) { r.mode = "fast" },
		"missing mode":       func(r *rawElastic) { r.mode = "" },
		"unknown key":        func(r *rawElastic) { r.extraKey = true },
		"zero heavy buckets": func(r *rawElastic) { r.heavyBuckets = 0 },
		"zero rows":          func(r *rawElastic) { r.rows = 0; r.counts = nil },
		"zero cols":          func(r *rawElastic) { r.cols = 0; r.counts = nil },
		"21 rows":            func(r *rawElastic) { r.rows = 21; r.cols = 1; r.counts = make([]int64, 21) },
		"heavy over int32":   func(r *rawElastic) { r.heavyBuckets = math.MaxInt32 + 1 },
		"short flow_ids":     func(r *rawElastic) { r.flowIDs = r.flowIDs[:1] },
		"short vote_pos":     func(r *rawElastic) { r.votePos = r.votePos[:1] },
		"short vote_neg":     func(r *rawElastic) { r.voteNeg = r.voteNeg[:1] },
		"short evictions":    func(r *rawElastic) { r.evictions = r.evictions[:1] },
		"long counts":        func(r *rawElastic) { r.counts = append(r.counts, 0) },
		"nil with votes":     func(r *rawElastic) { r.votePos[0] = 2 },
		"flow without votes": func(r *rawElastic) { r.votePos[1] = 0 },
		"vote over int32":    func(r *rawElastic) { r.votePos[1] = math.MaxInt32 + 1 },
		"count over int32":   func(r *rawElastic) { r.counts[0] = math.MinInt32 - 1 },
	} {
		r := goodRaw()
		mutate(&r)
		cases[name] = r.bytes(t)
	}
	for name, b := range cases {
		before := withGoldenState(t, newSketch(t, 4, 2, 4))
		s := withGoldenState(t, newSketch(t, 4, 2, 4))
		if err := s.UnmarshalASAPv1(b); err == nil {
			t.Errorf("%s: decoded", name)
		}
		if !sameState(s, before) {
			t.Errorf("%s: receiver changed on error", name)
		}
	}

	r := goodRaw()
	r.rows, r.cols, r.counts = 20, 1, make([]int64, 20)
	if err := probe.UnmarshalASAPv1(r.bytes(t)); err != nil {
		t.Errorf("20 rows: %v", err)
	}
}

func TestASAPv1MarshalRejects(t *testing.T) {
	if _, err := newSketch(t, 4, 21, 8).MarshalASAPv1(); err == nil {
		t.Error("21 light rows encoded")
	}
	if _, err := newSketch(t, 4, 20, 8).MarshalASAPv1(); err != nil {
		t.Errorf("20 light rows: %v", err)
	}
	named := newSketch(t, 4, 2, 8)
	named.heavy[1].FlowID = "ghost"
	if _, err := named.MarshalASAPv1(); err == nil {
		t.Error("free bucket naming a flow encoded")
	}
	skewed := newSketch(t, 4, 2, 8)
	skewed.bktlen = 3
	if _, err := skewed.MarshalASAPv1(); err == nil {
		t.Error("bktlen out of step with the heavy table encoded")
	}
	invalid := newSketch(t, 4, 2, 8)
	invalid.InsertN("\xff", 1)
	if _, err := invalid.MarshalASAPv1(); err == nil {
		t.Error("non-UTF-8 flow id encoded")
	}
}
