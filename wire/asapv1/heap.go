package asapv1

import (
	"bytes"
	"cmp"
	"errors"
	"math"
	"reflect"
	"slices"
)

// HeapKey is the set of Go types a top-k heap key takes on the wire, one per
// key_type name. "isize" and "usize" have no Go type.
type HeapKey interface {
	~int8 | ~int16 | ~int32 | ~int64 | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64 | ~string | ~[]byte
}

// HeapEntry is one top-k heap entry: a key and its signed count.
type HeapEntry[K HeapKey] struct {
	Key   K
	Count int64
}

// EmptyHeapKeyType is the key_type of a heap with no entries.
const EmptyHeapKeyType = "u64"

var heapKeyTypes = []string{
	"i8", "i16", "i32", "i64", "isize",
	"u8", "u16", "u32", "u64", "usize",
	"f32", "f64", "string", "bytes",
}

// IsHeapKeyType reports whether name is one of the fourteen key_type names.
func IsHeapKeyType(name string) bool { return slices.Contains(heapKeyTypes, name) }

var heapKeyTypeOfKind = map[reflect.Kind]string{
	reflect.Int8: "i8", reflect.Int16: "i16", reflect.Int32: "i32", reflect.Int64: "i64",
	reflect.Uint8: "u8", reflect.Uint16: "u16", reflect.Uint32: "u32", reflect.Uint64: "u64",
	reflect.Float32: "f32", reflect.Float64: "f64", reflect.String: "string", reflect.Slice: "bytes",
}

// HeapKeyType returns the key_type name of K.
func HeapKeyType[K HeapKey]() string {
	return heapKeyTypeOfKind[reflect.TypeFor[K]().Kind()]
}

// HeapKeyTypeOf returns the metadata key_type of es: K's name, or
// EmptyHeapKeyType when es is empty.
func HeapKeyTypeOf[K HeapKey](es []HeapEntry[K]) string {
	if len(es) == 0 {
		return EmptyHeapKeyType
	}
	return HeapKeyType[K]()
}

// CompareHeapKeys orders two keys by their wire bits: integers by their 64-bit
// two's-complement pattern as unsigned, floats by their IEEE 754 bits, and
// strings and bytes bytewise.
func CompareHeapKeys[K HeapKey](a, b K) int {
	x, y := reflect.ValueOf(a), reflect.ValueOf(b)
	switch x.Kind() {
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmp.Compare(uint64(x.Int()), uint64(y.Int()))
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return cmp.Compare(x.Uint(), y.Uint())
	case reflect.Float32:
		return cmp.Compare(math.Float32bits(float32(x.Float())), math.Float32bits(float32(y.Float())))
	case reflect.Float64:
		return cmp.Compare(math.Float64bits(x.Float()), math.Float64bits(y.Float()))
	case reflect.String:
		return cmp.Compare(x.String(), y.String())
	default:
		return bytes.Compare(x.Bytes(), y.Bytes())
	}
}

// SortHeapEntries sorts es into payload order: descending count, then
// ascending CompareHeapKeys.
func SortHeapEntries[K HeapKey](es []HeapEntry[K]) {
	slices.SortFunc(es, func(a, b HeapEntry[K]) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return CompareHeapKeys(a.Key, b.Key)
	})
}

// ErrDuplicateHeapKey reports a key set holding one key twice.
var ErrDuplicateHeapKey = errors.New("asapv1: the same heap key appears twice")

// CheckDistinctHeapKeys returns ErrDuplicateHeapKey when two keys are equal
// by CompareHeapKeys: two bit-identical NaNs are one key, +0.0 and -0.0 two.
func CheckDistinctHeapKeys[K HeapKey](keys []K) error {
	sorted := slices.Clone(keys)
	slices.SortFunc(sorted, CompareHeapKeys[K])
	for i := 1; i < len(sorted); i++ {
		if CompareHeapKeys(sorted[i-1], sorted[i]) == 0 {
			return ErrDuplicateHeapKey
		}
	}
	return nil
}

