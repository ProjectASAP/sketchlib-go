package ddsketch

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/common/storage"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

const testAlpha = 0.01

// literal builds a sketch from raw wire state, with count summed here rather
// than by the codec.
func literal(alpha float64, pos []uint64, posOffset int32, neg []uint64, negOffset int32,
	zero uint64, sum, minV, maxV float64) *DDSketch {
	d := NewDDSketch(alpha)
	if len(pos) > 0 {
		d.store = Buckets{counts: storage.Vector1DFromVec(pos), offset: posOffset}
	}
	if len(neg) > 0 {
		d.negative = Buckets{counts: storage.Vector1DFromVec(neg), offset: negOffset}
	}
	d.zeroCount = zero
	d.count = zero
	for _, c := range append(append([]uint64(nil), pos...), neg...) {
		d.count += c
	}
	d.sum, d.min, d.max = sum, minV, maxV
	d.gosPopulated = populatedBucketCount(&d.store)
	return d
}

func sameBuckets(a, b *Buckets) bool {
	if a.IsEmpty() || b.IsEmpty() {
		return a.IsEmpty() && b.IsEmpty()
	}
	x, y := a.counts.AsSlice(), b.counts.AsSlice()
	if a.offset != b.offset || len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func sameFloat(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

func sameState(got, want *DDSketch) bool {
	return sameFloat(got.mapping.alpha, want.mapping.alpha) &&
		sameFloat(got.mapping.gamma, want.mapping.gamma) &&
		sameFloat(got.mapping.invLogGamma, want.mapping.invLogGamma) &&
		sameBuckets(&got.store, &want.store) &&
		sameBuckets(&got.negative, &want.negative) &&
		got.zeroCount == want.zeroCount &&
		got.count == want.count &&
		sameFloat(got.sum, want.sum) &&
		sameFloat(got.min, want.min) &&
		sameFloat(got.max, want.max)
}

func signedFixture() *DDSketch {
	return literal(testAlpha, []uint64{3, 0, 2}, 310, []uint64{5, 1}, -208, 7, 2523.90625, -0.016, 515.0)
}

func TestASAPv1Golden(t *testing.T) {
	cases := map[string]*DDSketch{
		"ddsketch_empty_a001": NewDDSketch(testAlpha),
		"ddsketch_positive_a001": literal(testAlpha, []uint64{1, 0, 127, 128, 300, 65536, 4294967296}, -40,
			nil, 0, 0, 2181071000.0, 0.453125, 0.5078125),
		"ddsketch_signed_a001": signedFixture(),
	}
	for name, known := range cases {
		t.Run(name, func(t *testing.T) { asapv1test.CheckGolden(t, name, known, sameState) })
	}
}

func metadataVersion(t *testing.T, b []byte) uint8 {
	t.Helper()
	_, metadata, _, err := asapv1.Split(b)
	if err != nil {
		t.Fatal(err)
	}
	md, err := asapv1.ReadMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	return md.Version()
}

func roundTrip(t *testing.T, d *DDSketch) *DDSketch {
	t.Helper()
	b, err := d.MarshalASAPv1()
	if err != nil {
		t.Fatalf("MarshalASAPv1: %v", err)
	}
	var got DDSketch
	if err := got.UnmarshalASAPv1(b); err != nil {
		t.Fatalf("UnmarshalASAPv1: %v", err)
	}
	if !sameState(&got, d) {
		t.Fatalf("decoded state differs\n got: %+v\nwant: %+v", got, *d)
	}
	again, err := got.MarshalASAPv1()
	if err != nil {
		t.Fatalf("re-MarshalASAPv1: %v", err)
	}
	asapv1test.Equal(t, again, b)
	return &got
}

func TestASAPv1RoundTripAfterUpdates(t *testing.T) {
	d := NewDDSketch(testAlpha)
	for i := 1; i <= 2000; i++ {
		d.Update(float64(i) * 0.37)
	}
	b, err := d.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	if v := metadataVersion(t, b); v != 1 {
		t.Fatalf("metadata_version %d, want 1", v)
	}
	got := roundTrip(t, d)
	for _, q := range []float64{0, 0.01, 0.25, 0.5, 0.9, 0.99, 1} {
		gv, _ := got.Quantile(q)
		wv, _ := d.Quantile(q)
		if gv != wv {
			t.Errorf("q=%v: decoded %v, original %v", q, gv, wv)
		}
	}
	got.Update(100)
	if got.Count() != d.Count()+1 {
		t.Fatalf("Update after decode: count %d, want %d", got.Count(), d.Count()+1)
	}
}

func TestASAPv1EmptyRoundTrip(t *testing.T) {
	got := roundTrip(t, NewDDSketch(testAlpha))
	if _, ok := got.Min(); ok || got.Count() != 0 {
		t.Fatalf("decoded empty sketch: count %d, min ok %v", got.Count(), ok)
	}
	got.Update(42)
	if got.Count() != 1 {
		t.Fatalf("Update after decode: count %d, want 1", got.Count())
	}
	if q, _ := got.Quantile(0); q != 42 {
		t.Fatalf("min after decode and Update: %v, want 42", q)
	}
}

func TestASAPv1ClearedSketchEncodesAsEmpty(t *testing.T) {
	positive := NewDDSketch(testAlpha)
	for i := 1; i <= 100; i++ {
		positive.Update(float64(i))
	}
	for _, d := range []*DDSketch{positive, signedFixture()} {
		d.Clear()
		got, err := d.MarshalASAPv1()
		if err != nil {
			t.Fatal(err)
		}
		asapv1test.Equal(t, got, asapv1test.Golden(t, "ddsketch_empty_a001"))
	}
}

func TestASAPv1ZeroCountAloneSelectsVersion2(t *testing.T) {
	d := literal(testAlpha, []uint64{2}, 40, nil, 0, 3, 3, 0, 1.5)
	b, err := d.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	if v := metadataVersion(t, b); v != 2 {
		t.Fatalf("metadata_version %d, want 2", v)
	}
	roundTrip(t, d)
}

func TestASAPv1MergedRoundTrip(t *testing.T) {
	left, right := NewDDSketch(testAlpha), NewDDSketch(testAlpha)
	for i := 1; i <= 200; i++ {
		left.Update(float64(i))
		right.Update(float64(i) * 0.001)
	}
	if err := left.Merge(right); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, left)

	signed := signedFixture()
	if err := left.Merge(signed); err != nil {
		t.Fatal(err)
	}
	b, err := left.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	if v := metadataVersion(t, b); v != 2 {
		t.Fatalf("metadata_version %d after merging a signed sketch, want 2", v)
	}
	got := roundTrip(t, left)
	if got.Count() != 400+18 || got.zeroCount != 7 {
		t.Fatalf("merged count %d zero %d, want 418 and 7", got.Count(), got.zeroCount)
	}
	if lo, _ := got.Min(); lo != -0.016 {
		t.Fatalf("merged min %v, want -0.016", lo)
	}
}

func TestASAPv1CollapsedStoreRoundTrip(t *testing.T) {
	const maxBins = 50
	d := NewDDSketchWithMaxBins(testAlpha, maxBins)
	for exp := -20; exp <= 20; exp++ {
		for i := 0; i < 10; i++ {
			d.Update(math.Pow(1.5, float64(exp)))
		}
	}
	d.Update(1e15)
	if got := d.store.counts.Len(); got > maxBins {
		t.Fatalf("store span %d exceeds maxBins %d", got, maxBins)
	}
	got := roundTrip(t, d)
	if got.store.maxBins != 0 {
		t.Fatalf("decoded maxBins %d, want 0 (unbounded)", got.store.maxBins)
	}
}

func TestASAPv1SignedQuantileWalk(t *testing.T) {
	var d DDSketch
	if err := d.UnmarshalASAPv1(asapv1test.Golden(t, "ddsketch_signed_a001")); err != nil {
		t.Fatal(err)
	}
	if d.Count() != 18 {
		t.Fatalf("count %d, want 18", d.Count())
	}
	m := d.mapping
	// Ranks 1, 2-6, 7-13, 14-16, 17-18 fall in buckets -207, -208, zero, 310, 312;
	// values are capped to [min, max] = [-0.016, 515].
	want := map[int]float64{
		1:  -0.016,
		2:  -m.Value(-208),
		6:  -m.Value(-208),
		7:  0,
		13: 0,
		14: m.Value(310),
		16: m.Value(310),
		17: 515.0,
	}
	for rank, w := range want {
		q := (float64(rank) - 0.5) / 18
		if got, ok := d.Quantile(q); !ok || got != w {
			t.Errorf("rank %d (q=%v): got %v, want %v", rank, q, got, w)
		}
	}
}

func TestUpdateSumSaturates(t *testing.T) {
	d := NewDDSketch(testAlpha)
	v := d.mapping.MaxIndexableValue()
	d.Update(v)
	d.Update(v)
	d.Update(v)
	if d.sum != math.MaxFloat64 {
		t.Fatalf("sum %v, want MaxFloat64", d.sum)
	}
	roundTrip(t, d)
}

func craft(version uint8, alpha float64, payload func(p *asapv1.Encoder)) []byte {
	md := asapv1.NewMetadataWriter(version)
	md.Float64("alpha", alpha)
	p := asapv1.NewEncoder()
	payload(p)
	b, err := asapv1.Marshal(asapv1.KindDDSketch, md, p)
	if err != nil {
		panic(err)
	}
	return b
}

func v1Payload(counts []uint64, offset int64, sum, minV, maxV float64) func(p *asapv1.Encoder) {
	return func(p *asapv1.Encoder) {
		p.Array(5)
		asapv1.EncodeUints(p, counts)
		p.Int(offset)
		p.Float64(sum)
		p.Float64(minV)
		p.Float64(maxV)
	}
}

func v2Payload(counts []uint64, offset int64, sum, minV, maxV float64, neg []uint64, negOffset int64, zero uint64) func(p *asapv1.Encoder) {
	return func(p *asapv1.Encoder) {
		p.Array(8)
		asapv1.EncodeUints(p, counts)
		p.Int(offset)
		p.Float64(sum)
		p.Float64(minV)
		p.Float64(maxV)
		asapv1.EncodeUints(p, neg)
		p.Int(negOffset)
		p.Uint(zero)
	}
}

func TestASAPv1RejectsCraftedBytes(t *testing.T) {
	inf := math.Inf(1)
	valid := v1Payload([]uint64{3}, 7, 6, 1.5, 2.5)
	foreign := func() []byte {
		md := asapv1.NewMetadataWriter(1)
		md.Float64("alpha", testAlpha)
		p := asapv1.NewEncoder()
		valid(p)
		b, _ := asapv1.Marshal(asapv1.KindKLL, md, p)
		return b
	}()
	withMetadata := func(write func(md *asapv1.MetadataWriter)) []byte {
		md := asapv1.NewMetadataWriter(1)
		write(md)
		p := asapv1.NewEncoder()
		valid(p)
		b, _ := asapv1.Marshal(asapv1.KindDDSketch, md, p)
		return b
	}

	cases := []struct {
		name string
		b    []byte
		want string
	}{
		{"foreign kind_id", foreign, ""},
		{"metadata version 3", craft(3, testAlpha, valid), ""},
		{"unknown metadata key", withMetadata(func(md *asapv1.MetadataWriter) {
			md.Float64("alpha", testAlpha)
			md.Uint("bogus", 7)
		}), ""},
		{"missing alpha", withMetadata(func(md *asapv1.MetadataWriter) {}), ""},
		{"alpha 0", craft(1, 0, valid), "alpha"},
		{"alpha 1", craft(1, 1, valid), "alpha"},
		{"alpha negative", craft(1, -0.5, valid), "alpha"},
		{"alpha 2", craft(1, 2, valid), "alpha"},
		{"alpha NaN", craft(1, math.NaN(), valid), "alpha"},
		{"alpha +Inf", craft(1, inf, valid), "alpha"},
		{"v1 with eight fields", craft(1, testAlpha, v2Payload([]uint64{3}, 7, 6, 1.5, 2.5, nil, 0, 1)), ""},
		{"v2 with five fields", craft(2, testAlpha, valid), ""},
		{"offset past int32", craft(1, testAlpha, v1Payload([]uint64{3}, 1<<40, 6, 1.5, 2.5)), ""},
		{"span past int32", craft(1, testAlpha, v1Payload([]uint64{1, 1, 1, 1}, math.MaxInt32-2, 5, 1, 2)), "span past int32"},
		{"negative span past int32", craft(2, testAlpha,
			v2Payload([]uint64{1}, 0, 5, -2, 2, []uint64{1, 1, 1, 1}, math.MaxInt32-2, 0)), "span past int32"},
		{"empty store at nonzero offset", craft(1, testAlpha, v1Payload(nil, 42, 0, inf, -inf)), "offset 0"},
		{"empty negative store at nonzero offset", craft(2, testAlpha,
			v2Payload([]uint64{1}, 0, 1, 0, 1, nil, 42, 1)), "offset 0"},
		{"total overflow", craft(1, testAlpha, v1Payload([]uint64{math.MaxUint64, 1}, 0, 5, 1, 2)), "overflow"},
		{"signed total overflow", craft(2, testAlpha,
			v2Payload([]uint64{1}, 0, 5, -1, 2, []uint64{1}, 0, math.MaxUint64-1)), "overflow"},
		{"populated with empty sentinels", craft(1, testAlpha, v1Payload([]uint64{3}, 7, 6, inf, -inf)), "finite"},
		{"min above max", craft(1, testAlpha, v1Payload([]uint64{3}, 7, 6, 9, 2)), "out of order"},
		{"nonpositive min", craft(1, testAlpha, v1Payload([]uint64{3}, 7, 6, 0, 2)), "out of order"},
		{"sum below min", craft(1, testAlpha, v1Payload([]uint64{3}, 7, 0.5, 1.5, 2.5)), "out of order"},
		{"empty with nonzero sum", craft(1, testAlpha, v1Payload(nil, 0, 5, inf, -inf)), "empty sketch"},
		{"zero buckets with populated scalars", craft(1, testAlpha, v1Payload([]uint64{0, 0}, 7, 6, 1.5, 2.5)), "empty sketch"},
		{"signed min above max", craft(2, testAlpha, v2Payload([]uint64{1}, 0, 5, 3, -1, []uint64{1}, 0, 0)), "above max"},
		{"signed infinite sum", craft(2, testAlpha, v2Payload(nil, 0, inf, 0, 0, nil, 0, 1)), "finite"},
		{"v2 positive-only sum below min", craft(2, testAlpha, v2Payload([]uint64{3}, 7, 0.5, 1.5, 2.5, nil, 0, 0)), "out of order"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDDSketch(0.05)
			d.Update(3)
			before := d.Clone()
			err := d.UnmarshalASAPv1(tc.b)
			if err == nil {
				t.Fatal("decode succeeded")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
			if !sameState(d, before) {
				t.Fatal("receiver changed on a failed decode")
			}
		})
	}

	if err := new(DDSketch).UnmarshalASAPv1(craft(1, testAlpha, valid)); err != nil {
		t.Fatalf("control payload rejected: %v", err)
	}
	boundary := craft(1, testAlpha, v1Payload([]uint64{1, 1, 1}, math.MaxInt32-2, 5, 1, 2))
	if err := new(DDSketch).UnmarshalASAPv1(boundary); err != nil {
		t.Fatalf("store ending at MaxInt32 rejected: %v", err)
	}
}

func TestASAPv1PositiveOnlyVersion2DecodesAndEncodesAsVersion1(t *testing.T) {
	var d DDSketch
	if err := d.UnmarshalASAPv1(craft(2, testAlpha, v2Payload([]uint64{3}, 7, 6, 1.5, 2.5, nil, 0, 0))); err != nil {
		t.Fatal(err)
	}
	got, err := d.MarshalASAPv1()
	if err != nil {
		t.Fatal(err)
	}
	asapv1test.Equal(t, got, craft(1, testAlpha, v1Payload([]uint64{3}, 7, 6, 1.5, 2.5)))
}

func TestASAPv1MarshalRejectsUndecodableState(t *testing.T) {
	cases := map[string]*DDSketch{
		"span past int32":     literal(testAlpha, []uint64{1, 1, 1, 1}, math.MaxInt32-2, nil, 0, 0, 5, 1, 2),
		"total overflow":      literal(testAlpha, []uint64{math.MaxUint64, 1}, 0, nil, 0, 0, 5, 1, 2),
		"infinite sum":        literal(testAlpha, []uint64{1}, 0, nil, 0, 0, math.Inf(1), 1, 2),
		"sum below min":       literal(testAlpha, []uint64{3}, 7, nil, 0, 0, 0.5, 1.5, 2.5),
		"drained to empty":    literal(testAlpha, []uint64{0, 0}, 7, nil, 0, 0, 6, 1.5, 2.5),
		"signed min > max":    literal(testAlpha, []uint64{1}, 0, []uint64{1}, 0, 0, 5, 3, -1),
		"signed span":         literal(testAlpha, []uint64{1}, 0, []uint64{1, 1, 1, 1}, math.MaxInt32-2, 0, 5, -2, 2),
		"zero count with NaN": literal(testAlpha, nil, 0, nil, 0, 1, math.NaN(), 0, 0),
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			if b, err := d.MarshalASAPv1(); err == nil {
				t.Fatalf("marshal succeeded: %x", b)
			}
		})
	}
}

