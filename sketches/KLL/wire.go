package kll

import (
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

const (
	maxLevels   = 61
	itemTypeF64 = "f64"
)

// MarshalASAPv1 encodes the sketch as ASAPv1 kind KindKLLDynamic, with
// item_type "f64" and the seed key present when the sketch carries a seed.
func (s *KLLSketch) MarshalASAPv1() ([]byte, error) {
	md := asapv1.NewMetadataWriter(1)
	md.Uint("k", uint64(s.k))
	md.Uint("m", uint64(s.m))
	md.Str("item_type", itemTypeF64)
	if s.seedSet {
		md.Uint("seed", uint64(s.seed))
	}
	p := asapv1.NewEncoder()
	if err := s.EncodeASAPv1Payload(p); err != nil {
		return nil, err
	}
	return asapv1.Marshal(asapv1.KindKLLDynamic, md, p)
}

// UnmarshalASAPv1 decodes ASAPv1 kind KindKLLDynamic with item_type "f64".
func (s *KLLSketch) UnmarshalASAPv1(b []byte) error {
	md, p, err := asapv1.Open(b, asapv1.KindKLLDynamic)
	if err != nil {
		return err
	}
	var out KLLSketch
	if err := out.DecodeASAPv1Payload(md, p); err != nil {
		return err
	}
	if err := p.Finish(); err != nil {
		return err
	}
	*s = out
	return nil
}

// EncodeASAPv1Payload writes the payload [levels, items, coin], levels
// top-most first and level 0 in input order.
func (s *KLLSketch) EncodeASAPv1Payload(e *asapv1.Encoder) error {
	if err := checkState(s.k, s.m, s.levels, len(s.items), uint64(s.co.remainingBits)); err != nil {
		return err
	}
	e.Array(3)
	e.Array(len(s.levels))
	for _, l := range s.levels {
		e.Uint(uint64(l))
	}
	asapv1.EncodeFloat64s(e, s.items)
	e.Array(3)
	e.Uint(s.co.state)
	e.Uint(s.co.bitCache)
	e.Uint(uint64(s.co.remainingBits))
	return e.Err()
}

// DecodeASAPv1Payload reads metadata {metadata_version 1, k, m, item_type
// "f64", optional seed} from md and one payload array from d.
func (s *KLLSketch) DecodeASAPv1Payload(md *asapv1.MetadataReader, d *asapv1.Decoder) error {
	md.ExpectVersion(1)
	k := int(md.Uint32("k"))
	m := int(md.Uint32("m"))
	md.ExpectStr("item_type", itemTypeF64)
	seedSet := md.Has("seed")
	var seed uint64
	if seedSet {
		seed = md.Uint64("seed")
	}
	if err := md.Finish(); err != nil {
		return err
	}
	if err := checkParams(k, m); err != nil {
		return err
	}
	d.ExpectArray(3)
	wireLevels := asapv1.DecodeUints[uint32](d)
	items := asapv1.DecodeFloat64s(d)
	d.ExpectArray(3)
	state, bitCache, remaining := d.Uint(), d.Uint(), d.Uint32()
	if err := d.Err(); err != nil {
		return err
	}
	levels := make([]int, len(wireLevels))
	for i, l := range wireLevels {
		levels[i] = int(l)
	}
	if err := checkState(k, m, levels, len(items), uint64(remaining)); err != nil {
		return err
	}
	*s = KLLSketch{
		items:     items,
		levels:    levels,
		k:         k,
		m:         m,
		numLevels: len(levels) - 1,
		co:        coin{state: state, bitCache: bitCache, remainingBits: uint8(remaining)},
		seed:      int64(seed),
		seedSet:   seedSet,
	}
	s.bindStoresFromSlices()
	s.rebuildCapacityCache()
	return nil
}

func checkParams(k, m int) error {
	if m < 2 || m > k || k > maxCacheableK {
		return fmt.Errorf("kll: k=%d, m=%d outside 2 <= m <= k <= %d", k, m, maxCacheableK)
	}
	return nil
}

// checkState enforces the ASAPv1 KLL layout rules on top-most-first levels.
func checkState(k, m int, levels []int, items int, remainingBits uint64) error {
	if err := checkParams(k, m); err != nil {
		return err
	}
	if len(levels) < 2 {
		return fmt.Errorf("kll: %d level boundaries, need at least 2", len(levels))
	}
	numLevels := len(levels) - 1
	if numLevels > maxLevels {
		return fmt.Errorf("kll: %d levels exceed %d", numLevels, maxLevels)
	}
	if levels[0] != 0 {
		return fmt.Errorf("kll: levels[0] = %d, want 0", levels[0])
	}
	for i := 1; i < len(levels); i++ {
		if levels[i] < levels[i-1] {
			return errors.New("kll: levels must be non-decreasing")
		}
	}
	if levels[numLevels] != items {
		return fmt.Errorf("kll: levels[last] = %d, but %d items", levels[numLevels], items)
	}
	if uint64(items) > math.MaxUint32 {
		return fmt.Errorf("kll: %d items exceed the u32 level range", items)
	}
	if remainingBits > 64 {
		return fmt.Errorf("kll: coin remaining_bits %d exceeds 64", remainingBits)
	}
	total := uint64(0)
	for h := range numLevels {
		idx := numLevels - 1 - h
		hi, lo := bits.Mul64(uint64(levels[idx+1]-levels[idx]), 1<<h)
		if hi != 0 || lo > math.MaxInt-total {
			return errors.New("kll: level layout overflows the weighted count")
		}
		total += lo
	}
	return nil
}