// EncodeHeapKeys writes keys, in the given order, as an array of K's wire type.
func EncodeHeapKeys[K HeapKey](e *Encoder, keys []K) {
	e.Array(len(keys))
	for _, k := range keys {
		v := reflect.ValueOf(k)
		switch v.Kind() {
		case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			e.Int(v.Int())
		case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			e.Uint(v.Uint())
		case reflect.Float32:
			e.Float32(float32(v.Float()))
		case reflect.Float64:
			e.Float64(v.Float())
		case reflect.String:
			e.Str(v.String())
		default:
			e.Bin(v.Bytes())
		}
	}
}

// DecodeHeapKeys reads an array of keys typed by keyType. It fails unless
// keyType is a key_type name and, when the array is non-empty, K's name.
func DecodeHeapKeys[K HeapKey](d *Decoder, keyType string) []K {
	if d.err != nil {
		return nil
	}
	if !IsHeapKeyType(keyType) {
		d.failf("key_type %q is not a heap key type", keyType)
		return nil
	}
	n := d.Array()
	if d.err != nil {
		return nil
	}
	if want := HeapKeyType[K](); n > 0 && keyType != want {
		d.failf("key_type %q: heap keys are held as %q", keyType, want)
		return nil
	}
	keys := make([]K, n)
	for i := range keys {
		v := reflect.ValueOf(&keys[i]).Elem()
		switch v.Kind() {
		case reflect.Int8:
			v.SetInt(int64(DecodeInt[int8](d)))
		case reflect.Int16:
			v.SetInt(int64(DecodeInt[int16](d)))
		case reflect.Int32:
			v.SetInt(int64(DecodeInt[int32](d)))
		case reflect.Int64:
			v.SetInt(d.Int())
		case reflect.Uint8:
			v.SetUint(uint64(DecodeUint[uint8](d)))
		case reflect.Uint16:
			v.SetUint(uint64(DecodeUint[uint16](d)))
		case reflect.Uint32:
			v.SetUint(uint64(DecodeUint[uint32](d)))
		case reflect.Uint64:
			v.SetUint(d.Uint())
		case reflect.Float32:
			v.SetFloat(float64(d.Float32()))
		case reflect.Float64:
			v.SetFloat(d.Float64())
		case reflect.String:
			v.SetString(d.Str())
		default:
			v.SetBytes(d.Bin())
		}
	}
	if d.err != nil {
		return nil
	}
	return keys
}

// EncodeHeapEntries writes es in payload order as the parallel keys and
// heap_counts arrays. It records ErrDuplicateHeapKey when two entries hold one key.
func EncodeHeapEntries[K HeapKey](e *Encoder, es []HeapEntry[K]) {
	sorted := slices.Clone(es)
	SortHeapEntries(sorted)
	keys := make([]K, len(sorted))
	counts := make([]int64, len(sorted))
	for i, entry := range sorted {
		keys[i], counts[i] = entry.Key, entry.Count
	}
	if err := CheckDistinctHeapKeys(keys); err != nil {
		e.Fail(err)
		return
	}
	EncodeHeapKeys(e, keys)
	EncodeInts(e, counts)
}

// DecodeHeapEntries reads the parallel keys and heap_counts arrays, with keys
// typed by keyType as DecodeHeapKeys reads them, and returns the entries in
// payload order. It fails unless the arrays have equal length and no key repeats.
func DecodeHeapEntries[K HeapKey](d *Decoder, keyType string) []HeapEntry[K] {
	keys := DecodeHeapKeys[K](d, keyType)
	if m := d.Array(); d.err == nil && m != len(keys) {
		d.failf("%d heap keys but %d heap counts", len(keys), m)
	}
	es := make([]HeapEntry[K], len(keys))
	for i := range es {
		es[i] = HeapEntry[K]{Key: keys[i], Count: d.Int()}
	}
	if d.err != nil {
		return nil
	}
	if err := CheckDistinctHeapKeys(keys); err != nil {
		d.Fail(err)
		return nil
	}
	return es
}
