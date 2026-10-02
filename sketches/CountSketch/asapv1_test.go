package countsketch

import (
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

var fixtureCounts = []float64{0, 127, 128, 65536, -1, -33, -32768, -2147483648}

func fixtureSketch(t *testing.T, ct CounterType, mode Mode) *CountSketch {
	t.Helper()
	s, err := NewCountSketch(2, 4)
	if err != nil {
		t.Fatal(err)
	}
	s.CounterType, s.Mode = ct, mode
	for i, v := range fixtureCounts {
		s.Count[i/4][i%4] = v
		s.L2[i/4] += v * v
	}
	return s
}

func sameState(got, want *CountSketch) bool {
	return got.Rows == want.Rows && got.Cols == want.Cols &&
		got.CounterType == want.CounterType && got.Mode == want.Mode &&
		reflect.DeepEqual(got.Count, want.Count) && reflect.DeepEqual(got.L2, want.L2)
}

func TestASAPv1Golden(t *testing.T) {
	for name, known := range map[string]*CountSketch{
		"cs_i64_regular_2x4": fixtureSketch(t, CounterInt64, ModeRegular),
		"cs_i64_fast_2x4":    fixtureSketch(t, CounterInt64, ModeFast),
		"cs_i32_regular_2x4": fixtureSketch(t, CounterInt32, ModeRegular),
	} {
		t.Run(name, func(t *testing.T) { asapv1test.CheckGolden(t, name, known, sameState) })
	}
}

func TestASAPv1RoundTripAfterInserts(t *testing.T) {
	for _, mode := range []Mode{ModeFast, ModeRegular} {
		s, err := NewCountSketch(4, 64)
		if err != nil {
			t.Fatal(err)
		}
		s.CounterType, s.Mode = CounterInt64, mode
		for i := range 500 {
			s.UpdateWeight(common.FromString(fmt.Sprintf("k%d", i%37)), float64(i%5+1))
		}
		b, err := s.MarshalASAPv1()
		if err != nil {
			t.Fatal(err)
		}
		var got CountSketch
		if err := got.UnmarshalASAPv1(b); err != nil {
			t.Fatal(err)
		}
		if !sameState(&got, s) {
			t.Fatalf("mode %d: decoded state differs", mode)
		}
		key := common.FromString("k3")
		if got.Estimate(key) != s.Estimate(key) {
			t.Fatalf("mode %d: estimate %v, want %v", mode, got.Estimate(key), s.Estimate(key))
		}
	}
}

// The expected cells are the Rust sketch's after the same inserts:
// Count<Vector2D<i64>, RegularPath | FastPath>::with_dimensions(3, 16).
func TestModeMatchesRustCells(t *testing.T) {
	want := map[Mode][]float64{
		ModeRegular: {129, 0, 0, 0, 0, -100, 135, -105, 0, 0, 0, 0, 0, -111, -117, -123,
			0, 258, 100, 0, -129, 0, 0, 0, 0, 117, 105, 111, 0, 0, 0, 0,
			0, -117, 111, 0, 0, 0, 0, -129, 0, -100, 0, 0, 30, -123, 0, 0},
		ModeFast: {129, 0, 0, 0, 0, -100, 135, -105, 0, 0, 0, 0, 0, -111, -117, -123,
			0, 123, 0, 0, 0, -129, 0, 0, 135, -100, 0, 0, 0, 222, 0, 111,
			-111, 0, 0, -105, 0, -129, 0, 135, 0, 0, 0, 0, -123, 0, -17, 0},
	}
	for mode, cells := range want {
		s, err := NewCountSketch(3, 16)
		if err != nil {
			t.Fatal(err)
		}
		s.Mode = mode
		for i := range 40 {
			s.UpdateWeight(common.FromString(fmt.Sprintf("k%d", i%7)), float64(i+1))
		}
		var got []float64
		for _, row := range s.Count {
			got = append(got, row...)
		}
		if !reflect.DeepEqual(got, cells) {
			t.Fatalf("mode %d cells\n got %v\nwant %v", mode, got, cells)
		}
		if est := s.Estimate(common.FromString("k3")); mode == ModeRegular && est != 129 {
			t.Fatalf("regular estimate of k3 = %v, want 129", est)
		}
	}
}

func TestMarshalASAPv1Rejects(t *testing.T) {
	cases := map[string]func(s *CountSketch){
		"float64 counters":  func(s *CountSketch) { s.CounterType = CounterFloat64 },
		"fractional cell":   func(s *CountSketch) { s.Count[1][2] = 0.5 },
		"i32 overflow":      func(s *CountSketch) { s.CounterType = CounterInt32; s.Count[0][0] = 1 << 31 },
		"i64 overflow":      func(s *CountSketch) { s.Count[0][0] = 1 << 63 },
		"unknown mode":      func(s *CountSketch) { s.Mode = 9 },
		"unknown counter":   func(s *CountSketch) { s.CounterType = 9 },
		"ragged count rows": func(s *CountSketch) { s.Count = s.Count[:1] },
	}
	for name, mutate := range cases {
		s := fixtureSketch(t, CounterInt64, ModeFast)
		mutate(s)
		if _, err := s.MarshalASAPv1(); err == nil {
			t.Errorf("%s: MarshalASAPv1 succeeded", name)
		}
	}
	s, err := NewCountSketch()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarshalASAPv1(); err == nil || !strings.Contains(err.Error(), "float64") {
		t.Errorf("default sketch: got %v, want the float64 error", err)
	}
	for rows, ok := range map[int]bool{20: true, 21: false} {
		s, err := NewCountSketch(rows, 4)
		if err != nil {
			t.Fatal(err)
		}
		s.CounterType = CounterInt64
		if _, err := s.MarshalASAPv1(); (err == nil) != ok {
			t.Errorf("%d rows: MarshalASAPv1 error %v", rows, err)
		}
	}
}

func craft(t *testing.T, rows, cols uint64, counterType, mode string, counts []int64) []byte {
	t.Helper()
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	md.Uint("rows", rows)
	md.Uint("cols", cols)
	md.Str("counter_type", counterType)
	md.Str("mode", mode)
	p := asapv1.NewEncoder()
	p.Array(1)
	asapv1.EncodeInts(p, counts)
	b, err := asapv1.Marshal(asapv1.KindCountSketch, md, p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUnmarshalASAPv1Rejects(t *testing.T) {
	zeros := make([]int64, 8)
	cases := map[string][]byte{
		"count-min kind":   asapv1test.Golden(t, "cms_i64_regular_2x3"),
		"f64 counter_type": craft(t, 2, 4, "f64", "regular", zeros),
		"unknown mode":     craft(t, 2, 4, "i64", "nitro", zeros),
		"zero cols":        craft(t, 2, 0, "i64", "regular", nil),
		"zero rows":        craft(t, 0, 4, "i64", "regular", nil),
		"21 rows":          craft(t, 21, 4, "i64", "regular", make([]int64, 84)),
		"cols not pow2":    craft(t, 2, 3, "i64", "regular", make([]int64, 6)),
		"short counts":     craft(t, 2, 4, "i64", "regular", zeros[:7]),
		"i32 overflow":     craft(t, 2, 4, "i32", "regular", append([]int64{1 << 31}, zeros[:7]...)),
		"inexact i64":      craft(t, 2, 4, "i64", "regular", append([]int64{1<<53 + 1}, zeros[:7]...)),
	}
	for name, b := range cases {
		s := fixtureSketch(t, CounterInt32, ModeFast)
		if err := s.UnmarshalASAPv1(b); err == nil {
			t.Errorf("%s: UnmarshalASAPv1 succeeded", name)
		}
		if !sameState(s, fixtureSketch(t, CounterInt32, ModeFast)) {
			t.Errorf("%s: receiver changed on error", name)
		}
	}
}

func TestMergeRejectsModeOrCounterMismatch(t *testing.T) {
	a := fixtureSketch(t, CounterInt64, ModeFast)
	if err := a.Merge(fixtureSketch(t, CounterInt64, ModeRegular)); err == nil {
		t.Error("merged fast with regular")
	}
	if err := a.Merge(fixtureSketch(t, CounterInt32, ModeFast)); err == nil {
		t.Error("merged i64 with i32")
	}
}

func TestKeyMethodsFollowMode(t *testing.T) {
	geometries := []struct {
		mode       Mode
		rows, cols int
	}{{ModeRegular, 3, 16}, {ModeFast, 3, 16}, {ModeFast, 8, 4096}, {ModeFast, 12, 1024}}
	for _, g := range geometries {
		build := func() *CountSketch {
			s, err := NewCountSketch(g.rows, g.cols)
			if err != nil {
				t.Fatal(err)
			}
			s.CounterType, s.Mode = CounterInt64, g.mode
			return s
		}
		viaUpdate, viaString, viaGOS, viaRows, viaOcto := build(), build(), build(), build(), build()
		all := uint64(1)<<g.rows - 1
		for i := range 300 {
			key := fmt.Sprintf("k%d", i%23)
			w := float64(i%4 + 1)
			viaUpdate.UpdateWeight(common.FromString(key), w)
			viaString.UpdateString(key, w)
			viaGOS.UpdateStringGOS(key, w, 1e18)
			viaRows.UpdateStringAtRows(key, w, all, 1)
			for range int(w) {
				viaOcto.ProcessInput(common.FromString(key), 1e18, func(common.DeltaUpdate) {})
			}
		}
		for name, s := range map[string]*CountSketch{"UpdateString": viaString, "UpdateStringGOS": viaGOS,
			"UpdateStringAtRows": viaRows, "ProcessInput": viaOcto} {
			if !reflect.DeepEqual(s.Count, viaUpdate.Count) {
				t.Fatalf("mode %d %dx%d: %s cells differ from UpdateWeight", g.mode, g.rows, g.cols, name)
			}
		}
		b, err := viaString.MarshalASAPv1()
		if err != nil {
			t.Fatalf("mode %d %dx%d: %v", g.mode, g.rows, g.cols, err)
		}
		var back CountSketch
		if err := back.UnmarshalASAPv1(b); err != nil {
			t.Fatal(err)
		}
		for i := range 23 {
			key := fmt.Sprintf("k%d", i)
			want := viaUpdate.Estimate(common.FromString(key))
			if got := viaString.EstimateStringCount(key); got != int64(want) {
				t.Fatalf("mode %d %dx%d: EstimateStringCount(%s) = %d, want %v", g.mode, g.rows, g.cols, key, got, want)
			}
			if got := back.Estimate(common.FromString(key)); got != want {
				t.Fatalf("mode %d %dx%d: decoded Estimate(%s) = %v, want %v", g.mode, g.rows, g.cols, key, got, want)
			}
			if got := back.EstimateStringCount(key); got != int64(want) {
				t.Fatalf("mode %d %dx%d: decoded EstimateStringCount(%s) = %d, want %v", g.mode, g.rows, g.cols, key, got, want)
			}
		}
	}
}

func TestHashOnlyWritesThatDoNotFollowModeRefuseToMarshal(t *testing.T) {
	build := func(mode Mode, rows, cols int) *CountSketch {
		s, err := NewCountSketch(rows, cols)
		if err != nil {
			t.Fatal(err)
		}
		s.CounterType, s.Mode = CounterInt64, mode
		return s
	}
	hash := common.Hash64([]byte("k"))
	hashOnly := &common.SketchInput{Hash: hash}
	cases := []struct {
		name  string
		s     *CountSketch
		write func(s *CountSketch)
		ok    bool
	}{
		{"InsertWithHash fast packed64", build(ModeFast, 3, 16), func(s *CountSketch) { s.InsertWithHash(hash) }, true},
		{"InsertWithHash regular", build(ModeRegular, 3, 16), func(s *CountSketch) { s.InsertWithHash(hash) }, false},
		{"InsertWithHash fast packed128", build(ModeFast, 8, 4096), func(s *CountSketch) { s.InsertWithHash(hash) }, false},
		{"FastInsertWeightWithHashValue regular", build(ModeRegular, 3, 16), func(s *CountSketch) { s.FastInsertWeightWithHashValue(hash, 2) }, false},
		{"ProcessInput hash-only regular", build(ModeRegular, 3, 16), func(s *CountSketch) { s.ProcessInput(hashOnly, 1e18, func(common.DeltaUpdate) {}) }, false},
		{"UpdateCell hash-only fast rows", build(ModeFast, 12, 1024), func(s *CountSketch) { s.UpdateCell(0, s.ColForRow(hashOnly, 0), hashOnly) }, false},
		{"ProcessInput with bytes regular", build(ModeRegular, 3, 16), func(s *CountSketch) { s.ProcessInput(common.FromString("k"), 1e18, func(common.DeltaUpdate) {}) }, true},
	}
	for _, c := range cases {
		c.write(c.s)
		if _, err := c.s.MarshalASAPv1(); (err == nil) != c.ok {
			t.Errorf("%s: MarshalASAPv1 error %v", c.name, err)
		}
	}

	dirty := build(ModeRegular, 3, 16)
	dirty.InsertWithHash(hash)
	clean := build(ModeRegular, 3, 16)
	if err := clean.Merge(dirty); err != nil {
		t.Fatal(err)
	}
	if _, err := clean.MarshalASAPv1(); err == nil {
		t.Error("Merge dropped the hash-only write mark")
	}
	clean.Reset()
	if _, err := clean.MarshalASAPv1(); err != nil {
		t.Errorf("after Reset: %v", err)
	}
}

// rustNonPow2Cols is Count<Vector2D<i64>, RegularPath>::with_dimensions(2, 3)
// after one insert of the string "a", serialized by the Rust crate.
const rustNonPow2Cols = "4153415076310102040000000156000000088bb06d657461646174615f76657273696f6e01af686173685f70726f66696c655f6964bc70726f6a656374617361702e787868332e736565646c6973742e7631ae686173685f616c676f726974686dab787868335f36345f313238af736565645f64657269766174696f6eb4736565645f6c6973745f696e6465785f77726170ae696e7075745f656e636f64696e67b470726f6a656374617361702e696e7075742e7631a9736565645f6c697374dc0014cecafe3553cf000000ade3415118ce8cc70208ce2f024b2bce451a3df5ce6a09e667cebb67ae85ce3c6ef372cea54ff53ace510e527fce9b05688cce1f83d9abce5be0cd19cecbbb9d5dce629a292ace9159015ace152fecd8ce67332667ce8eb44a87cedb0c2e0db16d61747269785f736565645f696e64657800a4726f777302a4636f6c7303ac636f756e7465725f74797065a3693634a46d6f6465a7726567756c6172919600010000ff00"

func TestUnmarshalASAPv1RejectsNonPowerOfTwoCols(t *testing.T) {
	b, err := hex.DecodeString(rustNonPow2Cols)
	if err != nil {
		t.Fatal(err)
	}
	var s CountSketch
	err = s.UnmarshalASAPv1(b)
	if err == nil || !strings.Contains(err.Error(), "cols 3 is not a power of two") {
		t.Fatalf("got %v, want the power-of-two error", err)
	}
}
