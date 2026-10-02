package asapv1

import (
	"bytes"
	"cmp"
	"errors"
	"math"
	"slices"
)

// HeapKey is the set of Go types a top-k heap key takes on the wire, one per
// key_type name. "isize" and "usize" have no Go type.
type HeapKey interface {
	int8 | int16 | int32 | int64 | uint8 | uint16 | uint32 | uint64 | float32 | float64 | string | []byte
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

// HeapKeyType returns the key_type name of K.
func HeapKeyType[K HeapKey]() string {
	var k K
	switch any(k).(type) {
	case int8:
		return "i8"
	case int16:
		return "i16"
	case int32:
		return "i32"
	case int64:
		return "i64"
	case uint8:
		return "u8"
	case uint16:
		return "u16"
	case uint32:
		return "u32"
	case uint64:
		return "u64"
	case float32:
		return "f32"
	case float64:
		return "f64"
	case string:
		return "string"
	default:
		return "bytes"
	}
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
	switch x := any(a).(type) {
	case int8:
		return cmp.Compare(uint64(int64(x)), uint64(int64(any(b).(int8))))
	case int16:
		return cmp.Compare(uint64(int64(x)), uint64(int64(any(b).(int16))))
	case int32:
		return cmp.Compare(uint64(int64(x)), uint64(int64(any(b).(int32))))
	case int64:
		return cmp.Compare(uint64(x), uint64(any(b).(int64)))
	case uint8:
		return cmp.Compare(x, any(b).(uint8))
	case uint16:
		return cmp.Compare(x, any(b).(uint16))
	case uint32:
		return cmp.Compare(x, any(b).(uint32))
	case uint64:
		return cmp.Compare(x, any(b).(uint64))
	case float32:
		return cmp.Compare(math.Float32bits(x), math.Float32bits(any(b).(float32)))
	case float64:
		return cmp.Compare(math.Float64bits(x), math.Float64bits(any(b).(float64)))
	case string:
		return cmp.Compare(x, any(b).(string))
	default:
		return bytes.Compare(any(a).([]byte), any(b).([]byte))
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

var errDuplicateHeapKey = errors.New("asapv1: the same heap key appears twice")

// checkDistinctHeapKeys fails when two entries hold keys CompareHeapKeys
// finds equal.
func checkDistinctHeapKeys[K HeapKey](es []HeapEntry[K]) error {
	keys := make([]K, len(es))
	for i := range es {
		keys[i] = es[i].Key
	}
	slices.SortFunc(keys, CompareHeapKeys[K])
	for i := 1; i < len(keys); i++ {
		if CompareHeapKeys(keys[i-1], keys[i]) == 0 {
			return errDuplicateHeapKey
		}
	}
	return nil
}

// EncodeHeapEntries writes es in payload order as the parallel keys and
// heap_counts arrays. It records an error when two entries hold one key.
func EncodeHeapEntries[K HeapKey](e *Encoder, es []HeapEntry[K]) {
	if err := checkDistinctHeapKeys(es); err != nil {
		e.Fail(err)
		return
	}
	sorted := slices.Clone(es)
	SortHeapEntries(sorted)
	e.Array(len(sorted))
	for _, entry := range sorted {
		encodeHeapKey(e, entry.Key)
	}
	e.Array(len(sorted))
	for _, entry := range sorted {
		e.Int(entry.Count)
	}
}

func encodeHeapKey[K HeapKey](e *Encoder, k K) {
	switch x := any(k).(type) {
	case int8:
		e.Int(int64(x))
	case int16:
		e.Int(int64(x))
	case int32:
		e.Int(int64(x))
	case int64:
		e.Int(x)
	case uint8:
		e.Uint(uint64(x))
	case uint16:
		e.Uint(uint64(x))
	case uint32:
		e.Uint(uint64(x))
	case uint64:
		e.Uint(x)
	case float32:
		e.Float32(x)
	case float64:
		e.Float64(x)
	case string:
		e.Str(x)
	case []byte:
		e.Bin(x)
	}
}

// DecodeHeapEntries reads the parallel keys and heap_counts arrays, with keys
// typed by keyType, and returns the entries in payload order. It fails unless
// keyType is a key_type name, keys of another type than K are absent, the
// arrays have equal length, and no key appears twice.
func DecodeHeapEntries[K HeapKey](d *Decoder, keyType string) []HeapEntry[K] {
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
	es := make([]HeapEntry[K], n)
	for i := range es {
		es[i].Key = decodeHeapKey[K](d)
	}
	if m := d.Array(); d.err == nil && m != n {
		d.failf("%d heap keys but %d heap counts", n, m)
	}
	for i := range es {
		es[i].Count = d.Int()
	}
	if d.err != nil {
		return nil
	}
	if err := checkDistinctHeapKeys(es); err != nil {
		d.Fail(err)
		return nil
	}
	return es
}

func decodeHeapKey[K HeapKey](d *Decoder) K {
	var k K
	var v any
	switch any(k).(type) {
	case int8:
		v = DecodeInt[int8](d)
	case int16:
		v = DecodeInt[int16](d)
	case int32:
		v = DecodeInt[int32](d)
	case int64:
		v = d.Int()
	case uint8:
		v = DecodeUint[uint8](d)
	case uint16:
		v = DecodeUint[uint16](d)
	case uint32:
		v = DecodeUint[uint32](d)
	case uint64:
		v = d.Uint()
	case float32:
		v = d.Float32()
	case float64:
		v = d.Float64()
	case string:
		v = d.Str()
	case []byte:
		v = d.Bin()
	}
	return v.(K)
}
