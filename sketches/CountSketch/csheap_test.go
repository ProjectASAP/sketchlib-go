package countsketch

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

func csHeapFixture() *CSHeap {
	return &CSHeap{
		Rows: 2, Cols: 4, CounterType: "i64", Mode: "regular",
		Counts: []int64{0, 127, 128, 65536, -1, -33, -32768, -2147483648},
		K:      5,
		Heap: []common.Item{
			{Key: "alpha", Count: 4294967296},
			{Key: "beta", Count: 127},
			{Key: "delta", Count: 127},
			{Key: "gamma", Count: -33},
		},
	}
}

func TestCSHeapGolden(t *testing.T) {
	asapv1test.CheckGolden(t, "csheap_i64_regular_2x4_strkeys", csHeapFixture(), nil)
}

func TestCSHeapMarshalSortsHeap(t *testing.T) {
	h := csHeapFixture()
	h.Heap = []common.Item{h.Heap[3], h.Heap[2], h.Heap[0], h.Heap[1]}
	got, err := h.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	asapv1test.Equal(t, got, asapv1test.Golden(t, "csheap_i64_regular_2x4_strkeys"))
}

func TestCSHeapRoundTrips(t *testing.T) {
	for name, h := range map[string]*CSHeap{
		"i32 fast": {Rows: 3, Cols: 2, CounterType: "i32", Mode: "fast",
			Counts: []int64{math.MinInt32, math.MaxInt32, 0, -1, 1, 7}, K: 2,
			Heap: []common.Item{{Key: "x", Count: -4}}},
		"empty heap": {Rows: 1, Cols: 1, CounterType: "i64", Mode: "regular",
			Counts: []int64{math.MinInt64}, K: 0, Heap: []common.Item{}},
		"20 rows": {Rows: 20, Cols: 1, CounterType: "i64", Mode: "fast",
			Counts: make([]int64, 20), K: 3, Heap: []common.Item{}},
	} {
		b, err := h.MarshalASAPv1()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var got CSHeap
		if err := got.UnmarshalASAPv1(b); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(&got, h) {
			t.Fatalf("%s: got %+v, want %+v", name, got, h)
		}
	}
}

func TestCSHeapEmptyHeapKeyType(t *testing.T) {
	h := &CSHeap{Rows: 1, Cols: 2, CounterType: "i64", Mode: "fast", Counts: []int64{1, 2}, K: 4}
	b, err := h.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	md, _, err := asapv1.Open(b, asapv1.KindCSHeap)
	if err != nil {
		t.Fatal(err)
	}
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	if got := md.Str("key_type"); got != "u64" {
		t.Fatalf("empty heap key_type %q, want u64", got)
	}
}

func TestCSHeapMarshalRejects(t *testing.T) {
	for name, mutate := range map[string]func(h *CSHeap){
		"f64 counter":       func(h *CSHeap) { h.CounterType = "f64" },
		"unknown mode":      func(h *CSHeap) { h.Mode = "packed" },
		"zero rows":         func(h *CSHeap) { h.Rows, h.Counts = 0, nil },
		"zero cols":         func(h *CSHeap) { h.Cols, h.Counts = 0, nil },
		"21 rows":           func(h *CSHeap) { h.Rows, h.Cols, h.Counts = 21, 1, make([]int64, 21) },
		"short counts":      func(h *CSHeap) { h.Counts = h.Counts[:7] },
		"i32 overflow":      func(h *CSHeap) { h.CounterType, h.Counts[1] = "i32", math.MaxInt32+1 },
		"i32 underflow":     func(h *CSHeap) { h.CounterType = "i32"; h.Counts[7] = math.MinInt32 - 1 },
		"negative k":        func(h *CSHeap) { h.K, h.Heap = -1, nil },
		"k beyond u32":      func(h *CSHeap) { h.K = math.MaxUint32 + 1 },
		"more entries":      func(h *CSHeap) { h.K = 3 },
		"duplicate key":     func(h *CSHeap) { h.Heap[2].Key = "alpha" },
		"invalid UTF-8 key": func(h *CSHeap) { h.Heap[0].Key = "\xff" },
	} {
		h := csHeapFixture()
		mutate(h)
		if _, err := h.MarshalASAPv1(); err == nil {
			t.Errorf("%s: marshalled", name)
		}
	}
}

// rawCSHeap frames metadata and payload fields as given, so a test can build
// an envelope the codec would refuse to write.
type rawCSHeap struct {
	kind              asapv1.KindID
	rows, cols, k     uint64
	counterType, mode string
	keyType           string
	extraKey          bool
	trailing          bool
	counts            []int64
	keys              []string
	intKeys           []int64
	heapCounts        []int64
}

