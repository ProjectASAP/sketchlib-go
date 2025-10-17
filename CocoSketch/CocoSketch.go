// Package coco implements CocoSketch (Cornucopia Sketch) in Go.
// Features:
// - d-ary (number of arrays/hashes can be >2)
// - Insert: power-of-d choices + probabilistic replacement (1/C)
// - Merge: sketch-to-sketch & per-entry
// - Snapshot & Aggregation: raw, sum, median
//
// Note: this is a single-threaded version. For concurrent ingestion,
// use application-level sharding or protect Insert/Merge with a mutex.
package cocosketch

import (
	"errors"
	"math"
	"math/rand"
	"sort"
	"time"
)

// Hasher converts key K into a 64-bit hash.
// Simple implementation: FNV-1a 64 or xxhash64 (if adding extra lib).
type Hasher[K comparable] func(K) uint64

// Aggregation determines how to combine values across multiple arrays
// (when a key appears in >1 array). Paper usually recommends median.
type Aggregation int

const (
	AggregateRaw    Aggregation = iota // no combination; just use Snapshot()
	AggregateSum                       // sum across all arrays
	AggregateMedian                    // median across arrays (recommended)
)

// Entry is the payload for MergeEntry (per-key).
type Entry[K comparable] struct {
	Key   K
	Value uint64
	// If PositionsValid=false, positions will be computed from hash(key).
	Positions      []int
	PositionsValid bool
}

// Coco is the main CocoSketch structure.
type Coco[K comparable] struct {
	d      int   // number of arrays/hashes (d)
	length int   // number of buckets per array
	keys   [][]K // [d][length]
	counts [][]uint64
	hasher Hasher[K]
	rng    *rand.Rand
}

// NewCoco creates a sketch with d arrays and length buckets per array.
func NewCoco[K comparable](d, length int, hasher Hasher[K]) (*Coco[K], error) {
	if d <= 0 || length <= 0 {
		return nil, errors.New("d and length must be > 0")
	}
	if hasher == nil {
		return nil, errors.New("hasher must not be nil")
	}
	keys := make([][]K, d)
	counts := make([][]uint64, d)
	for i := 0; i < d; i++ {
		keys[i] = make([]K, length)
		counts[i] = make([]uint64, length)
	}
	src := rand.NewSource(time.Now().UnixNano())
	return &Coco[K]{
		d:      d,
		length: length,
		keys:   keys,
		counts: counts,
		hasher: hasher,
		rng:    rand.New(src),
	}, nil
}

// positions uses double hashing to generate candidate positions
// for each array: pos[i] = (h1 + i*h2) % length, with h2 odd.
func (c *Coco[K]) positions(h uint64) []int {
	pos := make([]int, c.d)
	// split into 2x32-bit
	h1 := uint64(uint32(h & 0xffffffff))
	h2 := uint64(uint32((h >> 32) | 1)) // ensure non-zero (usually odd)
	mod := uint64(c.length)
	for i := 0; i < c.d; i++ {
		p := (h1 + uint64(i)*h2) % mod
		pos[i] = int(p)
	}
	return pos
}

// Insert adds 1 to an item (full key) using power-of-d choices.
func (c *Coco[K]) Insert(item K) {
	h := c.hasher(item)
	pos := c.positions(h)

	// 1) if same key is found in candidates → increment and finish
	for i := 0; i < c.d; i++ {
		if c.keys[i][pos[i]] == item {
			c.counts[i][pos[i]]++
			return
		}
	}

	// 2) choose the bucket with minimum counter
	minIdx := 0
	minPos := pos[0]
	minVal := c.counts[0][minPos]
	for i := 1; i < c.d; i++ {
		v := c.counts[i][pos[i]]
		if v < minVal {
			minVal = v
			minIdx = i
			minPos = pos[i]
		}
	}

	// 3) increment counter
	c.counts[minIdx][minPos]++
	C := c.counts[minIdx][minPos]
	// 4) probabilistic replacement: replace key with probability 1/C
	if C == 0 {
		// won't happen because we just incremented
		C = 1
	}
	if (c.rng.Uint64() % C) == 0 {
		c.keys[minIdx][minPos] = item
	}
}

// Merge combines another sketch into this one, following rules:
// counts[i][j] += other.counts[i][j]
// and probability to adopt key from other = other.counts / new counts.
func (c *Coco[K]) Merge(other *Coco[K]) error {
	if other == nil {
		return nil
	}
	if c.d != other.d || c.length != other.length {
		return errors.New("incompatible sketches: d/length mismatch")
	}
	for i := 0; i < c.d; i++ {
		for j := 0; j < c.length; j++ {
			add := other.counts[i][j]
			if add == 0 {
				continue
			}
			c.counts[i][j] += add
			total := c.counts[i][j]
			// adopt key from other with probability add/total
			// realization: rng%total < add
			if (c.rng.Uint64() % total) < add {
				c.keys[i][j] = other.keys[i][j]
			}
		}
	}
	return nil
}