func TestQuantileCapsAtCarriedMax(t *testing.T) {
	d := NewDDSketch(testAlpha)
	d.Update(1.0)
	for _, q := range []float64{0.5, 1} {
		if got, _ := d.Quantile(q); got != 1.0 {
			t.Fatalf("Quantile(%v) = %v, want 1.0", q, got)
		}
	}

	top := NewDDSketch(testAlpha)
	v := top.mapping.LowerBound(11) * 0.9999999
	top.Update(top.mapping.LowerBound(10))
	top.Update(v)
	if got, _ := top.Quantile(1); got != v {
		t.Fatalf("Quantile(1) = %v, want max %v", got, v)
	}
}

// TestSignedFixtureQuantilesMatchRust compares against the values Rust's
// get_value_at_quantile returns for the decoded signed fixture; math.Pow and
// Rust's powf can differ in the last bits, so bucket values get a tolerance.
func TestSignedFixtureQuantilesMatchRust(t *testing.T) {
	var d DDSketch
	if err := d.UnmarshalASAPv1(asapv1test.Golden(t, "ddsketch_signed_a001")); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[float64]float64{
		0.05: -0.016, 0.1: -0.01576144762907453, 0.4: 0, 0.75: 497.77940145581556, 0.9: 515.0, 1: 515.0,
	} {
		got, _ := d.Quantile(q)
		if math.Abs(got-want) > 1e-13*math.Abs(want) {
			t.Errorf("Quantile(%v) = %v, want %v", q, got, want)
		}
	}
}

