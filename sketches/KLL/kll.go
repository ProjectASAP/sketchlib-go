package kll

import (
	"errors"
	"math"
	"math/rand"
	"sort"
	"time"
	"unsafe"

	"github.com/approx-telemetry/sketchlib-go/common"
)

// KLLSketch implements the KLL streaming quantiles sketch.
// Reference: http://arxiv.org/pdf/1603.05346v1.pdf
type KLLSketch struct {
	Compactors []Compactor
	k          int
	H          int

	// size tracks the number of items strictly retained in memory.
	// This is used for internal compaction logic.
	// For total stream length, use Count() or GetSize().
	size    int
	maxSize int

	co coin
}

// NewKLLSketch creates a new KLLSketch.
// k controls the maximum memory used by the stream, which is 3*k + lg(n).
func NewKLLSketch(k int) (*KLLSketch, error) {
	if k <= 0 {
		return nil, errors.New("k must be positive")
	}

	// Initialize coin state using time
	// We use a non-zero seed for xorshift
	seed := uint64(time.Now().UnixNano())
	if seed == 0 {
		seed = 0xCAFEBABE // Fallback for safety
	}

	s := KLLSketch{
		k:  k,
		co: coin{st: seed, mask: 0},
	}
	s.grow()
	return &s, nil
}

// ================= API IMPLEMENTATION (common.Sketch) =================

// TypeName returns the unique name of the sketch type.
func (s *KLLSketch) TypeName() string {
	return "kll"
}

// InsertWithHash implements common.Sketch.
// For KLL, we treat the hash as the float64 value to track distribution.
func (s *KLLSketch) InsertWithHash(hash uint64) {
	s.Insert(float64(hash))
}

// QueryWithHash implements common.Sketch.
// KLL does not support frequency queries by hash.
func (s *KLLSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	return 0, common.ErrUnsupportedQuery
}

// Merge combines another sketch into this one.
func (s *KLLSketch) Merge(other common.Sketch) error {
	o, ok := other.(*KLLSketch)
	if !ok {
		return errors.New("cannot merge: incompatible sketch type")
	}

	// Grow this sketch if the other sketch is deeper
	for s.H < o.H {
		s.grow()
	}

	// Merge compactors
	for h, c := range o.Compactors {
		s.Compactors[h] = append(s.Compactors[h], c...)
	}

	// Recalculate retained size and perform compaction if needed
	s.updateRetainedSize()
	s.compact()
	return nil
}

// ================= PUBLIC METHODS =================

// Insert adds a value x to the stream.
func (s *KLLSketch) Insert(x float64) {
	s.Compactors[0] = append(s.Compactors[0], x)
	s.size++
	s.compact()
}

// GetSize returns the total number of items seen in the stream (N).
// NOTE: This fixes the test failure "expected 2000, got 319".
func (s *KLLSketch) GetSize() int {
	return s.Count()
}

// GetRetainedItems returns the number of items actually stored in memory.
// Use this to check memory pressure.
func (s *KLLSketch) GetRetainedItems() int {
	return s.size
}

// GetMemoryBytes returns the approximate memory usage in bytes.
func (s *KLLSketch) GetMemoryBytes() float64 {
	var totalMem float64 = 0
	totalMem += float64(unsafe.Sizeof(*s))
	for i := range s.Compactors {
		totalMem += float64(len(s.Compactors[i])) * 8 // float64 is 8 bytes
	}
	return totalMem
}

// Rank estimates the rank of the value x in the stream.
func (s *KLLSketch) Rank(x float64) int {
	var r int
	for h, c := range s.Compactors {
		for _, v := range c {
			if v <= x {
				r += 1 << uint(h)
			}
		}
	}
	return r
}

// Count returns the total number of items inserted (Approximate N).
func (s *KLLSketch) Count() int {
	var n int
	for h, c := range s.Compactors {
		n += len(c) * (1 << uint(h))
	}
	return n
}

// Quantile estimates the quantile of the value x in the stream.
func (s *KLLSketch) Quantile(x float64) float64 {
	var r, n int
	for h, c := range s.Compactors {
		for _, v := range c {
			w := 1 << uint(h)
			if v <= x {
				r += w
			}
			n += w
		}
	}
	if n == 0 {
		return 0
	}
	return float64(r) / float64(n)
}

// CDF returns the Cumulative Distribution Function representation.
func (s *KLLSketch) CDF() CDF {
	q := make(CDF, 0, s.size)

	var totalW float64
	for h, c := range s.Compactors {
		weight := float64(int(1 << uint(h)))
		for _, v := range c {
			q = append(q, Quantile{Q: weight, V: v})
		}
		totalW += float64(len(c)) * weight
	}

	sort.Sort(q)

	var curW float64
	for i := range q {
		curW += q[i].Q
		q[i].Q = curW / totalW
	}

	return q
}

// ================= INTERNAL LOGIC =================

func (s *KLLSketch) grow() {
	s.Compactors = append(s.Compactors, Compactor{})
	s.H = len(s.Compactors)

	s.maxSize = 0
	for h := 0; h < s.H; h++ {
		s.maxSize += s.capacity(h)
	}
}