// MergeEntry combines a single entry (e.g., from a shard).
// If PositionsValid, use Positions (length must == d).
// Otherwise, compute from hash(key).
func (c *Coco[K]) MergeEntry(e Entry[K]) error {
	if e.Value == 0 {
		return nil
	}
	var pos []int
	if e.PositionsValid {
		if len(e.Positions) != c.d {
			return errors.New("entry positions length != d")
		}
		pos = make([]int, c.d)
		for i := 0; i < c.d; i++ {
			pi := e.Positions[i] % c.length
			if pi < 0 {
				pi += c.length
			}
			pos[i] = pi
		}
	} else {
		pos = c.positions(c.hasher(e.Key))
	}

	// 1) if same key exists in candidates → add value, finish
	for i := 0; i < c.d; i++ {
		if c.keys[i][pos[i]] == e.Key {
			c.counts[i][pos[i]] += e.Value
			return nil
		}
	}

	// 2) choose bucket with minimum counter among candidates
	minIdx := 0
	minPos := pos[0]
	minVal := c.counts[0][minPos]
	for i := 1; i < c.d; i++ {
		v := c.counts[i][pos[i]]
		if v < minVal {
			minVal = v
			minIdx = i
			minPos = pos[i]
		}
	}

	// 3) add value
	c.counts[minIdx][minPos] += e.Value
	C := c.counts[minIdx][minPos]
	if C == 0 {
		C = 1
	}
	// 4) probabilistic replacement: rng%count < value
	if (c.rng.Uint64() % C) < e.Value {
		c.keys[minIdx][minPos] = e.Key
	}
	return nil
}

// Snapshot returns a map key → slice of values (one per array) for keys
// recorded in buckets. Useful for post-processing/reconstruction.
// Note: zero-value keys may appear; caller should filter if necessary.
func (c *Coco[K]) Snapshot() map[K][]uint64 {
	m := make(map[K][]uint64)
	for i := 0; i < c.d; i++ {
		for j := 0; j < c.length; j++ {
			k := c.keys[i][j]
			v := c.counts[i][j]
			if _, ok := m[k]; !ok {
				m[k] = make([]uint64, 0, c.d)
			}
			m[k] = append(m[k], v)
		}
	}
	return m
}

// Table returns aggregated estimates per key using an aggregation method.
// - AggregateSum: sum of values across arrays
// - AggregateMedian: median of values across arrays
// Note: zero-value keys & zero counts can be filtered by caller if desired.
func (c *Coco[K]) Table(agg Aggregation) map[K]uint64 {
	raw := c.Snapshot()
	out := make(map[K]uint64, len(raw))
	switch agg {
	case AggregateSum:
		for k, arr := range raw {
			var s uint64
			for _, x := range arr {
				s += x
			}
			out[k] = s
		}
	case AggregateMedian:
		for k, arr := range raw {
			cp := append([]uint64(nil), arr...)
			sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
			n := len(cp)
			if n == 0 {
				continue
			}
			var med uint64
			if n%2 == 1 {
				med = cp[n/2]
			} else {
				// even-length → average of two middle values (floored)
				med = (cp[n/2-1] + cp[n/2]) / 2
			}
			out[k] = med
		}
	default:
		// AggregateRaw → return one value (e.g., the first)
		for k, arr := range raw {
			if len(arr) > 0 {
				out[k] = arr[0]
			}
		}
	}
	return out
}

// ====== Example built-in hashers (optional) ======

// FNV1a64 for string (simple, no dependency).
func FNV1a64String(s string) uint64 {
	var h uint64 = 1469598103934665603
	const prime = 1099511628211
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

// FNV1a64Bytes for []byte.
func FNV1a64Bytes(b []byte) uint64 {
	var h uint64 = 1469598103934665603
	const prime = 1099511628211
	for _, x := range b {
		h ^= uint64(x)
		h *= prime
	}
	return h
}

// FNV1a64Uint64 for uint64 (fold into small bytes).
func FNV1a64Uint64(x uint64) uint64 {
	// split into two 32-bit parts as a simple variation
	return FNV1a64Bytes([]byte{
		byte(x), byte(x >> 8), byte(x >> 16), byte(x >> 24),
		byte(x >> 32), byte(x >> 40), byte(x >> 48), byte(x >> 56),
	})
}

// ====== Small utilities ======

// EstimatedMemoryBytes estimates memory footprint for current configuration.
func (c *Coco[K]) EstimatedMemoryBytes() int {
	// Estimate: counts (uint64) + keys (size unknown).
	// Since size of generic K is not known at runtime in Go portably,
	// we only count counts: d * length * 8 bytes.
	return c.d * c.length * 8
}

// HeavyThreshold returns heavy hitter threshold based on totalCount and theta.
func HeavyThreshold(totalCount uint64, theta float64) uint64 {
	if theta <= 0 {
		return math.MaxUint64
	}
	v := float64(totalCount) * theta
	if v < 1 {
		v = 1
	}
	return uint64(v)
}
