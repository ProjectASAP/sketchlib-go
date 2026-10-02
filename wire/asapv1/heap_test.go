package asapv1_test

import (
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1/asapv1test"
)

type entry[K asapv1.HeapKey] = asapv1.HeapEntry[K]

// heapRoundTrip decodes the keys and heap_counts arrays in raw as K, checks
// they are already in payload order, and re-encodes them to raw.
func heapRoundTrip[K asapv1.HeapKey](t *testing.T, name, keyType string, raw []byte) {
	t.Helper()
	d := asapv1.NewDecoder(raw)
	es := asapv1.DecodeHeapEntries[K](d, keyType)
	if err := d.Finish(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	sorted := slices.Clone(es)
	asapv1.SortHeapEntries(sorted)
	if !reflect.DeepEqual(sorted, es) {
		t.Fatalf("%s: payload order %v, SortHeapEntries gives %v", name, es, sorted)
	}
	if got := asapv1.HeapKeyTypeOf(es); got != keyType {
		t.Fatalf("%s: key_type %q, HeapKeyTypeOf gives %q", name, keyType, got)
	}
	e := asapv1.NewEncoder()
	asapv1.EncodeHeapEntries(e, es)
	if err := e.Err(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	asapv1test.Equal(t, e.Bytes(), raw)
}

func TestHeapEntriesMatchFixtures(t *testing.T) {
	seen := 0
	for _, name := range asapv1test.Names(t) {
		kind, metadata, payload, err := asapv1.Split(asapv1test.Golden(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if kind != asapv1.KindCMSHeap && kind != asapv1.KindCSHeap {
			continue
		}
		md, err := asapv1.ReadMetadata(metadata)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		keyType := md.Str("key_type")
		p := asapv1.NewDecoder(payload)
		p.ExpectArray(3)
		p.Skip()
		raw := append(slices.Clone(p.Raw()), p.Raw()...)
		if err := p.Finish(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		switch keyType {
		case "i64":
			heapRoundTrip[int64](t, name, keyType, raw)
		case "string":
			heapRoundTrip[string](t, name, keyType, raw)
		case asapv1.EmptyHeapKeyType:
			heapRoundTrip[uint64](t, name, keyType, raw)
		default:
			t.Fatalf("%s: no test for key_type %q", name, keyType)
		}
		seen++
	}
	if seen < 6 {
		t.Fatalf("checked %d heap fixtures, want at least 6", seen)
	}
}

func TestCompareHeapKeys(t *testing.T) {
	ints := []int64{-1, math.MinInt64, 0, 2, 1}
	slices.SortFunc(ints, asapv1.CompareHeapKeys[int64])
	if want := []int64{0, 1, 2, math.MinInt64, -1}; !slices.Equal(ints, want) {
		t.Errorf("int64 order %v, want %v", ints, want)
	}
	small := []int8{-1, 3, -128, 0}
	slices.SortFunc(small, asapv1.CompareHeapKeys[int8])
	if want := []int8{0, 3, -128, -1}; !slices.Equal(small, want) {
		t.Errorf("int8 order %v, want %v", small, want)
	}
	strs := []string{"b", "aa", "Z", "a", ""}
	slices.SortFunc(strs, asapv1.CompareHeapKeys[string])
	if want := []string{"", "Z", "a", "aa", "b"}; !slices.Equal(strs, want) {
		t.Errorf("string order %q, want %q", strs, want)
	}
	floats := []float64{1, -1, math.Copysign(0, -1), 0}
	slices.SortFunc(floats, asapv1.CompareHeapKeys[float64])
	want := []uint64{0, math.Float64bits(1), math.Float64bits(math.Copysign(0, -1)), math.Float64bits(-1)}
	for i, f := range floats {
		if math.Float64bits(f) != want[i] {
			t.Errorf("float64 order %v, want bits %x", floats, want)
			break
		}
	}
	bins := [][]byte{{1, 0}, {1}, {0, 255}}
	slices.SortFunc(bins, asapv1.CompareHeapKeys[[]byte])
	if want := [][]byte{{0, 255}, {1}, {1, 0}}; !reflect.DeepEqual(bins, want) {
		t.Errorf("bytes order %v, want %v", bins, want)
	}
}

func TestSortHeapEntries(t *testing.T) {
	es := []entry[int64]{{1, 5}, {math.MinInt64, 5}, {2, 9}, {-1, 5}, {0, 5}, {7, -3}}
	asapv1.SortHeapEntries(es)
	want := []entry[int64]{{2, 9}, {0, 5}, {1, 5}, {math.MinInt64, 5}, {-1, 5}, {7, -3}}
	if !reflect.DeepEqual(es, want) {
		t.Fatalf("got %v, want %v", es, want)
	}
}

func TestHeapKeyTypes(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{asapv1.HeapKeyType[int8](), "i8"}, {asapv1.HeapKeyType[int16](), "i16"},
		{asapv1.HeapKeyType[int32](), "i32"}, {asapv1.HeapKeyType[int64](), "i64"},
		{asapv1.HeapKeyType[uint8](), "u8"}, {asapv1.HeapKeyType[uint16](), "u16"},
		{asapv1.HeapKeyType[uint32](), "u32"}, {asapv1.HeapKeyType[uint64](), "u64"},
		{asapv1.HeapKeyType[float32](), "f32"}, {asapv1.HeapKeyType[float64](), "f64"},
		{asapv1.HeapKeyType[string](), "string"}, {asapv1.HeapKeyType[[]byte](), "bytes"},
	} {
		if c.got != c.want {
			t.Errorf("HeapKeyType %q, want %q", c.got, c.want)
		}
		if !asapv1.IsHeapKeyType(c.want) {
			t.Errorf("IsHeapKeyType(%q) is false", c.want)
		}
	}
	for _, name := range []string{"isize", "usize"} {
		if !asapv1.IsHeapKeyType(name) {
			t.Errorf("IsHeapKeyType(%q) is false", name)
		}
	}
	for _, name := range []string{"", "str", "i128", "u128", "String"} {
		if asapv1.IsHeapKeyType(name) {
			t.Errorf("IsHeapKeyType(%q) is true", name)
		}
	}
	if got := asapv1.HeapKeyTypeOf[string](nil); got != "u64" {
		t.Errorf("empty heap key_type %q, want u64", got)
	}
}

func TestHeapEntriesRoundTripEveryKeyType(t *testing.T) {
	check := func(name string, encode func(*asapv1.Encoder), decode func(*asapv1.Decoder) any, want any) {
		t.Helper()
		e := asapv1.NewEncoder()
		encode(e)
		if err := e.Err(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		d := asapv1.NewDecoder(e.Bytes())
		got := decode(d)
		if err := d.Finish(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
	}
	f32 := []entry[float32]{{float32(math.Inf(1)), 1}, {-2.5, 3}}
	check("f32", func(e *asapv1.Encoder) { asapv1.EncodeHeapEntries(e, f32) },
		func(d *asapv1.Decoder) any { return asapv1.DecodeHeapEntries[float32](d, "f32") },
		[]entry[float32]{{-2.5, 3}, {float32(math.Inf(1)), 1}})
	u16 := []entry[uint16]{{65535, -2}, {7, 0}}
	check("u16", func(e *asapv1.Encoder) { asapv1.EncodeHeapEntries(e, u16) },
		func(d *asapv1.Decoder) any { return asapv1.DecodeHeapEntries[uint16](d, "u16") },
		[]entry[uint16]{{7, 0}, {65535, -2}})
	bins := []entry[[]byte]{{[]byte{0xff, 0xfe}, 1}, {[]byte{}, 1}}
	check("bytes", func(e *asapv1.Encoder) { asapv1.EncodeHeapEntries(e, bins) },
		func(d *asapv1.Decoder) any { return asapv1.DecodeHeapEntries[[]byte](d, "bytes") },
		[]entry[[]byte]{{[]byte{}, 1}, {[]byte{0xff, 0xfe}, 1}})
}

func TestHeapEntriesRejectDuplicateKeys(t *testing.T) {
	nan := math.Float64frombits(0x7ff8000000000001)
	e := asapv1.NewEncoder()
	asapv1.EncodeHeapEntries(e, []entry[float64]{{nan, 1}, {nan, 2}})
	if e.Err() == nil {
		t.Error("two bit-identical NaN keys encoded")
	}
	e = asapv1.NewEncoder()
	asapv1.EncodeHeapEntries(e, []entry[float64]{{0, 1}, {math.Copysign(0, -1), 1}})
	if err := e.Err(); err != nil {
		t.Errorf("+0.0 and -0.0 are distinct keys: %v", err)
	}
	e = asapv1.NewEncoder()
	asapv1.EncodeHeapEntries(e, []entry[string]{{"a", 1}, {"b", 9}, {"a", 3}})
	if e.Err() == nil {
		t.Error("a repeated string key encoded")
	}

	raw := asapv1.NewEncoder()
	asapv1.EncodeStrs(raw, []string{"a", "b", "a"})
	asapv1.EncodeInts(raw, []int64{3, 2, 1})
	d := asapv1.NewDecoder(raw.Bytes())
	if asapv1.DecodeHeapEntries[string](d, "string"); d.Err() == nil {
		t.Error("a repeated string key decoded")
	}
}

func TestDecodeHeapEntriesRejects(t *testing.T) {
	strs := func(keys []string, counts []int64) []byte {
		e := asapv1.NewEncoder()
		asapv1.EncodeStrs(e, keys)
		asapv1.EncodeInts(e, counts)
		return e.Bytes()
	}
	ints := func(keys []int64, counts []int64) []byte {
		e := asapv1.NewEncoder()
		asapv1.EncodeInts(e, keys)
		asapv1.EncodeInts(e, counts)
		return e.Bytes()
	}
	for _, c := range []struct {
		name    string
		raw     []byte
		keyType string
	}{
		{"unknown key_type", strs(nil, nil), "str"},
		{"i128 key_type", strs(nil, nil), "i128"},
		{"keys of another type", ints([]int64{1}, []int64{1}), "i64"},
		{"relabelled keys", strs([]string{"a"}, []int64{1}), "bytes"},
		{"more counts than keys", strs([]string{"a"}, []int64{1, 2}), "string"},
		{"fewer counts than keys", strs([]string{"a", "b"}, []int64{1}), "string"},
	} {
		d := asapv1.NewDecoder(c.raw)
		if asapv1.DecodeHeapEntries[string](d, c.keyType); d.Err() == nil {
			t.Errorf("%s: decoded", c.name)
		}
	}
	for _, keyType := range []string{"u64", "i64", "bytes", "isize"} {
		d := asapv1.NewDecoder(strs(nil, nil))
		es := asapv1.DecodeHeapEntries[string](d, keyType)
		if err := d.Finish(); err != nil || len(es) != 0 {
			t.Errorf("empty heap labelled %q: %v, %v", keyType, es, err)
		}
	}
}
