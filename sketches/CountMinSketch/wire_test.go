package countminsketch

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

// fromCounts builds the state UnmarshalASAPv1 yields for a row-major counts
// array: Sum and Sum2 equal to the counts, L1 the row sums.
func fromCounts(t *testing.T, rows, cols int, counts []float64) *CountMinSketch {
	t.Helper()
	s, err := NewCountMinSketch(rows, cols)
	if err != nil {
		t.Fatal(err)
	}
	for r := range rows {
		row := counts[r*cols : (r+1)*cols]
		copy(s.Count[r], row)
		copy(s.Sum[r], row)
		copy(s.Sum2[r], row)
		for _, v := range row {
			s.L1[r] += v
		}
	}
	return s
}

func sameState(got, want *CountMinSketch) bool {
	eq := func(a, b [][]float64) bool { return slices.EqualFunc(a, b, slices.Equal) }
	return got.Rows == want.Rows && got.Cols == want.Cols &&
		eq(got.Count, want.Count) && eq(got.Sum, want.Sum) && eq(got.Sum2, want.Sum2) &&
		slices.Equal(got.L1, want.L1) && got.SampleP() == want.SampleP()
}

func TestASAPv1Golden(t *testing.T) {
	known := fromCounts(t, 2, 3, []float64{0, 1.5, 2.25, 3.75, 4.125, 5.0625})
	asapv1test.CheckGolden(t, "cms_f64_fast_2x3", known, sameState)
}

