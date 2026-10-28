package ddsketch

import (
	"errors"
	"fmt"
	"math"

	"github.com/ProjectASAP/sketchlib-go/common/storage"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// MarshalASAPv1 encodes the sketch as an ASAPv1 DDSketch envelope, kind
// 05 01: metadata_version 1 and alpha, then the payload
// [counts, offset, negative_counts, negative_offset, zero_count].
func (d *DDSketch) MarshalASAPv1() ([]byte, error) {
	alpha := d.mapping.alpha
	if err := checkAlpha(alpha); err != nil {
		return nil, err
	}
	counts, offset := wireStore(&d.store)
	negCounts, negOffset := wireStore(&d.negative)
	if err := checkState(counts, offset, negCounts, negOffset, d.zeroCount); err != nil {
		return nil, err
	}

	md := asapv1.NewMetadataWriter(1)
	md.Float64("alpha", alpha)
	p := asapv1.NewEncoder()
	p.Array(5)
	asapv1.EncodeUints(p, counts)
	p.Int(int64(offset))
	asapv1.EncodeUints(p, negCounts)
	p.Int(int64(negOffset))
	p.Uint(d.zeroCount)
	return asapv1.Marshal(asapv1.KindDDSketch, md, p)
}

// UnmarshalASAPv1 replaces the sketch with the one encoded in b: kind 05 01,
// or the retired 05 00, whose eight-field payload also carries sum / min /
// max between offset and negative_counts; those are read as float64 and
// dropped. The result has no bin cap and no sampler.
func (d *DDSketch) UnmarshalASAPv1(b []byte) error {
	kind, metadata, payload, err := asapv1.Split(b)
	if err != nil {
		return err
	}
	if kind != asapv1.KindDDSketch && kind != asapv1.KindDDSketchWithScalars {
		return fmt.Errorf("asapv1: kind_id %v, want %v (or the retired %v)",
			kind, asapv1.KindDDSketch, asapv1.KindDDSketchWithScalars)
	}
	md, err := asapv1.ReadMetadata(metadata)
	if err != nil {
		return err
	}
	md.ExpectVersion(1)
	alpha := md.Float64("alpha")
	if err := md.Finish(); err != nil {
		return err
	}
	if err := checkAlpha(alpha); err != nil {
		return err
	}

	withScalars := kind == asapv1.KindDDSketchWithScalars
	p := asapv1.NewDecoder(payload)
	if withScalars {
		p.ExpectArray(8)
	} else {
		p.ExpectArray(5)
	}
	counts := asapv1.DecodeUints[uint64](p)
	offset := p.Int32()
	if withScalars {
		_, _, _ = p.Float64(), p.Float64(), p.Float64() // sum, min, max: dropped
	}
	negCounts := asapv1.DecodeUints[uint64](p)
	negOffset := p.Int32()
	zeroCount := p.Uint()
	if err := p.Finish(); err != nil {
		return err
	}
	if err := checkState(counts, offset, negCounts, negOffset, zeroCount); err != nil {
		return err
	}
	total, _ := totalCount(counts, negCounts, zeroCount)

	store := storeFromWire(counts, offset)
	*d = DDSketch{
		mapping:      NewIndexMapping(alpha),
		store:        store,
		negative:     storeFromWire(negCounts, negOffset),
		zeroCount:    zeroCount,
		count:        total,
		gosPopulated: populatedBucketCount(&store),
	}
	return nil
}

func wireStore(b *Buckets) ([]uint64, int32) {
	if b.IsEmpty() {
		return nil, 0
	}
	return b.counts.AsSlice(), b.offset
}

func storeFromWire(counts []uint64, offset int32) Buckets {
	if len(counts) == 0 {
		return Buckets{}
	}
	return Buckets{counts: storage.Vector1DFromVec(counts), offset: offset}
}

func checkAlpha(alpha float64) error {
	if !(alpha > 0 && alpha < 1) {
		return fmt.Errorf("ddsketch: alpha %v is not in (0, 1)", alpha)
	}
	return nil
}

// checkState applies the ASAPv1 decode rules to a store pair and zero count.
func checkState(counts []uint64, offset int32, negCounts []uint64, negOffset int32, zeroCount uint64) error {
	if err := checkSpan(counts, offset); err != nil {
		return err
	}
	if err := checkSpan(negCounts, negOffset); err != nil {
		return err
	}
	if _, ok := totalCount(counts, negCounts, zeroCount); !ok {
		return errors.New("ddsketch: bucket counts overflow the total sample count")
	}
	return nil
}

// checkSpan requires an empty store at offset 0 and a populated store whose
// highest index fits in int32.
func checkSpan(counts []uint64, offset int32) error {
	if len(counts) == 0 {
		if offset != 0 {
			return fmt.Errorf("ddsketch: empty store must be at offset 0, got %d", offset)
		}
		return nil
	}
	if int64(offset)+int64(len(counts))-1 > math.MaxInt32 {
		return fmt.Errorf("ddsketch: store span past int32: offset=%d, len=%d", offset, len(counts))
	}
	return nil
}

func totalCount(counts, negCounts []uint64, zeroCount uint64) (uint64, bool) {
	total := zeroCount
	for _, xs := range [][]uint64{counts, negCounts} {
		for _, c := range xs {
			next := total + c
			if next < total {
				return 0, false
			}
			total = next
		}
	}
	return total, true
}