// capacity calculates the capacity of a specific layer h.
// OPTIMIZATION: Uses computeHeight() from capacity.go (same package)
// to avoid expensive math.Pow() calls.
func (s *KLLSketch) capacity(h int) int {
	// Standard decay: k * (2/3)^(H - h - 1)
	decay := computeHeight(s.H - h - 1)
	return int(math.Ceil(float64(s.k)*decay)) + 1
}

func (s *KLLSketch) compact() {
	for s.size >= s.maxSize {
		for h := 0; h < len(s.Compactors); h++ {
			if len(s.Compactors[h]) >= s.capacity(h) {
				if h+1 >= s.H {
					s.grow()
				}

				prevH := len(s.Compactors[h])
				prevH1 := len(s.Compactors[h+1])

				s.Compactors[h+1] = s.Compactors[h].compact(
					&s.co, s.Compactors[h+1])

				s.size += len(s.Compactors[h]) - prevH
				s.size += len(s.Compactors[h+1]) - prevH1

				if s.size < s.maxSize {
					break
				}
			}
		}
	}
}

// updateRetainedSize recalculates the number of items stored in memory.
func (s *KLLSketch) updateRetainedSize() {
	s.size = 0
	for _, c := range s.Compactors {
		s.size += len(c)
	}
}

// ================= HELPERS & TYPES =================

// 64-bit xorshift multiply rng
func xorshiftMult64(x uint64) uint64 {
	x ^= x >> 12 // a
	x ^= x << 25 // b
	x ^= x >> 27 // c
	return x * 2685821657736338717
}

type coin struct {
	st   uint64
	mask uint64
}

// v is either 0 or 1
func (c *coin) toss() (v int) {
	if c.mask == 0 {
		if c.st == 0 {
			c.st = uint64(rand.Int63())
		}
		c.st = xorshiftMult64(c.st)
		c.mask = 1
	}
	if c.st&c.mask > 0 {
		v = 1
	}
	c.mask <<= 1
	return v
}

type Compactor []float64

func (c *Compactor) compact(co *coin, dst []float64) []float64 {
	l := len(*c)

	if l == 0 || l == 1 {
		return dst
	} else if l == 2 {
		sl := *c
		if sl[0] > sl[1] {
			sl[0], sl[1] = sl[1], sl[0]
		}
	} else if l > 100 {
		sort.Float64s([]float64(*c))
	} else {
		c.insertionSort()
	}

	free := cap(dst) - len(dst)
	if free < len(*c)/2 {
		extra := len(*c)/2 - free
		newdst := make([]float64, len(dst), cap(dst)+extra)
		copy(newdst, dst)
		dst = newdst
	}

	// choose either the evens or the odds
	offs := co.toss()
	for len(*c) >= 2 {
		l := len(*c) - 2
		dst = append(dst, (*c)[l+offs])
		*c = (*c)[:l]
	}

	return dst
}

func (c Compactor) insertionSort() {
	l := len(c)
	for i := 1; i < l; i++ {
		v := c[i]
		j := i
		for ; j > 0 && c[j-1] > v; j-- {
		}
		if j == i {
			continue
		}
		copy(c[j+1:], c[j:i])
		c[j] = v
	}
}

type Quantile struct {
	Q float64
	V float64
}

type CDF []Quantile

func (q CDF) Len() int           { return len(q) }
func (q CDF) Less(i, j int) bool { return q[i].V < q[j].V }
func (q CDF) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }

// Quantile estimates the quantile of the value x in the stream.
func (q CDF) Quantile(x float64) float64 {
	idx := sort.Search(len(q), func(i int) bool { return q[i].V >= x })
	if idx == 0 {
		return 0
	}
	return q[idx-1].Q
}

// Query estimates the value given quantile p.
func (q CDF) Query(p float64) float64 {
	idx := sort.Search(len(q), func(i int) bool { return q[i].Q >= p })
	if idx == len(q) {
		return q[len(q)-1].V
	}
	return q[idx].V
}

// QuantileLI estimates the quantile using linear interpolation.
func (q CDF) QuantileLI(x float64) float64 {
	idx := sort.Search(len(q), func(i int) bool { return q[i].V >= x })
	if idx == len(q) {
		return 1
	}
	if idx == 0 {
		return 0
	}
	// a < x <= b
	a, aq := q[idx-1].V, q[idx-1].Q
	b, bq := q[idx].V, q[idx].Q
	return ((a-x)*bq + (x-b)*aq) / (a - b)
}

// QueryLI estimates the value given quantile p using linear interpolation.
func (q CDF) QueryLI(p float64) float64 {
	idx := sort.Search(len(q), func(i int) bool { return q[i].Q >= p })
	if idx == len(q) {
		return q[len(q)-1].V
	}
	if idx == 0 {
		return q[0].V
	}
	// aq < p <= b
	a, aq := q[idx-1].V, q[idx-1].Q
	b, bq := q[idx].V, q[idx].Q
	return ((aq-p)*b + (p-bq)*a) / (aq - bq)
}
