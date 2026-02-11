package ddsketch

import (
	"errors"
	"math"
)

const GrowChunk = 128

// ---------------- Index Mapping ----------------

type IndexMapping struct {
	gamma       float64
	invLogGamma float64
}

func NewIndexMapping(alpha float64) IndexMapping {
	if !(alpha > 0 && alpha < 1) {
		panic("alpha must be in (0,1)")
	}

	gamma := (1 + alpha) / (1 - alpha)

	return IndexMapping{
		gamma:       gamma,
		invLogGamma: 1 / math.Log(gamma),
	}
}

func (m IndexMapping) Equals(other IndexMapping) bool {
	return math.Abs(m.gamma-other.gamma) < 1e-15
}

func (m IndexMapping) Index(v float64) int32 {
	return int32(math.Floor(math.Log(v) * m.invLogGamma))
}

func (m IndexMapping) Value(k int32) float64 {
	return math.Pow(m.gamma, float64(k)+0.5)
}

// ---------------- Buckets ----------------

type Buckets struct {
	counts []uint64
	offset int32
}

func (b *Buckets) IsEmpty() bool {
	return len(b.counts) == 0
}

func (b *Buckets) Range() (int32, int32, bool) {
	if len(b.counts) == 0 {
		return 0, 0, false
	}
	left := b.offset
	right := b.offset + int32(len(b.counts)) - 1
	return left, right, true
}

func (b *Buckets) ensure(k int32) {
	if len(b.counts) == 0 {
		b.counts = make([]uint64, GrowChunk)
		b.offset = k - int32(GrowChunk/2)
		return
	}

	left, right, _ := b.Range()

	if k < left {
		needed := int(left - k)
		grow := maxInt(needed, GrowChunk)

		newCounts := make([]uint64, grow+len(b.counts))
		copy(newCounts[grow:], b.counts)
		b.counts = newCounts
		b.offset -= int32(grow)

	} else if k > right {
		needed := int(k - right)
		grow := maxInt(needed, GrowChunk)
		b.counts = append(b.counts, make([]uint64, grow)...)
	}
}

func (b *Buckets) addOne(k int32) {
	idx := k - b.offset
	if idx >= 0 {
		i := int(idx)
		if i < len(b.counts) {
			b.counts[i]++
			return
		}
	}
	b.ensure(k)
	b.counts[int(k-b.offset)]++
}

func (b Buckets) Clone() Buckets {
	cp := make([]uint64, len(b.counts))
	copy(cp, b.counts)
	return Buckets{counts: cp, offset: b.offset}
}

// ---------------- DDSketch ----------------

// High-performance latency quantile sketch (v > 0 only)

type DDSketch struct {
	mapping IndexMapping
	store   Buckets

	count uint64
	sum   float64
	min   float64
	max   float64
}

func NewDDSketch(alpha float64) *DDSketch {
	return &DDSketch{
		mapping: NewIndexMapping(alpha),
		min:     math.Inf(1),
		max:     math.Inf(-1),
	}
}

// Strictly positive values only
func (d *DDSketch) Add(v float64) {
	if !(v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)) {
		return // ignore invalid or non-positive
	}

	d.count++
	d.sum += v

	if v < d.min {
		d.min = v
	}
	if v > d.max {
		d.max = v
	}

	k := d.mapping.Index(v)
	d.store.addOne(k)
}

func (d *DDSketch) GetCount() uint64 {
	return d.count
}

// ---------------- Safe Merge ----------------

func (d *DDSketch) Merge(other *DDSketch) error {

	if !d.mapping.Equals(other.mapping) {
		return errors.New("cannot merge sketches with different index mappings")
	}

	if other.count == 0 {
		return nil
	}
	if d.count == 0 {
		*d = *other.Clone()
		return nil
	}

	d.count += other.count
	d.sum += other.sum

	if other.min < d.min {
		d.min = other.min
	}
	if other.max > d.max {
		d.max = other.max
	}

	d.mergeBuckets(&d.store, &other.store)

	return nil
}

func (d *DDSketch) mergeBuckets(a *Buckets, b *Buckets) {

	if b.IsEmpty() {
		return
	}
	if a.IsEmpty() {
		*a = b.Clone()
		return
	}

	al, ar, _ := a.Range()
	bl, br, _ := b.Range()

	newL := minInt32(al, bl)
	newR := maxInt32(ar, br)
	newLen := int(newR-newL) + 1

	merged := make([]uint64, newLen)

	for i, c := range a.counts {
		k := al + int32(i)
		merged[int(k-newL)] += c
	}
	for i, c := range b.counts {
		k := bl + int32(i)
		merged[int(k-newL)] += c
	}

	a.counts = merged
	a.offset = newL
}

// ---------------- Quantile ----------------

func (d *DDSketch) GetValueAtQuantile(q float64) (float64, bool) {

	if d.count == 0 || q < 0 || q > 1 {
		return 0, false
	}

	if q == 0 {
		return d.min, true
	}
	if q == 1 {
		return d.max, true
	}

	rank := uint64(math.Ceil(q * float64(d.count)))

	var seen uint64

	for i, c := range d.store.counts {
		if c == 0 {
			continue
		}
		seen += c
		if seen >= rank {
			k := d.store.offset + int32(i)
			v := d.mapping.Value(k)

			// clamp for safety
			if v < d.min {
				v = d.min
			}
			if v > d.max {
				v = d.max
			}
			return v, true
		}
	}

	return d.max, true
}

func (d *DDSketch) Clone() *DDSketch {
	return &DDSketch{
		mapping: d.mapping,
		store:   d.store.Clone(),
		count:   d.count,
		sum:     d.sum,
		min:     d.min,
		max:     d.max,
	}
}

// ---------------- Utils ----------------

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
