package univmon

import (
	"fmt"
	"slices"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// KeyType returns the metadata key_type: K's name, or "u64" when every heap
// is empty.
func (u *UnivMon[K]) KeyType() string {
	for _, h := range u.heaps {
		if len(h.entries) > 0 {
			return asapv1.HeapKeyType[K]()
		}
	}
	return asapv1.EmptyHeapKeyType
}

// check rejects a state UnmarshalASAPv1 would not rebuild.
func (u *UnivMon[K]) check() error {
	if err := checkShape(u.heapSize, u.sketchRow, u.sketchCol, u.layerSize); err != nil {
		return err
	}
	if len(u.layers) != u.layerSize || len(u.heaps) != u.layerSize || len(u.candidateComplete) != u.layerSize {
		return fmt.Errorf("univmon: %d counters, %d heaps and %d candidate flags over %d layers",
			len(u.layers), len(u.heaps), len(u.candidateComplete), u.layerSize)
	}
	for i, l := range u.layers {
		if l.rows != u.sketchRow || l.cols != u.sketchCol || len(l.counts) != l.rows*l.cols || len(l.l2) != l.rows {
			return fmt.Errorf("univmon: layer %d is not a %dx%d CountL2HH", i, u.sketchRow, u.sketchCol)
		}
		if l.seedIdx != i {
			return fmt.Errorf("univmon: layer %d hashes at seed index %d", i, l.seedIdx)
		}
		if err := checkL2(i, l.l2); err != nil {
			return err
		}
		if h := u.heaps[i]; h.k != u.heapSize || len(h.entries) > u.heapSize {
			return fmt.Errorf("univmon: layer %d heap holds %d of capacity %d, want capacity %d",
				i, len(h.entries), h.k, u.heapSize)
		}
	}
	if u.updateMode > UpdateModeTerminal {
		return fmt.Errorf("univmon: update mode %d is not a wire mode", u.updateMode)
	}
	return nil
}

func checkL2(layer int, l2 []int64) error {
	for r, v := range l2 {
		if v < 0 {
			return fmt.Errorf("univmon: layer %d row %d l2 accumulator %d is negative", layer, r, v)
		}
	}
	return nil
}

// MarshalASAPv1 encodes u as ASAPv1 kind UnivMon.
func (u *UnivMon[K]) MarshalASAPv1() ([]byte, error) {
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	md.Uint("layer_size", uint64(u.layerSize))
	md.Uint("sketch_row", uint64(u.sketchRow))
	md.Uint("sketch_col", uint64(u.sketchCol))
	md.Uint("heap_size", uint64(u.heapSize))
	md.Str("key_type", u.KeyType())
	p := asapv1.NewEncoder()
	if err := u.EncodeASAPv1Payload(p); err != nil {
		return nil, err
	}
	return asapv1.Marshal(asapv1.KindUnivMon, md, p)
}

// UnmarshalASAPv1 replaces u with the UnivMon in b. A pyramid with heap
// entries must have K's key_type.
func (u *UnivMon[K]) UnmarshalASAPv1(b []byte) error {
	md, p, err := asapv1.Open(b, asapv1.KindUnivMon)
	if err != nil {
		return err
	}
	var out UnivMon[K]
	if err := out.DecodeASAPv1Payload(md, p); err != nil {
		return err
	}
	if err := p.Finish(); err != nil {
		return err
	}
	*u = out
	return nil
}

// EncodeASAPv1Payload writes the payload [counts, l2, heap_lens, keys,
// heap_counts, candidate_complete, bucket_size, update_mode], layers
// ascending and each heap in descending count, then key order.
func (u *UnivMon[K]) EncodeASAPv1Payload(e *asapv1.Encoder) error {
	if err := u.check(); err != nil {
		return err
	}
	var counts, l2, heapCounts []int64
	var keys []K
	heapLens := make([]uint32, u.layerSize)
	for i, l := range u.layers {
		counts = append(counts, l.counts...)
		l2 = append(l2, l.l2...)
		es := u.HeapEntries(i)
		for _, entry := range es {
			keys = append(keys, entry.Key)
			heapCounts = append(heapCounts, entry.Count)
		}
		heapLens[i] = uint32(len(es))
	}
	e.Array(8)
	asapv1.EncodeInts(e, counts)
	asapv1.EncodeInts(e, l2)
	asapv1.EncodeUints(e, heapLens)
	asapv1.EncodeHeapKeys(e, keys)
	asapv1.EncodeInts(e, heapCounts)
	e.Array(u.layerSize)
	for _, c := range u.candidateComplete {
		e.Bool(c)
	}
	e.Uint(u.bucketSize)
	e.Uint(uint64(u.updateMode))
	return e.Err()
}

// DecodeASAPv1Payload reads metadata {metadata_version 1, the standard hash
// spec with no seed index, layer_size, sketch_row, sketch_col, heap_size,
// key_type} from md and one payload array from d.
func (u *UnivMon[K]) DecodeASAPv1Payload(md *asapv1.MetadataReader, d *asapv1.Decoder) error {
	md.ExpectVersion(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
	layerSize := int(md.Uint32("layer_size"))
	sketchRow := int(md.Uint32("sketch_row"))
	sketchCol := int(md.Uint32("sketch_col"))
	heapSize := int(md.Uint32("heap_size"))
	keyType := md.Str("key_type")
	if err := md.Finish(); err != nil {
		return err
	}
	if layerSize == 0 || sketchRow == 0 || sketchCol == 0 || heapSize == 0 {
		return fmt.Errorf("univmon: layer_size %d, sketch_row %d, sketch_col %d and heap_size %d must be non-zero",
			layerSize, sketchRow, sketchCol, heapSize)
	}
	if layerSize > MaxLayerSize {
		return fmt.Errorf("univmon: layer_size %d exceeds %d", layerSize, MaxLayerSize)
	}
	if !asapv1.IsHeapKeyType(keyType) {
		return fmt.Errorf("univmon: key_type %q is not a heap key type", keyType)
	}

	d.ExpectArray(8)
	counts := asapv1.DecodeInts[int64](d)
	l2 := asapv1.DecodeInts[int64](d)
	heapLens := asapv1.DecodeUints[uint32](d)
	keys := asapv1.DecodeHeapKeys[K](d, keyType)
	heapCounts := asapv1.DecodeInts[int64](d)
	complete := make([]bool, d.Array())
	for i := range complete {
		complete[i] = d.Bool()
	}
	bucketSize := d.Uint()
	mode := UpdateMode(d.Uint8())
	if err := d.Err(); err != nil {
		return err
	}

	if len(keys) != len(heapCounts) {
		return fmt.Errorf("univmon: %d keys but %d heap counts", len(keys), len(heapCounts))
	}
	if sketchRow*layerSize != len(l2) {
		return fmt.Errorf("univmon: %d layers of %d rows against %d l2 accumulators", layerSize, sketchRow, len(l2))
	}
	if len(heapLens) != layerSize || len(complete) != layerSize {
		return fmt.Errorf("univmon: %d heap_lens and %d candidate_complete over %d layers",
			len(heapLens), len(complete), layerSize)
	}
	if err := checkDimensions(sketchRow, sketchCol); err != nil {
		return err
	}
	if cells := uint64(layerSize*sketchRow) * uint64(sketchCol); uint64(len(counts)) != cells {
		return fmt.Errorf("univmon: counts length %d != the declared layers' %d cells", len(counts), cells)
	}
	var seated uint64
	for _, n := range heapLens {
		seated += uint64(n)
	}
	if seated != uint64(len(keys)) {
		return fmt.Errorf("univmon: %d heap entries against the declared %d", len(keys), seated)
	}
	if mode > UpdateModeTerminal {
		return fmt.Errorf("univmon: update_mode %d is not a wire mode", mode)
	}

	out := UnivMon[K]{
		heapSize: heapSize, sketchRow: sketchRow, sketchCol: sketchCol, layerSize: layerSize,
		layers:            make([]*CountL2HH, layerSize),
		heaps:             make([]*hhHeap[K], layerSize),
		bucketSize:        bucketSize,
		updateMode:        mode,
		candidateComplete: complete,
	}
	cells := sketchRow * sketchCol
	next := 0
	for i := range layerSize {
		layerL2 := slices.Clone(l2[i*sketchRow : (i+1)*sketchRow])
		if err := checkL2(i, layerL2); err != nil {
			return err
		}
		out.layers[i] = &CountL2HH{
			rows: sketchRow, cols: sketchCol, seedIdx: i,
			maskBits: colsMaskBits(sketchCol),
			counts:   slices.Clone(counts[i*cells : (i+1)*cells]),
			l2:       layerL2,
		}
		n := int(heapLens[i])
		if n > heapSize {
			return fmt.Errorf("univmon: layer %d carries %d heap entries over a heap_size of %d", i, n, heapSize)
		}
		run := keys[next : next+n]
		if err := asapv1.CheckDistinctHeapKeys(run); err != nil {
			return fmt.Errorf("univmon: layer %d: %w", i, err)
		}
		h := newHHHeap[K](heapSize, n)
		for j, key := range run {
			h.update(key, keyID(key), heapCounts[next+j])
		}
		out.heaps[i] = h
		next += n
	}
	*u = out
	return nil
}
