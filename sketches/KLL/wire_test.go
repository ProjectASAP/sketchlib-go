package kll

import (
	"bytes"
	"math"
	"math/rand"
	"slices"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

func sameState(a, b *KLLSketch) bool {
	if a.k != b.k || a.m != b.m || a.numLevels != b.numLevels || a.co != b.co ||
		a.seed != b.seed || a.seedSet != b.seedSet || a.capacityCache != b.capacityCache ||
		a.topHeight != b.topHeight || a.level0Cap != b.level0Cap || !slices.Equal(a.levels, b.levels) {
		return false
	}
	return slices.EqualFunc(a.items, b.items, func(x, y float64) bool {
		return math.Float64bits(x) == math.Float64bits(y)
	})
}

func seededSketch(t *testing.T, seed int64, values []float64) *KLLSketch {
	t.Helper()
	s, err := NewKLLSketchWithSeed(200, seed)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		s.Update(v)
	}
	return s
}

func randomValues(n int, src int64) []float64 {
	rng := rand.New(rand.NewSource(src))
	out := make([]float64, n)
	for i := range out {
		out[i] = rng.Float64() * 1_000_000
	}
	return out
}

func roundTrip(t *testing.T, s *KLLSketch) (*KLLSketch, []byte) {
	t.Helper()
	b, err := s.MarshalASAPv1()
	if err != nil {
		t.Fatalf("MarshalASAPv1: %v", err)
	}
	var got KLLSketch
	if err := got.UnmarshalASAPv1(b); err != nil {
		t.Fatalf("UnmarshalASAPv1: %v", err)
	}
	again, err := got.MarshalASAPv1()
	if err != nil {
		t.Fatalf("re-MarshalASAPv1: %v", err)
	}
	asapv1test.Equal(t, again, b)
	if !sameState(&got, s) {
		t.Fatalf("decoded state differs\n got: %+v\nwant: %+v", got, *s)
	}
	return &got, b
}

func TestASAPv1Golden(t *testing.T) {
	known := InitWithSeed(200, 8, 42)
	known.seed, known.seedSet = 0, false
	for _, v := range []float64{2.5, -1.0, 0.0, 1e300, -0.125, 42.0, 3.0e-5} {
		known.Update(v)
	}
	asapv1test.CheckGolden(t, "kll_dynamic_f64_k200", known, sameState)
}

func TestASAPv1RejectsOtherKLLFixtures(t *testing.T) {
	for _, name := range []string{"kll_dynamic_i64_k200", "kll_f64_k200", "kll_i64_k200"} {
		s := seededSketch(t, 7, []float64{1, 2, 3})
		before, _ := s.MarshalASAPv1()
		if err := s.UnmarshalASAPv1(asapv1test.Golden(t, name)); err == nil {
			t.Errorf("%s: decoded, want an error", name)
		}
		after, _ := s.MarshalASAPv1()
		if !bytes.Equal(before, after) {
			t.Errorf("%s: failed decode changed the receiver", name)
		}
	}
}

func TestASAPv1RoundTrip(t *testing.T) {
	unseeded, err := NewKLLSketch(200)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range randomValues(5000, 3) {
		unseeded.Update(v)
	}
	ints := make([]float64, 5000)
	for i := range ints {
		ints[i] = float64(i + 1)
	}
	for name, s := range map[string]*KLLSketch{
		"empty":     seededSketch(t, 1, nil),
		"seeded":    seededSketch(t, 42, randomValues(5000, 7)),
		"integers":  seededSketch(t, 42, ints),
		"unseeded":  unseeded,
		"neg_seed":  seededSketch(t, -5, randomValues(3000, 9)),
		"decimals":  seededSketch(t, 99, []float64{100.0, 100.001, 100.002, math.Pi}),
		"specials":  seededSketch(t, 2, []float64{math.Inf(1), math.Inf(-1), math.Copysign(0, -1), math.MaxFloat64}),
		"one_value": seededSketch(t, 3, []float64{-7}),
	} {
		t.Run(name, func(t *testing.T) {
			got, _ := roundTrip(t, s)
			if got.Count() != s.Count() {
				t.Fatalf("count %d, want %d", got.Count(), s.Count())
			}
			for _, q := range []float64{0, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99, 1} {
				if g, w := got.Quantile(q), s.Quantile(q); math.Float64bits(g) != math.Float64bits(w) {
					t.Fatalf("quantile(%v) = %v, want %v", q, g, w)
				}
			}
		})
	}
}

