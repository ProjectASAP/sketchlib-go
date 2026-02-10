package ddsketch

import (
	"errors"
	"math"
)

// Number of buckets to grow by when expanding.
const GrowChunk = 128

// ---------------- Buckets ----------------

type Buckets struct {
	counts []uint64
	offset int32
}

func NewBuckets() Buckets {
	return Buckets{
		counts: nil,
		offset: 0,
	}
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

// ---------------- DDSketch ----------------

type DDSketch struct {
	alpha       float64
	gamma       float64
	logGamma    float64
	invLogGamma float64
	store       Buckets
	count       uint64
	sum         float64
	min         float64
	max         float64
}

func NewDDSketch(alpha float64) *DDSketch {
	if !(alpha > 0 && alpha < 1) {
		panic("alpha must be in (0,1)")
	}

	gamma := (1 + alpha) / (1 - alpha)
	logGamma := math.Log(gamma)

	return &DDSketch{
		alpha:       alpha,
		gamma:       gamma,
		logGamma:    logGamma,
		invLogGamma: 1 / logGamma,
		store:       NewBuckets(),
		count:       0,
		sum:         0,
		min:         math.Inf(1),
		max:         math.Inf(-1),
	}
}

func (d *DDSketch) Add(v float64) {
	if !(v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)) {
		return
	}

	d.count++
	d.sum += v

	if v < d.min {
		d.min = v
	}
	if v > d.max {
		d.max = v
	}

	k := d.keyFor(v)
	d.store.addOne(k)
}

func (d *DDSketch) keyFor(v float64) int32 {
	return int32(math.Floor(math.Log(v) * d.invLogGamma))
}

func (d *DDSketch) binRepresentative(k int32) float64 {
	return math.Pow(d.gamma, float64(k)+0.5)
}

func (d *DDSketch) GetValueAtQuantile(q float64) (float64, bool) {
	if d.count == 0 || math.IsNaN(q) {
		return 0, false
	}
	if q <= 0 {
		return d.min, true
	}
	if q >= 1 {
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
			bin := d.store.offset + int32(i)
			v := d.binRepresentative(bin)

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

func (d *DDSketch) Merge(other *DDSketch) error {
	if math.Abs(d.alpha-other.alpha) > 1e-12 {
		return errors.New("alpha mismatch")
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

	d.mergeBucketsFrom(other)
	return nil
}

func (d *DDSketch) mergeBucketsFrom(o *DDSketch) {
	if o.store.IsEmpty() {
		return
	}
	if d.store.IsEmpty() {
		d.store = o.store.Clone()
		return
	}

	sl, sr, _ := d.store.Range()
	ol, or, _ := o.store.Range()

	newL := minInt32(sl, ol)
	newR := maxInt32(sr, or)
	newLen := int(newR-newL) + 1

	merged := make([]uint64, newLen)

	for i, c := range d.store.counts {
		k := sl + int32(i)
		merged[int(k-newL)] += c
	}
	for i, c := range o.store.counts {
		k := ol + int32(i)
		merged[int(k-newL)] += c
	}

	d.store.counts = merged
	d.store.offset = newL
}

func (d *DDSketch) Clone() *DDSketch {
	cp := *d
	cp.store = d.store.Clone()
	return &cp
}

func (b Buckets) Clone() Buckets {
	cp := make([]uint64, len(b.counts))
	copy(cp, b.counts)
	return Buckets{
		counts: cp,
		offset: b.offset,
	}
}

// ---------------- utils ----------------

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
