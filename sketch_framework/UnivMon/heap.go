package univmon

import (
	"encoding/binary"
	"math"
	"reflect"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// keyID returns k's input bytes as a string: an integer as its 64-bit
// two's-complement pattern, a float as its IEEE 754 bits, both in native byte
// order, and a string or byte slice as is. Equal IDs are the same key.
func keyID[K asapv1.HeapKey](k K) string {
	v := reflect.ValueOf(k)
	var b [8]byte
	switch v.Kind() {
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		binary.NativeEndian.PutUint64(b[:], uint64(v.Int()))
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		binary.NativeEndian.PutUint64(b[:], v.Uint())
	case reflect.Float32:
		binary.NativeEndian.PutUint32(b[:4], math.Float32bits(float32(v.Float())))
		return string(b[:4])
	case reflect.Float64:
		binary.NativeEndian.PutUint64(b[:], math.Float64bits(v.Float()))
	case reflect.String:
		return v.String()
	default:
		return string(v.Bytes())
	}
	return string(b[:])
}

// cloneKey returns k, with a byte-slice key copied.
func cloneKey[K asapv1.HeapKey](k K) K {
	v := reflect.ValueOf(k)
	if v.Kind() != reflect.Slice {
		return k
	}
	c := reflect.New(v.Type()).Elem()
	c.SetBytes(append([]byte{}, v.Bytes()...))
	return c.Interface().(K)
}

// hhHeap is a min-heap by count of at most k entries with distinct keys,
// beside an index from key ID to heap position.
type hhHeap[K asapv1.HeapKey] struct {
	k       int
	entries []asapv1.HeapEntry[K]
	ids     []string
	pos     map[string]int
}

func newHHHeap[K asapv1.HeapKey](k, expected int) *hhHeap[K] {
	n := min(expected, k, 1024)
	return &hhHeap[K]{
		k:       k,
		entries: make([]asapv1.HeapEntry[K], 0, n),
		ids:     make([]string, 0, n),
		pos:     make(map[string]int, n),
	}
}

// update sets a resident key's count, or seats a new key when the heap has
// room or count exceeds the minimum, evicting the minimum. It reports whether
// the key was resident or the heap had room for it.
func (h *hhHeap[K]) update(key K, id string, count int64) bool {
	if i, ok := h.pos[id]; ok {
		h.entries[i].Count = count
		if !h.down(i) {
			h.up(i)
		}
		return true
	}
	room := len(h.entries) < h.k
	switch {
	case room:
		h.entries = append(h.entries, asapv1.HeapEntry[K]{Key: cloneKey(key), Count: count})
		h.ids = append(h.ids, id)
		h.pos[id] = len(h.entries) - 1
		h.up(len(h.entries) - 1)
	case h.k > 0 && count > h.entries[0].Count:
		delete(h.pos, h.ids[0])
		h.entries[0] = asapv1.HeapEntry[K]{Key: cloneKey(key), Count: count}
		h.ids[0] = id
		h.pos[id] = 0
		h.down(0)
	}
	return room
}

func (h *hhHeap[K]) swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.ids[i], h.ids[j] = h.ids[j], h.ids[i]
	h.pos[h.ids[i]] = i
	h.pos[h.ids[j]] = j
}

// down sifts entry i toward the leaves and reports whether it moved.
func (h *hhHeap[K]) down(i int) bool {
	start, n := i, len(h.entries)
	for {
		t := i
		if l := 2*i + 1; l < n && h.entries[l].Count < h.entries[t].Count {
			t = l
		}
		if r := 2*i + 2; r < n && h.entries[r].Count < h.entries[t].Count {
			t = r
		}
		if t == i {
			return i != start
		}
		h.swap(i, t)
		i = t
	}
}

func (h *hhHeap[K]) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if h.entries[i].Count >= h.entries[p].Count {
			return
		}
		h.swap(i, p)
		i = p
	}
}

func (h *hhHeap[K]) clear() {
	h.entries = h.entries[:0]
	h.ids = h.ids[:0]
	clear(h.pos)
}