func TestASAPv1RejectsOtherCounterTypeAndMode(t *testing.T) {
	var s CountMinSketch
	err := s.UnmarshalASAPv1(asapv1test.Golden(t, "cms_i64_regular_2x3"))
	if err == nil || !strings.Contains(err.Error(), "counter_type") {
		t.Fatalf("i64/regular fixture: got %v, want a counter_type mismatch", err)
	}
	for name, mutate := range map[string]func(md *asapv1.MetadataWriter){
		"i64":     func(md *asapv1.MetadataWriter) { md.Str("counter_type", "i64"); md.Str("mode", "fast") },
		"i32":     func(md *asapv1.MetadataWriter) { md.Str("counter_type", "i32"); md.Str("mode", "fast") },
		"regular": func(md *asapv1.MetadataWriter) { md.Str("counter_type", "f64"); md.Str("mode", "regular") },
	} {
		if err := s.UnmarshalASAPv1(envelope(t, 2, 3, mutate, make([]float64, 6))); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

// envelope frames a Count-Min envelope by hand; tail writes the keys after cols.
func envelope(t *testing.T, rows, cols uint64, tail func(*asapv1.MetadataWriter), counts []float64) []byte {
	t.Helper()
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	md.Uint("rows", rows)
	md.Uint("cols", cols)
	tail(md)
	p := asapv1.NewEncoder()
	p.Array(1)
	asapv1.EncodeFloat64s(p, counts)
	b, err := asapv1.Marshal(asapv1.KindCountMin, md, p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fastF64(md *asapv1.MetadataWriter) {
	md.Str("counter_type", "f64")
	md.Str("mode", "fast")
}

func TestASAPv1RejectsMalformed(t *testing.T) {
	good := envelope(t, 2, 3, fastF64, make([]float64, 6))
	var s CountMinSketch
	if err := s.UnmarshalASAPv1(good); err != nil {
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
	other, err := asapv1.Encode(asapv1.KindCountSketch, metadata, payload)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{
		"zero rows":      envelope(t, 0, 3, fastF64, nil),
		"zero cols":      envelope(t, 2, 0, fastF64, nil),
		"21 rows":        envelope(t, 21, 1, fastF64, make([]float64, 21)),
		"short counts":   envelope(t, 2, 3, fastF64, make([]float64, 5)),
		"long counts":    envelope(t, 2, 3, fastF64, make([]float64, 7)),
		"unknown key":    envelope(t, 2, 3, func(md *asapv1.MetadataWriter) { fastF64(md); md.Uint("extra", 1) }, make([]float64, 6)),
		"missing mode":   envelope(t, 2, 3, func(md *asapv1.MetadataWriter) { md.Str("counter_type", "f64") }, make([]float64, 6)),
		"other kind":     other,
		"trailing bytes": trailing,
	} {
		before := fromCounts(t, 1, 2, []float64{7, 8})
		s := *before
		if err := s.UnmarshalASAPv1(b); err == nil {
			t.Errorf("%s: decoded", name)
		}
		if !sameState(&s, before) {
			t.Errorf("%s: receiver changed on error", name)
		}
	}
	if err := s.UnmarshalASAPv1(envelope(t, 20, 1, fastF64, make([]float64, 20))); err != nil {
		t.Errorf("20 rows: %v", err)
	}
}

func TestASAPv1MarshalRejects(t *testing.T) {
	wide, err := NewCountMinSketch(21, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wide.MarshalASAPv1(); err == nil {
		t.Error("21 rows encoded")
	}
	sampled, _ := NewCountMinSketch(3, 8)
	sampled.WithSampleP(0.5, 1)
	if _, err := sampled.MarshalASAPv1(); err == nil {
		t.Error("sampled sketch encoded")
	}
	ragged := &CountMinSketch{Rows: 2, Cols: 2, Count: [][]float64{{1, 2}, {3}}}
	if _, err := ragged.MarshalASAPv1(); err == nil {
		t.Error("ragged matrix encoded")
	}
	empty := &CountMinSketch{}
	if _, err := empty.MarshalASAPv1(); err == nil {
		t.Error("zero-dimension sketch encoded")
	}
}

func TestASAPv1RoundTripKeepsEstimates(t *testing.T) {
	cm, _ := NewCountMinSketch(5, 1000)
	disabled, _ := NewCountMinSketch(5, 1000)
	disabled.WithSampleP(1.0, 9)
	for i := range 3000 {
		in := common.FromString(fmt.Sprintf("k%d", i%211))
		cm.UpdateWeight(in, 0.5+float64(i%3))
		disabled.UpdateWeight(in, 0.5+float64(i%3))
	}
	b, err := cm.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	b2, err := disabled.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	asapv1test.Equal(t, b2, b)
	var back CountMinSketch
	if err := back.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	for i := range 211 {
		in := common.FromString(fmt.Sprintf("k%d", i))
		if got, want := back.Estimate(in), cm.Estimate(in); got != want {
			t.Fatalf("k%d: estimate %v after round trip, want %v", i, got, want)
		}
	}
	if got, want := back.CM_L1(), cm.CM_L1(); got != want {
		t.Fatalf("L1 %v after round trip, want %v", got, want)
	}
	back.Update(common.FromString("k0"))
	if back.Estimate(common.FromString("k0")) != cm.Estimate(common.FromString("k0"))+1 {
		t.Fatal("decoded sketch does not keep updating in place")
	}
}

// Columns Rust's CountMin<Vector2D<f64>, FastPath> touches for
// DataInput::Str("asapv1-pin"), one geometry per matrix-hash layout.
func TestUpdateColumnsMatchRustFastPath(t *testing.T) {
	for _, tc := range []struct {
		rows, cols int
		want       []int
	}{
		{3, 4096, []int{139, 2213, 3527}},
		{5, 4096, []int{2621, 1916, 3522, 2386, 3321}},
		{20, 1024, []int{139, 279, 543, 9, 469, 788, 568, 298, 16, 796, 468, 39, 609, 966, 629, 919, 852, 543, 64, 624}},
	} {
		s, _ := NewCountMinSketch(tc.rows, tc.cols)
		s.Update(common.FromString("asapv1-pin"))
		var got []int
		for r := range tc.rows {
			for c, v := range s.Count[r] {
				if v != 0 {
					got = append(got, c)
				}
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%dx%d: columns %v, want %v", tc.rows, tc.cols, got, tc.want)
		}
	}
}

func TestASAPv1RefusesUint64HashWritesOutsidePacked64(t *testing.T) {
	const hash = 0x9e3779b97f4a7c15
	in := &common.SketchInput{Hash: hash}
	writes := map[string]func(s *CountMinSketch){
		"InsertWithHash":                 func(s *CountMinSketch) { s.InsertWithHash(hash) },
		"FastInsertWeightWithHashValue":  func(s *CountMinSketch) { s.FastInsertWeightWithHashValue(hash, 2) },
		"InsertWithHashGOS":              func(s *CountMinSketch) { s.InsertWithHashGOS(hash, 1, 100) },
		"InsertWithHashGOS no threshold": func(s *CountMinSketch) { s.InsertWithHashGOS(hash, 1, 0) },
		"ProcessInput":                   func(s *CountMinSketch) { s.ProcessInput(in, 100, func(common.DeltaUpdate) {}) },
		"ColForRow":                      func(s *CountMinSketch) { s.IncrCell(0, s.ColForRow(in, 0), 1) },
		"InsertWithHashSampledPerRow":    func(s *CountMinSketch) { s.InsertWithHashSampledPerRow(hash, common.NewGeometricSampler(0.99, 1)) },
		"InsertWithHashAtRows":           func(s *CountMinSketch) { s.InsertWithHashAtRows(hash, 1, 1, 0.5) },
		"InsertWithHashAtRowsGOS":        func(s *CountMinSketch) { s.InsertWithHashAtRowsGOS(hash, 1, 1, 0.5, 100) },
		"MergeDelta":                     func(s *CountMinSketch) { s.MergeDelta(common.DeltaUpdate{Row: 0, Col: 1, Value: 1}) },
	}
	for name, write := range writes {
		packed, _ := NewCountMinSketch(3, 4096)
		write(packed)
		if _, err := packed.MarshalASAPv1(); err != nil {
			t.Errorf("%s at 3x4096 (packed 64-bit): %v", name, err)
		}
		for _, dims := range [][2]int{{5, 4096}, {20, 1024}} {
			wide, _ := NewCountMinSketch(dims[0], dims[1])
			write(wide)
			if _, err := wide.MarshalASAPv1(); err == nil || !strings.Contains(err.Error(), "uint64-hash") {
				t.Errorf("%s at %dx%d: got %v, want a uint64-hash refusal", name, dims[0], dims[1], err)
			}
			clean, _ := NewCountMinSketch(dims[0], dims[1])
			clean.Update(common.FromString("k"))
			if _, err := clean.MarshalASAPv1(); err != nil {
				t.Fatalf("Update-only %dx%d: %v", dims[0], dims[1], err)
			}
			if err := clean.Merge(wide); err != nil {
				t.Fatal(err)
			}
			if _, err := clean.MarshalASAPv1(); err == nil {
				t.Errorf("%s at %dx%d: merged sketch encoded", name, dims[0], dims[1])
			}
			wide.Reset()
			if _, err := wide.MarshalASAPv1(); err != nil {
				t.Errorf("%s at %dx%d after Reset: %v", name, dims[0], dims[1], err)
			}
		}
	}
}

func TestASAPv1RefusesSampledCountsAfterSamplingIsCleared(t *testing.T) {
	sampled := func() *CountMinSketch {
		s, _ := NewCountMinSketch(3, 512)
		s.WithSampleP(0.5, 1)
		for range 100 {
			s.Update(common.FromString("z"))
		}
		return s
	}
	cleared := sampled().WithSampleP(1.0, 1)
	if _, err := cleared.MarshalASAPv1(); err == nil {
		t.Error("sampled counts encoded after WithSampleP(1.0)")
	}
	cleared.Reset()
	if _, err := cleared.MarshalASAPv1(); err != nil {
		t.Errorf("after Reset: %v", err)
	}

	merged, _ := NewCountMinSketch(3, 512)
	if err := merged.Merge(sampled()); err != nil {
		t.Fatal(err)
	}
	if _, err := merged.MarshalASAPv1(); err == nil {
		t.Error("unsampled sketch encoded after merging sampled counts")
	}
	merged3, _ := NewCountMinSketch(3, 512)
	if err := merged3.Merge(sampled().WithSampleP(1.0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := merged3.MarshalASAPv1(); err == nil {
		t.Error("unsampled sketch encoded after merging cleared sampled counts")
	}
	idle, _ := NewCountMinSketch(3, 512)
	idle.WithSampleP(0.5, 1)
	merged2, _ := NewCountMinSketch(3, 512)
	if err := merged2.Merge(idle); err != nil {
		t.Fatal(err)
	}
	if _, err := merged2.MarshalASAPv1(); err == nil {
		t.Error("unsampled sketch encoded after merging a sampling sketch")
	}
}