func rawFixture() rawCSHeap {
	h := csHeapFixture()
	r := rawCSHeap{kind: asapv1.KindCSHeap, rows: 2, cols: 4, k: 5, counterType: "i64",
		mode: "regular", keyType: "string", counts: slices.Clone(h.Counts)}
	for _, it := range h.Heap {
		r.keys = append(r.keys, it.Key)
		r.heapCounts = append(r.heapCounts, it.Count)
	}
	return r
}

func (r rawCSHeap) bytes(t *testing.T) []byte {
	t.Helper()
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	md.Uint("rows", r.rows)
	md.Uint("cols", r.cols)
	md.Str("counter_type", r.counterType)
	md.Str("mode", r.mode)
	md.Uint("k", r.k)
	md.Str("key_type", r.keyType)
	if r.extraKey {
		md.Uint("bogus", 1)
	}
	p := asapv1.NewEncoder()
	p.Array(3)
	asapv1.EncodeInts(p, r.counts)
	if r.intKeys != nil {
		asapv1.EncodeInts(p, r.intKeys)
	} else {
		asapv1.EncodeStrs(p, r.keys)
	}
	asapv1.EncodeInts(p, r.heapCounts)
	if r.trailing {
		p.Nil()
	}
	b, err := asapv1.Marshal(r.kind, md, p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCSHeapUnmarshalRejects(t *testing.T) {
	var plain CSHeap
	if err := plain.UnmarshalASAPv1(rawFixture().bytes(t)); err != nil {
		t.Fatalf("unmodified raw fixture: %v", err)
	}
	for name, mutate := range map[string]func(r *rawCSHeap){
		"CMSHeap kind":       func(r *rawCSHeap) { r.kind = asapv1.KindCMSHeap },
		"CountSketch kind":   func(r *rawCSHeap) { r.kind = asapv1.KindCountSketch },
		"f64 counter":        func(r *rawCSHeap) { r.counterType = "f64" },
		"unknown mode":       func(r *rawCSHeap) { r.mode = "Fast" },
		"zero rows":          func(r *rawCSHeap) { r.rows, r.counts = 0, nil },
		"zero cols":          func(r *rawCSHeap) { r.cols, r.counts = 0, nil },
		"21 rows":            func(r *rawCSHeap) { r.rows, r.cols, r.counts = 21, 1, make([]int64, 21) },
		"huge declared dims": func(r *rawCSHeap) { r.rows, r.cols = 20, 1<<24 },
		"cols beyond u32":    func(r *rawCSHeap) { r.cols = 1 << 32 },
		"k beyond u32":       func(r *rawCSHeap) { r.k = 1 << 32 },
		"short counts":       func(r *rawCSHeap) { r.counts = r.counts[:7] },
		"i32 overflow":       func(r *rawCSHeap) { r.counterType, r.counts[3] = "i32", math.MaxInt32+1 },
		"unknown key_type":   func(r *rawCSHeap) { r.keyType = "str" },
		"i64 keys":           func(r *rawCSHeap) { r.keyType, r.intKeys = "i64", []int64{1, 2, 3, 4} },
		"relabelled keys":    func(r *rawCSHeap) { r.keyType = "bytes" },
		"fewer counts":       func(r *rawCSHeap) { r.heapCounts = r.heapCounts[:3] },
		"more entries":       func(r *rawCSHeap) { r.k = 3 },
		"duplicate key":      func(r *rawCSHeap) { r.keys = []string{"alpha", "beta", "alpha", "gamma"} },
		"unknown metadata":   func(r *rawCSHeap) { r.extraKey = true },
		"trailing payload":   func(r *rawCSHeap) { r.trailing = true },
	} {
		r := rawFixture()
		mutate(&r)
		var h CSHeap
		h.Rows = 99
		if err := h.UnmarshalASAPv1(r.bytes(t)); err == nil {
			t.Errorf("%s: unmarshalled", name)
		} else if h.Rows != 99 {
			t.Errorf("%s: receiver changed on error", name)
		}
	}
}

func TestCSHeapEmptyHeapOfAnyKeyTypeDecodes(t *testing.T) {
	for _, keyType := range []string{"u64", "i64", "bytes"} {
		r := rawFixture()
		r.keyType, r.keys, r.heapCounts = keyType, nil, nil
		var h CSHeap
		if err := h.UnmarshalASAPv1(r.bytes(t)); err != nil {
			t.Errorf("empty heap labelled %q: %v", keyType, err)
		}
	}
}

func populatedSketch(t *testing.T) *CountSketch {
	t.Helper()
	s, err := NewCountSketch(4, 64)
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range []string{"a", "b", "c", "d", "e", "a", "a", "b"} {
		s.UpdateString(key, float64(i+1))
	}
	s.TopK = common.NewTopKHeap(3)
	for _, key := range s.SS.Candidates() {
		s.TopK.Update(key, s.EstimateStringCount(key))
	}
	return s
}

func TestCountSketchThroughCSHeap(t *testing.T) {
	s := populatedSketch(t)
	h, err := s.ToCSHeap()
	if err != nil {
		t.Fatal(err)
	}
	if h.CounterType != "i64" || h.Mode != "fast" || h.K != 3 || len(h.Heap) != 3 {
		t.Fatalf("ToCSHeap gave %+v", h)
	}
	b, err := h.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	var decoded CSHeap
	if err := decoded.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	back, err := decoded.ToCountSketch()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Count, s.Count) {
		t.Fatalf("matrix differs:\n got %v\nwant %v", back.Count, s.Count)
	}
	if !reflect.DeepEqual(back.L2, s.L2) {
		t.Fatalf("L2 %v, want %v", back.L2, s.L2)
	}
	for _, key := range []string{"a", "b", "c", "d", "e", "zz"} {
		if got, want := back.EstimateStringCount(key), s.EstimateStringCount(key); got != want {
			t.Errorf("estimate %q: %d, want %d", key, got, want)
		}
	}
	if back.TopK.K != s.TopK.K {
		t.Errorf("TopK K %d, want %d", back.TopK.K, s.TopK.K)
	}
	gotHeap := slices.Clone(back.TopK.Heap)
	wantHeap := slices.Clone(s.TopK.Heap)
	byKey := func(a, b common.Item) int { return strings.Compare(a.Key, b.Key) }
	slices.SortFunc(gotHeap, byKey)
	slices.SortFunc(wantHeap, byKey)
	if !reflect.DeepEqual(gotHeap, wantHeap) {
		t.Errorf("TopK %v, want %v", gotHeap, wantHeap)
	}
	again, err := back.ToCSHeap()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, h) {
		t.Errorf("second ToCSHeap %+v, want %+v", again, h)
	}
}