func TestMappingUsesConstructorAlpha(t *testing.T) {
	for _, alpha := range []float64{0.001, 0.01, 0.05, 0.3} {
		m := NewIndexMapping(alpha)
		if m.RelativeAccuracy() != alpha || m.Value(0) != 1+alpha {
			t.Errorf("alpha %v: RelativeAccuracy %v, Value(0) %v", alpha, m.RelativeAccuracy(), m.Value(0))
		}
	}
}

func TestASAPv1EmptiedWorkerEncodesAsEmpty(t *testing.T) {
	fill := func() *DDSketch {
		d := NewDDSketch(testAlpha)
		d.Update(5)
		d.Update(5)
		return d
	}
	drained := fill()
	drained.DrainBuckets(func(int32, uint64) {})
	reset := fill()
	reset.ResetBucket(reset.BucketIndex(5))
	crossed := NewDDSketch(testAlpha)
	crossed.UpdateGOS(5, 1)

	empty := NewDDSketch(testAlpha)
	for name, d := range map[string]*DDSketch{"DrainBuckets": drained, "ResetBucket": reset, "UpdateGOS": crossed} {
		t.Run(name, func(t *testing.T) {
			b, err := d.MarshalASAPv1()
			if err != nil {
				t.Fatalf("MarshalASAPv1: %v", err)
			}
			var got DDSketch
			if err := got.UnmarshalASAPv1(b); err != nil {
				t.Fatalf("UnmarshalASAPv1: %v", err)
			}
			if got.count != 0 || !sameFloat(got.sum, empty.sum) ||
				!sameFloat(got.min, empty.min) || !sameFloat(got.max, empty.max) {
				t.Fatalf("decoded count %d sum %v min %v max %v, want the empty state",
					got.count, got.sum, got.min, got.max)
			}
		})
	}
}

