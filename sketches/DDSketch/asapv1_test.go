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
func literal(alpha float64, pos []uint64, posOffset int32, neg []uint64, negOffset int32, zero uint64) *DDSketch {
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
		got.count == want.count
}

func signedFixture() *DDSketch {
	return literal(testAlpha, []uint64{3, 0, 2}, 310, []uint64{5, 1}, -208, 7)
}

func positiveFixture() *DDSketch {
	return literal(testAlpha, []uint64{1, 0, 127, 128, 300, 65536, 4294967296}, -40, nil, 0, 0)
}

func TestASAPv1Golden(t *testing.T) {
	cases := map[string]*DDSketch{
		"ddsketch_buckets_empty_a001":    NewDDSketch(testAlpha),
		"ddsketch_buckets_positive_a001": positiveFixture(),
		"ddsketch_buckets_signed_a001":   signedFixture(),
	}
	for name, known := range cases {
		t.Run(name, func(t *testing.T) { asapv1test.CheckGolden(t, name, known, sameState) })
	}
}

// The retired 05 00 fixtures decode, with sum / min / max dropped, into the
// state of their 05 01 twins and re-encode as those twins' bytes.
func TestASAPv1RetiredFixturesDecodeToBucketTwins(t *testing.T) {
	for retired, want := range map[string]*DDSketch{
		"ddsketch_empty_a001":    NewDDSketch(testAlpha),
		"ddsketch_positive_a001": positiveFixture(),
		"ddsketch_signed_a001":   signedFixture(),
	} {
		t.Run(retired, func(t *testing.T) {
			var got DDSketch
			if err := got.UnmarshalASAPv1(asapv1test.Golden(t, retired)); err != nil {
				t.Fatal(err)
			}
			if !sameState(&got, want) {
				t.Fatalf("decoded state differs\n got: %+v\nwant: %+v", got, *want)
			}
			b, err := got.MarshalASAPv1()
			if err != nil {
				t.Fatal(err)
			}
			asapv1test.Equal(t, b, asapv1test.Golden(t, strings.Replace(retired, "ddsketch_", "ddsketch_buckets_", 1)))
		})
	}
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
	if _, ok := got.Quantile(0); ok || got.Count() != 0 {
		t.Fatalf("decoded empty sketch: count %d, quantile ok %v", got.Count(), ok)
	}
	got.Update(42)
	if got.Count() != 1 {
		t.Fatalf("Update after decode: count %d, want 1", got.Count())
	}
	if q, _ := got.Quantile(0); math.Abs(q-42) > testAlpha*42 {
		t.Fatalf("Quantile(0) after decode and Update: %v, want 42 within alpha", q)
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
		asapv1test.Equal(t, got, asapv1test.Golden(t, "ddsketch_buckets_empty_a001"))
	}
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

	if err := left.Merge(signedFixture()); err != nil {
		t.Fatal(err)
	}
	got := roundTrip(t, left)
	if got.Count() != 400+18 || got.zeroCount != 7 {
		t.Fatalf("merged count %d zero %d, want 418 and 7", got.Count(), got.zeroCount)
	}
	if lo, _ := got.Quantile(0); lo != -got.mapping.Value(-207) {
		t.Fatalf("merged Quantile(0) %v, want -Value(-207) %v", lo, -got.mapping.Value(-207))
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
	if err := d.UnmarshalASAPv1(asapv1test.Golden(t, "ddsketch_buckets_signed_a001")); err != nil {
		t.Fatal(err)
	}
	if d.Count() != 18 {
		t.Fatalf("count %d, want 18", d.Count())
	}
	m := d.mapping
	// Ranks 1, 2-6, 7-13, 14-16, 17-18 fall in buckets -207, -208, zero, 310,
	// 312; every rank reads its bucket's representative.
	want := map[int]float64{
		1:  -m.Value(-207),
		2:  -m.Value(-208),
		6:  -m.Value(-208),
		7:  0,
		13: 0,
		14: m.Value(310),
		16: m.Value(310),
		17: m.Value(312),
		18: m.Value(312),
	}
	for rank, w := range want {
		q := (float64(rank) - 0.5) / 18
		if got, ok := d.Quantile(q); !ok || got != w {
			t.Errorf("rank %d (q=%v): got %v, want %v", rank, q, got, w)
		}
	}
}

func craft(kind asapv1.KindID, version uint8, alpha float64, payload func(p *asapv1.Encoder)) []byte {
	md := asapv1.NewMetadataWriter(version)
	md.Float64("alpha", alpha)
	p := asapv1.NewEncoder()
	payload(p)
	b, err := asapv1.Marshal(kind, md, p)
	if err != nil {
		panic(err)
	}
	return b
}

// bucketsPayload is the 05 01 payload.
func bucketsPayload(counts []uint64, offset int64, neg []uint64, negOffset int64, zero uint64) func(p *asapv1.Encoder) {
	return func(p *asapv1.Encoder) {
		p.Array(5)
		asapv1.EncodeUints(p, counts)
		p.Int(offset)
		asapv1.EncodeUints(p, neg)
		p.Int(negOffset)
		p.Uint(zero)
	}
}

// scalarsPayload is the retired 05 00 payload.
func scalarsPayload(counts []uint64, offset int64, sum, minV, maxV float64, neg []uint64, negOffset int64, zero uint64) func(p *asapv1.Encoder) {
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
	const dd, retired = asapv1.KindDDSketch, asapv1.KindDDSketchWithScalars
	valid := bucketsPayload([]uint64{3}, 7, nil, 0, 0)
	withMetadata := func(write func(md *asapv1.MetadataWriter)) []byte {
		md := asapv1.NewMetadataWriter(1)
		write(md)
		p := asapv1.NewEncoder()
		valid(p)
		b, _ := asapv1.Marshal(dd, md, p)
		return b
	}

	cases := []struct {
		name string
		b    []byte
		want string
	}{
		{"foreign kind_id", craft(asapv1.KindKLL, 1, testAlpha, valid), "kind_id"},
		{"metadata version 2", craft(dd, 2, testAlpha, valid), ""},
		{"unknown metadata key", withMetadata(func(md *asapv1.MetadataWriter) {
			md.Float64("alpha", testAlpha)
			md.Uint("bogus", 7)
		}), ""},
		{"missing alpha", withMetadata(func(md *asapv1.MetadataWriter) {}), ""},
		{"alpha 0", craft(dd, 1, 0, valid), "alpha"},
		{"alpha 1", craft(dd, 1, 1, valid), "alpha"},
		{"alpha negative", craft(dd, 1, -0.5, valid), "alpha"},
		{"alpha 2", craft(dd, 1, 2, valid), "alpha"},
		{"alpha NaN", craft(dd, 1, math.NaN(), valid), "alpha"},
		{"alpha +Inf", craft(dd, 1, inf, valid), "alpha"},
		{"05 01 with eight fields", craft(dd, 1, testAlpha, scalarsPayload([]uint64{3}, 7, 6, 1.5, 2.5, nil, 0, 0)), ""},
		{"05 00 with five fields", craft(retired, 1, testAlpha, valid), ""},
		{"offset past int32", craft(dd, 1, testAlpha, bucketsPayload([]uint64{3}, 1<<40, nil, 0, 0)), ""},
		{"span past int32", craft(dd, 1, testAlpha, bucketsPayload([]uint64{1, 1, 1, 1}, math.MaxInt32-2, nil, 0, 0)), "span past int32"},
		{"negative span past int32", craft(dd, 1, testAlpha,
			bucketsPayload([]uint64{1}, 0, []uint64{1, 1, 1, 1}, math.MaxInt32-2, 0)), "span past int32"},
		{"retired span past int32", craft(retired, 1, testAlpha,
			scalarsPayload([]uint64{1, 1, 1, 1}, math.MaxInt32-2, 4, 1, 2, nil, 0, 0)), "span past int32"},
		{"empty store at nonzero offset", craft(dd, 1, testAlpha, bucketsPayload(nil, 42, nil, 0, 0)), "offset 0"},
		{"empty negative store at nonzero offset", craft(dd, 1, testAlpha,
			bucketsPayload([]uint64{1}, 0, nil, 42, 1)), "offset 0"},
		{"total overflow", craft(dd, 1, testAlpha, bucketsPayload([]uint64{math.MaxUint64, 1}, 0, nil, 0, 0)), "overflow"},
		{"signed total overflow", craft(dd, 1, testAlpha,
			bucketsPayload([]uint64{1}, 0, []uint64{1}, 0, math.MaxUint64-1)), "overflow"},
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

	if err := new(DDSketch).UnmarshalASAPv1(craft(dd, 1, testAlpha, valid)); err != nil {
		t.Fatalf("control payload rejected: %v", err)
	}
	boundary := craft(dd, 1, testAlpha, bucketsPayload([]uint64{1, 1, 1}, math.MaxInt32-2, nil, 0, 0))
	if err := new(DDSketch).UnmarshalASAPv1(boundary); err != nil {
		t.Fatalf("store ending at MaxInt32 rejected: %v", err)
	}
}

// The retired kind's scalars are read and dropped without any check, so
// values the old decoder refused still decode, into the buckets alone.
func TestASAPv1RetiredScalarsAreDropped(t *testing.T) {
	want := literal(testAlpha, []uint64{3}, 7, []uint64{1}, -2, 4)
	for name, scalars := range map[string][3]float64{
		"consistent":  {6, -1.5, 2.5},
		"NaN":         {math.NaN(), math.NaN(), math.NaN()},
		"min > max":   {0, 9, -9},
		"empty-style": {0, math.Inf(1), math.Inf(-1)},
	} {
		t.Run(name, func(t *testing.T) {
			b := craft(asapv1.KindDDSketchWithScalars, 1, testAlpha,
				scalarsPayload([]uint64{3}, 7, scalars[0], scalars[1], scalars[2], []uint64{1}, -2, 4))
			var got DDSketch
			if err := got.UnmarshalASAPv1(b); err != nil {
				t.Fatal(err)
			}
			if !sameState(&got, want) {
				t.Fatalf("decoded state differs\n got: %+v\nwant: %+v", got, *want)
			}
			again, err := got.MarshalASAPv1()
			if err != nil {
				t.Fatal(err)
			}
			asapv1test.Equal(t, again, craft(asapv1.KindDDSketch, 1, testAlpha,
				bucketsPayload([]uint64{3}, 7, []uint64{1}, -2, 4)))
		})
	}
}

func TestASAPv1MarshalRejectsUndecodableState(t *testing.T) {
	cases := map[string]*DDSketch{
		"span past int32": literal(testAlpha, []uint64{1, 1, 1, 1}, math.MaxInt32-2, nil, 0, 0),
		"total overflow":  literal(testAlpha, []uint64{math.MaxUint64, 1}, 0, nil, 0, 0),
		"signed span":     literal(testAlpha, []uint64{1}, 0, []uint64{1, 1, 1, 1}, math.MaxInt32-2, 0),
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			if b, err := d.MarshalASAPv1(); err == nil {
				t.Fatalf("marshal succeeded: %x", b)
			}
		})
	}
}

