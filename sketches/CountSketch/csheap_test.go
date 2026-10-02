package countsketch

import (
	"cmp"
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
	return s
}

// topByEstimate is the heap ToCSHeap should build: the k keys of highest
// positive estimate, ties by key.
func topByEstimate(s *CountSketch, keys []string, k int) []common.Item {
	var items []common.Item
	for _, key := range keys {
		if est := s.EstimateStringCount(key); est > 0 {
			items = append(items, common.Item{Key: key, Count: est})
		}
	}
	slices.SortFunc(items, func(a, b common.Item) int {
		if a.Count != b.Count {
			return cmp.Compare(b.Count, a.Count)
		}
		return strings.Compare(a.Key, b.Key)
	})
	return items[:min(len(items), k)]
}

func sortedByKey(items []common.Item) []common.Item {
	out := slices.Clone(items)
	slices.SortFunc(out, func(a, b common.Item) int { return strings.Compare(a.Key, b.Key) })
	return out
}

func TestCountSketchThroughCSHeap(t *testing.T) {
	s := populatedSketch(t)
	h, err := s.ToCSHeap(3)
	if err != nil {
		t.Fatal(err)
	}
	if h.CounterType != "i64" || h.Mode != "fast" || h.K != 3 {
		t.Fatalf("ToCSHeap gave %+v", h)
	}
	if want := topByEstimate(s, []string{"a", "b", "c", "d", "e"}, 3); !reflect.DeepEqual(h.Heap, want) {
		t.Fatalf("heap %v, want %v", h.Heap, want)
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
	if back.TopK.K != 3 {
		t.Errorf("TopK K %d, want 3", back.TopK.K)
	}
	if got, want := sortedByKey(back.TopK.Heap), sortedByKey(h.Heap); !reflect.DeepEqual(got, want) {
		t.Errorf("TopK %v, want %v", got, want)
	}
}

func TestToCSHeapBuildsHeapFromCandidates(t *testing.T) {
	s, err := NewCountSketch(5, 1024)
	if err != nil {
		t.Fatal(err)
	}
	for key, n := range map[string]int{"/checkout": 100, "/cart": 40, "/home": 5} {
		for range n {
			s.UpdateString(key, 1)
		}
	}
	if s.TopK != nil && len(s.TopK.Heap) != 0 {
		t.Fatalf("UpdateString filled TopK: %v", s.TopK.Heap)
	}
	h, err := s.ToCSHeap(20)
	if err != nil {
		t.Fatal(err)
	}
	if h.Rows != 5 || h.Cols != 1024 || h.K != 20 {
		t.Fatalf("rows=%d cols=%d k=%d", h.Rows, h.Cols, h.K)
	}
	if len(h.Heap) != 3 {
		t.Fatalf("heap %v, want the 3 candidates", h.Heap)
	}
	if h.Heap[0].Key != "/checkout" || h.Heap[1].Key != "/cart" || h.Heap[2].Key != "/home" {
		t.Fatalf("heap order %v", h.Heap)
	}
	if c := h.Heap[0].Count; c < 85 || c > 115 {
		t.Fatalf("/checkout estimate out of band: %d", c)
	}
	if _, err := h.MarshalASAPv1(); err != nil {
		t.Fatal(err)
	}
}

func TestToCSHeapKeepsTopK(t *testing.T) {
	s, err := NewCountSketch(5, 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []common.Item{{Key: "b", Count: 5}, {Key: "z", Count: 9}, {Key: "a", Count: 5}, {Key: "c", Count: -1}} {
		s.UpdateString(it.Key, float64(it.Count))
		if got := s.EstimateStringCount(it.Key); got != it.Count {
			t.Fatalf("estimate %q = %d, want %d", it.Key, got, it.Count)
		}
	}
	s.UpdateString("d", 2)
	s.InsertWithHashAndValue(common.Hash64([]byte("d")), -2)
	if got := s.EstimateStringCount("d"); got != 0 {
		t.Fatalf("estimate d = %d, want 0", got)
	}
	all := []common.Item{{Key: "z", Count: 9}, {Key: "a", Count: 5}, {Key: "b", Count: 5}}
	for k := 0; k <= 4; k++ {
		h, err := s.ToCSHeap(k)
		if err != nil {
			t.Fatal(err)
		}
		if want := all[:min(k, len(all))]; !reflect.DeepEqual(h.Heap, want) || h.K != k {
			t.Errorf("k=%d: heap %v (K %d), want %v", k, h.Heap, h.K, want)
		}
	}
}

func TestToCSHeapEmptyWithoutCandidates(t *testing.T) {
	s, err := NewCountSketch(3, 256)
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.ToCSHeap(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Heap) != 0 || h.K != 10 {
		t.Fatalf("heap %v, K %d", h.Heap, h.K)
	}
	if _, err := h.MarshalASAPv1(); err != nil {
		t.Fatal(err)
	}
}

func TestToCSHeapRejects(t *testing.T) {
	s := populatedSketch(t)
	s.Count[1][3] += 0.5
	if _, err := s.ToCSHeap(3); err == nil {
		t.Error("fractional cell converted")
	}
	s = populatedSketch(t)
	s.Count[0][0] = math.Inf(1)
	if _, err := s.ToCSHeap(3); err == nil {
		t.Error("infinite cell converted")
	}
	s = populatedSketch(t)
	s.Count[0][0] = math.Exp2(63)
	if _, err := s.ToCSHeap(3); err == nil {
		t.Error("cell of 2^63 converted")
	}
	if _, err := populatedSketch(t).ToCSHeap(-1); err == nil {
		t.Error("negative k converted")
	}
	wide, err := NewCountSketch(5, 8192)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wide.ToCSHeap(3); err == nil {
		t.Error("5x8192 sketch converted to fast mode")
	}
}

func TestLargeCellsSurviveConversion(t *testing.T) {
	s := populatedSketch(t)
	s.Count[2][5] = math.Exp2(60)
	s.Count[3][1] = -math.Exp2(62) - math.Exp2(10)
	h, err := s.ToCSHeap(3)
	if err != nil {
		t.Fatal(err)
	}
	back, err := h.ToCountSketch()
	if err != nil {
		t.Fatal(err)
	}
	if back.Count[2][5] != s.Count[2][5] || back.Count[3][1] != s.Count[3][1] {
		t.Fatalf("cells %v, %v", back.Count[2][5], back.Count[3][1])
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
	for _, v := range []int64{1<<53 + 1, math.MaxInt64, math.MinInt64 + 1} {
		h := fast()
		h.Counts[0] = v
		if _, err := h.ToCountSketch(); err == nil {
			t.Errorf("cell %d, not exact in float64, converted", v)
		}
	}
	h := fast()
	h.Counts[0] = math.MinInt64
	if _, err := h.ToCountSketch(); err != nil {
		t.Errorf("cell MinInt64: %v", err)
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
	h = fast()
	h.Rows, h.Cols, h.Counts = 5, 8192, make([]int64, 5*8192)
	if _, err := h.ToCountSketch(); err == nil {
		t.Error("5x8192 converted")
	}
}