func TestToCSHeapOrdersHeap(t *testing.T) {
	s, err := NewCountSketch(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	s.TopK = common.NewTopKHeap(4)
	for _, it := range []common.Item{{Key: "b", Count: 5}, {Key: "z", Count: 9}, {Key: "a", Count: 5}, {Key: "c", Count: -1}} {
		s.TopK.Insert(it.Key, it.Count)
	}
	h, err := s.ToCSHeap()
	if err != nil {
		t.Fatal(err)
	}
	want := []common.Item{{Key: "z", Count: 9}, {Key: "a", Count: 5}, {Key: "b", Count: 5}, {Key: "c", Count: -1}}
	if !reflect.DeepEqual(h.Heap, want) {
		t.Fatalf("heap %v, want %v", h.Heap, want)
	}
}

func TestToCSHeapRejects(t *testing.T) {
	s := populatedSketch(t)
	s.Count[1][3] += 0.5
	if _, err := s.ToCSHeap(); err == nil {
		t.Error("fractional cell converted")
	}
	s = populatedSketch(t)
	s.Count[0][0] = math.Inf(1)
	if _, err := s.ToCSHeap(); err == nil {
		t.Error("infinite cell converted")
	}
	s = populatedSketch(t)
	s.Count[0][0] = math.Exp2(63)
	if _, err := s.ToCSHeap(); err == nil {
		t.Error("cell of 2^63 converted")
	}
	s = populatedSketch(t)
	s.TopK = nil
	if _, err := s.ToCSHeap(); err == nil {
		t.Error("sketch without a TopK heap converted")
	}
}

func TestToCountSketchRejects(t *testing.T) {
	fast := func() *CSHeap {
		h := csHeapFixture()
		h.Mode = "fast"
		return h
	}
	if _, err := fast().ToCountSketch(); err != nil {
		t.Fatalf("fast fixture: %v", err)
	}
	if _, err := csHeapFixture().ToCountSketch(); err == nil {
		t.Error("regular mode converted")
	}
	h := fast()
	h.Counts[0] = 1<<53 + 1
	if _, err := h.ToCountSketch(); err == nil {
		t.Error("cell beyond float64's exact range converted")
	}
	h = fast()
	h.Heap[1].Key = "alpha"
	if _, err := h.ToCountSketch(); err == nil {
		t.Error("duplicate heap key converted")
	}
	h = fast()
	h.Rows, h.Cols, h.Counts = 1, 3, []int64{1, 2, 3}
	if _, err := h.ToCountSketch(); err == nil {
		t.Error("non-power-of-two cols converted")
	}
}