// q = 0 and q = 1 read the lowest and highest occupied buckets, within alpha
// of the true extremes; there is no exact min / max to clamp to.
func TestQuantileEndpointsAreWithinAlpha(t *testing.T) {
	d := NewDDSketch(testAlpha)
	values := []float64{0.37, 12, 15.5, 230, 1200}
	for _, v := range values {
		d.Update(v)
	}
	lo, _ := d.Quantile(0)
	hi, _ := d.Quantile(1)
	if lo != d.mapping.Value(d.mapping.Index(0.37)) || math.Abs(lo-0.37) > testAlpha*0.37 {
		t.Fatalf("Quantile(0) = %v, want the bucket of 0.37 within alpha", lo)
	}
	if hi != d.mapping.Value(d.mapping.Index(1200)) || math.Abs(hi-1200) > testAlpha*1200 {
		t.Fatalf("Quantile(1) = %v, want the bucket of 1200 within alpha", hi)
	}
	for _, q := range []float64{-0.1, 1.1, math.NaN()} {
		if _, ok := d.Quantile(q); ok {
			t.Fatalf("Quantile(%v) ok, want false", q)
		}
	}
}

// A sketch rebuilt from its buckets alone (the Octo aggregator path) answers
// every quantile exactly as the sketch the values were added to.
func TestBucketFedSketchMatchesTheProducer(t *testing.T) {
	src := NewDDSketch(testAlpha)
	for _, v := range []float64{12, 15.5, 9.8, 230, 14.1, 18.9, 1200} {
		src.Update(v)
	}
	agg := NewDDSketch(testAlpha)
	src.EachBucket(func(k int32, c uint64) { agg.AddToBucket(k, c) })
	for _, q := range []float64{0, 0.25, 0.5, 0.99, 1} {
		a, _ := agg.Quantile(q)
		s, _ := src.Quantile(q)
		if a != s {
			t.Fatalf("Quantile(%v): aggregator %v, producer %v", q, a, s)
		}
	}
}

// If count ever exceeded the buckets, Quantile reads the last occupied
// bucket, a negative one included.
func TestQuantilePastTheBucketsReadsTheLastOccupiedBucket(t *testing.T) {
	d := literal(testAlpha, nil, 0, []uint64{2}, 30, 0)
	d.count += 5
	if got, _ := d.Quantile(1); got != -d.mapping.Value(30) {
		t.Fatalf("Quantile(1) = %v, want %v", got, -d.mapping.Value(30))
	}
}

// TestSignedFixtureQuantilesMatchRust compares against the values Rust's
// get_value_at_quantile returns for the decoded signed fixture; math.Pow and
// Rust's powf can differ in the last bits, so bucket values get a tolerance.
func TestSignedFixtureQuantilesMatchRust(t *testing.T) {
	var d DDSketch
	if err := d.UnmarshalASAPv1(asapv1test.Golden(t, "ddsketch_buckets_signed_a001")); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[float64]float64{
		0: -0.016079860712490172, 0.05: -0.016079860712490172, 0.1: -0.01576144762907453, 0.4: 0,
		0.75: 497.77940145581556, 0.9: 518.0948550403809, 1: 518.0948550403809,
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
