package ddsketch

import (
	"errors"
	"fmt"
	"math"

	"github.com/ProjectASAP/sketchlib-go/common/storage"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// MarshalASAPv1 encodes the sketch as an ASAPv1 DDSketch envelope. It writes
// metadata version 1 for a positive-only state and version 2 when the sketch
// holds a negative store or a nonzero zero count.
func (d *DDSketch) MarshalASAPv1() ([]byte, error) {
	alpha := d.mapping.alpha
	if err := checkAlpha(alpha); err != nil {
		return nil, err
	}
	counts, offset := wireStore(&d.store)
	negCounts, negOffset := wireStore(&d.negative)
	signed := len(negCounts) > 0 || d.zeroCount != 0
	if err := checkState(signed, counts, offset, negCounts, negOffset, d.zeroCount, d.sum, d.min, d.max); err != nil {
		return nil, err
	}

	version, fields := uint8(1), 5
	if signed {
		version, fields = 2, 8
	}
	md := asapv1.NewMetadataWriter(version)
	md.Float64("alpha", alpha)
	p := asapv1.NewEncoder()
	p.Array(fields)
	asapv1.EncodeUints(p, counts)
	p.Int(int64(offset))
	p.Float64(d.sum)
	p.Float64(d.min)
	p.Float64(d.max)
	if signed {
		asapv1.EncodeUints(p, negCounts)
		p.Int(int64(negOffset))
		p.Uint(d.zeroCount)
	}
	return asapv1.Marshal(asapv1.KindDDSketch, md, p)
}

// UnmarshalASAPv1 replaces the sketch with the one encoded in b, accepting
// metadata versions 1 and 2. The result has no bin cap and no sampler.
func (d *DDSketch) UnmarshalASAPv1(b []byte) error {
	md, p, err := asapv1.Open(b, asapv1.KindDDSketch)
	if err != nil {
		return err
	}
	md.ExpectVersion(1, 2)
	alpha := md.Float64("alpha")
	if err := md.Finish(); err != nil {
		return err
	}
	if err := checkAlpha(alpha); err != nil {
		return err
	}
	signed := md.Version() == 2

	if signed {
		p.ExpectArray(8)
	} else {
		p.ExpectArray(5)
	}
	counts := asapv1.DecodeUints[uint64](p)
	offset := p.Int32()
	sum, minV, maxV := p.Float64(), p.Float64(), p.Float64()
	var negCounts []uint64
	var negOffset int32
	var zeroCount uint64
	if signed {
		negCounts = asapv1.DecodeUints[uint64](p)
		negOffset = p.Int32()
		zeroCount = p.Uint()
	}
	if err := p.Finish(); err != nil {
		return err
	}
	if err := checkState(signed, counts, offset, negCounts, negOffset, zeroCount, sum, minV, maxV); err != nil {
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
		sum:          sum,
		min:          minV,
		max:          maxV,
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

// checkState applies the ASAPv1 decode rules to a store pair, zero count and
// running scalars. A state with no negative or zero mass gets the positive-only
// scalar rule even when it is written as version 2.
func checkState(signed bool, counts []uint64, offset int32, negCounts []uint64, negOffset int32,
	zeroCount uint64, sum, minV, maxV float64) error {
	if err := checkSpan(counts, offset); err != nil {
		return err
	}
	if signed {
		if err := checkSpan(negCounts, negOffset); err != nil {
			return err
		}
	}
	total, ok := totalCount(counts, negCounts, zeroCount)
	if !ok {
		return errors.New("ddsketch: bucket counts overflow the total sample count")
	}
	if total == 0 {
		if sum != 0 || !math.IsInf(minV, 1) || !math.IsInf(maxV, -1) {
			return fmt.Errorf("ddsketch: empty sketch must carry sum=0, min=+Inf, max=-Inf, got %v, %v, %v", sum, minV, maxV)
		}
		return nil
	}
	if !isFinite(sum) || !isFinite(minV) || !isFinite(maxV) {
		return fmt.Errorf("ddsketch: scalars must be finite for a populated sketch: %v, %v, %v", sum, minV, maxV)
	}
	negTotal, _ := totalCount(negCounts, nil, zeroCount)
	if negTotal == 0 {
		if !(minV > 0 && minV <= maxV && sum >= minV) {
			return fmt.Errorf("ddsketch: scalars out of order: sum=%v, min=%v, max=%v", sum, minV, maxV)
		}
	} else if !(minV <= maxV) {
		return fmt.Errorf("ddsketch: min %v above max %v", minV, maxV)
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

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