func TestASAPv1SeedKey(t *testing.T) {
	metadata := func(s *KLLSketch) *asapv1.MetadataReader {
		b, err := s.MarshalASAPv1()
		if err != nil {
			t.Fatal(err)
		}
		_, md, _, err := asapv1.Split(b)
		if err != nil {
			t.Fatal(err)
		}
		r, err := asapv1.ReadMetadata(md)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if md := metadata(seededSketch(t, 42, nil)); md.Uint64("seed") != 42 || md.Err() != nil {
		t.Fatalf("seeded sketch: seed key missing or wrong: %v", md.Err())
	}
	if md := metadata(seededSketch(t, -1, nil)); md.Uint64("seed") != math.MaxUint64 || md.Err() != nil {
		t.Fatalf("seed -1 must encode as its u64 bit pattern: %v", md.Err())
	}
	unseeded, _ := NewKLLSketch(200)
	if metadata(unseeded).Has("seed") {
		t.Fatal("unseeded sketch must omit the seed key")
	}
}

func TestASAPv1ClearAfterDecodeStaysDeterministic(t *testing.T) {
	src := seededSketch(t, 42, randomValues(5000, 11))
	a, _ := roundTrip(t, src)
	b := seededSketch(t, 42, nil)
	a.Clear()
	b.Clear()
	for _, v := range randomValues(3000, 12) {
		a.Update(v)
		b.Update(v)
	}
	ab, _ := a.MarshalASAPv1()
	bb, _ := b.MarshalASAPv1()
	asapv1test.Equal(t, ab, bb)
}

func TestASAPv1DecodeContinuesCompaction(t *testing.T) {
	values := randomValues(8000, 21)
	whole := seededSketch(t, 5, values)
	half, _ := roundTrip(t, seededSketch(t, 5, values[:4000]))
	for _, v := range values[4000:] {
		half.Update(v)
	}
	if !sameState(half, whole) {
		t.Fatal("a decoded sketch fed the rest of the stream diverged from one fed it all")
	}
}

type wireCase struct {
	k, m, version uint64
	itemType      string
	extraKey      bool
	levels        []uint64
	items         []float64
	coin          [3]uint64
	trailing      bool
}

func validCase() wireCase {
	return wireCase{k: 200, m: 8, version: 1, itemType: "f64", levels: []uint64{0, 2},
		items: []float64{1, 2}, coin: [3]uint64{42, 0, 0}}
}

func (c wireCase) bytes(t *testing.T) []byte {
	t.Helper()
	md := asapv1.NewMetadataWriter(uint8(c.version))
	md.Uint("k", c.k)
	md.Uint("m", c.m)
	md.Str("item_type", c.itemType)
	if c.extraKey {
		md.Uint("bogus", 1)
	}
	p := asapv1.NewEncoder()
	p.Array(3)
	asapv1.EncodeUints(p, c.levels)
	asapv1.EncodeFloat64s(p, c.items)
	asapv1.EncodeUints(p, c.coin[:])
	if c.trailing {
		p.Uint(0)
	}
	b, err := asapv1.Marshal(asapv1.KindKLLDynamic, md, p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestASAPv1DecodeRules(t *testing.T) {
	var s KLLSketch
	if err := s.UnmarshalASAPv1(validCase().bytes(t)); err != nil {
		t.Fatalf("valid case rejected: %v", err)
	}
	tooMany := make([]uint64, maxLevels+2)
	overflow := make([]uint64, maxLevels+1)
	for i := 1; i < len(overflow); i++ {
		overflow[i] = 16
	}
	for name, edit := range map[string]func(*wireCase){
		"metadata_version": func(c *wireCase) { c.version = 2 },
		"item_type_i64":    func(c *wireCase) { c.itemType = "i64" },
		"unknown_key":      func(c *wireCase) { c.extraKey = true },
		"m_below_2":        func(c *wireCase) { c.m = 1 },
		"m_above_k":        func(c *wireCase) { c.k, c.m = 8, 9 },
		"k_too_large":      func(c *wireCase) { c.k = maxCacheableK + 1 },
		"k_u32_max":        func(c *wireCase) { c.k, c.m = math.MaxUint32, math.MaxUint32 },
		"k_above_u32":      func(c *wireCase) { c.k = math.MaxUint32 + 1 },
		"levels_short":     func(c *wireCase) { c.levels, c.items = []uint64{0}, nil },
		"levels_too_many":  func(c *wireCase) { c.levels, c.items = tooMany, nil },
		"levels_0_nonzero": func(c *wireCase) { c.levels = []uint64{1, 2} },
		"levels_decrease":  func(c *wireCase) { c.levels = []uint64{0, 2, 1} },
		"levels_last":      func(c *wireCase) { c.levels = []uint64{0, 3} },
		"level_above_u32":  func(c *wireCase) { c.levels = []uint64{0, math.MaxUint32 + 1} },
		"remaining_bits":   func(c *wireCase) { c.coin[2] = 65 },
		"weighted_count":   func(c *wireCase) { c.levels, c.items = overflow, make([]float64, 16) },
		"trailing_payload": func(c *wireCase) { c.trailing = true },
	} {
		t.Run(name, func(t *testing.T) {
			c := validCase()
			edit(&c)
			s := seededSketch(t, 7, []float64{9, 8})
			want := *s
			if err := s.UnmarshalASAPv1(c.bytes(t)); err == nil {
				t.Fatal("decoded, want an error")
			}
			if !sameState(s, &want) {
				t.Fatal("failed decode changed the receiver")
			}
		})
	}
}

func TestASAPv1RemainingBits64Accepted(t *testing.T) {
	c := validCase()
	c.coin = [3]uint64{1, math.MaxUint64, 64}
	b := c.bytes(t)
	var s KLLSketch
	if err := s.UnmarshalASAPv1(b); err != nil {
		t.Fatal(err)
	}
	again, err := s.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	asapv1test.Equal(t, again, b)
}

func TestASAPv1MarshalRejectsInvalidState(t *testing.T) {
	s := seededSketch(t, 1, []float64{1, 2, 3})
	s.levels = []int{0, 2}
	if _, err := s.MarshalASAPv1(); err == nil {
		t.Fatal("marshalled a layout the decoder rejects")
	}
}

func TestASAPv1NestedPayload(t *testing.T) {
	a := seededSketch(t, 42, randomValues(2000, 31))
	a.seed, a.seedSet = 0, false
	b := InitWithSeed(200, 8, 9)
	b.seed, b.seedSet = 0, false
	b.Update(-5)

	whole, err := a.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	_, _, payload, err := asapv1.Split(whole)
	if err != nil {
		t.Fatal(err)
	}
	cell := asapv1.NewEncoder()
	if err := a.EncodeASAPv1Payload(cell); err != nil {
		t.Fatal(err)
	}
	asapv1test.Equal(t, cell.Bytes(), payload)

	grid := asapv1.NewEncoder()
	grid.Array(2)
	for _, s := range []*KLLSketch{a, b} {
		if err := s.EncodeASAPv1Payload(grid); err != nil {
			t.Fatal(err)
		}
	}
	cellMD := asapv1.NewMetadataWriter(1)
	cellMD.Uint("k", 200)
	cellMD.Uint("m", 8)
	cellMD.Str("item_type", "f64")
	d := asapv1.NewDecoder(grid.Bytes())
	d.ExpectArray(2)
	for i, want := range []*KLLSketch{a, b} {
		md, err := asapv1.ReadMetadata(cellMD.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		var got KLLSketch
		if err := got.DecodeASAPv1Payload(md, d); err != nil {
			t.Fatalf("cell %d: %v", i, err)
		}
		if !sameState(&got, want) {
			t.Fatalf("cell %d: decoded state differs", i)
		}
	}
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}
}