func TestASAPv1SumAfterBucketInserts(t *testing.T) {
	d := NewDDSketch(testAlpha)
	d.AddToBucket(10, 3)
	d.addOneFast(10)
	d.addOneFast(20)
	m := d.mapping
	want := 0.0
	want += m.Value(10) * 3
	want += m.Value(10)
	want += m.Value(20)
	got := roundTrip(t, d)
	if !sameFloat(got.sum, want) {
		t.Fatalf("encoded sum %v, want %v", got.sum, want)
	}
}

func TestMergeSumSaturates(t *testing.T) {
	a, b := NewDDSketch(testAlpha), NewDDSketch(testAlpha)
	v := a.mapping.MaxIndexableValue()
	for _, d := range []*DDSketch{a, b} {
		d.Update(v)
		d.Update(v)
	}
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if a.sum != math.MaxFloat64 {
		t.Fatalf("merged sum %v, want MaxFloat64", a.sum)
	}
	roundTrip(t, a)
}

func TestASAPv1EncodesAggregatorFedByAddToBucket(t *testing.T) {
	src := NewDDSketch(testAlpha)
	for i := 1; i <= 500; i++ {
		src.Update(float64(i))
	}
	agg := NewDDSketch(testAlpha)
	src.EachBucket(func(k int32, c uint64) { agg.AddToBucket(k, c) })
	got := roundTrip(t, agg)
	if !reflect.DeepEqual(got.LocalBuckets(), src.LocalBuckets()) {
		t.Fatal("aggregator buckets differ from the source buckets")
	}
}

func TestSignedStateSurvivesCloneAndMergeIntoEmpty(t *testing.T) {
	want := signedFixture()
	if got := want.Clone(); !sameState(got, want) {
		t.Fatalf("Clone lost signed state: %+v", got)
	}
	empty := NewDDSketch(testAlpha)
	if err := empty.Merge(want); err != nil {
		t.Fatal(err)
	}
	if !sameState(empty, want) {
		t.Fatalf("Merge into empty lost signed state: %+v", empty)
	}
}
