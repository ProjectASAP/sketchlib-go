package cocosketch

import (
	"errors"
	"math/rand"
	"sort"
	"time"

	"github.com/approx-telemetry/sketchlib-go/common"
)

// Aggregation determines how to combine values if a hash is found in multiple arrays simultaneously.
type Aggregation int

const (
	AggregateRaw    Aggregation = iota // Returns the first value found (fastest)
	AggregateSum                       // Sums up all occurrences across arrays
	AggregateMedian                    // Takes the median of all occurrences (Recommended by paper)
	AggregateMax                       // Takes the maximum value (Common in Count-Min)
)

// CocoSketch implements common.Sketch using the Cornucopia Sketch algorithm.
// It uses probabilistic replacement to manage heavy hitters in a fixed-size table.
type CocoSketch struct {
	d          int         // number of arrays
	length     int         // number of buckets per array
	keys       [][]uint64  // Stores the Hash itself as the key
	counts     [][]uint64  // Stores the frequency count
	rng        *rand.Rand  // Local RNG for probabilistic replacement
	defaultAgg Aggregation // Default aggregation method for QueryWithHash
}

// NewCocoSketch creates a new instance.
// d: number of arrays (hashes).
// length: size of each array.
func NewCocoSketch(d, length int) (*CocoSketch, error) {
	if d <= 0 || length <= 0 {
		return nil, errors.New("d and length must be > 0")
	}

	keys := make([][]uint64, d)
	counts := make([][]uint64, d)
	for i := 0; i < d; i++ {
		keys[i] = make([]uint64, length)
		counts[i] = make([]uint64, length)
	}

	// Use local random source
	src := rand.NewSource(time.Now().UnixNano())

	return &CocoSketch{
		d:          d,
		length:     length,
		keys:       keys,
		counts:     counts,
		rng:        rand.New(src),
		defaultAgg: AggregateMedian, // Recommended default
	}, nil
}

// SetAggregation changes the default aggregation strategy used by QueryWithHash.
func (c *CocoSketch) SetAggregation(agg Aggregation) {
	c.defaultAgg = agg
}

// TypeName returns the name of the sketch.
func (c *CocoSketch) TypeName() string {
	return "CocoSketch"
}

// positions generates d positions based on the input hash using double hashing logic.
func (c *CocoSketch) positions(h uint64) []int {
	pos := make([]int, c.d)
	// Split 64-bit hash into two 32-bit parts
	h1 := uint64(uint32(h & 0xffffffff))
	h2 := uint64(uint32((h >> 32) | 1)) // ensure odd
	mod := uint64(c.length)

	for i := 0; i < c.d; i++ {
		p := (h1 + uint64(i)*h2) % mod
		pos[i] = int(p)
	}
	return pos
}

// InsertWithHash inserts a hash into the sketch (value = 1).
// Implements common.Sketch.
func (c *CocoSketch) InsertWithHash(hash uint64) {
	pos := c.positions(hash)

	// 1) If same hash is found in candidates -> increment and finish
	for i := 0; i < c.d; i++ {
		if c.keys[i][pos[i]] == hash {
			c.counts[i][pos[i]]++
			return
		}
	}

	// 2) Choose the bucket with minimum counter
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

	// 3) Increment counter of the victim/target
	c.counts[minIdx][minPos]++
	C := c.counts[minIdx][minPos]

	// 4) Probabilistic replacement: replace key with probability 1/C
	if C == 0 {
		C = 1 // Should not happen after increment
	}
	if (c.rng.Uint64() % C) == 0 {
		c.keys[minIdx][minPos] = hash
	}
}

// QueryWithHash returns the estimated frequency of the hash.
// It uses the default aggregation strategy set by SetAggregation (default: Median).
// Implements common.Sketch.
func (c *CocoSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if q == common.QuerySum2 {
		// Not implemented for Coco yet
		return 0, nil
	}
	if q != common.QueryFrequency {
		return 0, common.ErrUnsupportedQuery
	}

	// Call internal function using default aggregation strategy
	return c.estimateSpecific(hash, c.defaultAgg), nil
}

// QuerySpecific allows querying with a specific Aggregation strategy manually.
func (c *CocoSketch) QuerySpecific(hash uint64, agg Aggregation) float64 {
	return c.estimateSpecific(hash, agg)
}

// estimateSpecific is the internal logic to collect values and aggregate them.
func (c *CocoSketch) estimateSpecific(hash uint64, agg Aggregation) float64 {
	pos := c.positions(hash)

	// Collect all counter values that match this hash
	var values []uint64

	for i := 0; i < c.d; i++ {
		if c.keys[i][pos[i]] == hash {
			val := c.counts[i][pos[i]]
			if agg == AggregateRaw {
				return float64(val) // Return immediately for Raw (First match)
			}
			values = append(values, val)
		}
	}

	if len(values) == 0 {
		return 0
	}

	// Process Aggregation
	switch agg {
	case AggregateSum:
		var sum uint64
		for _, v := range values {
			sum += v
		}
		return float64(sum)

	case AggregateMax:
		var maxVal uint64
		for _, v := range values {
			if v > maxVal {
				maxVal = v
			}
		}
		return float64(maxVal)

	case AggregateMedian:
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		n := len(values)
		if n%2 == 1 {
			return float64(values[n/2])
		}
		// Average of two middle values
		return float64(values[n/2-1]+values[n/2]) / 2.0

	default: // Fallback to Sum or Raw
		return float64(values[0])
	}
}

// Merge combines another sketch into this one.
// Implements common.Sketch.
func (c *CocoSketch) Merge(other common.Sketch) error {
	o, ok := other.(*CocoSketch)
	if !ok {
		return errors.New("cannot merge different sketch types")
	}

	if c.d != o.d || c.length != o.length {
		return errors.New("incompatible sketches: d/length mismatch")
	}

	for i := 0; i < c.d; i++ {
		for j := 0; j < c.length; j++ {
			add := o.counts[i][j]
			if add == 0 {
				continue
			}

			c.counts[i][j] += add
			total := c.counts[i][j]

			// Adopt key from other with probability add/total
			if total > 0 && (c.rng.Uint64()%total) < add {
				c.keys[i][j] = o.keys[i][j]
			}
		}
	}
	return nil
}

// Clear resets the sketch.
func (c *CocoSketch) Clear() {
	for i := 0; i < c.d; i++ {
		for j := 0; j < c.length; j++ {
			c.keys[i][j] = 0
			c.counts[i][j] = 0
		}
	}
}

type cocoSnapshot struct {
	D          int
	Length     int
	Keys       [][]uint64
	Counts     [][]uint64
	DefaultAgg Aggregation
}

// SerializeToBytes serializes CocoSketch into bytes.
func (c *CocoSketch) SerializeToBytes() ([]byte, error) {
	return common.EncodeToBytes(cocoSnapshot{
		D:          c.d,
		Length:     c.length,
		Keys:       c.keys,
		Counts:     c.counts,
		DefaultAgg: c.defaultAgg,
	})
}

// DeserializeCocoSketchFromBytes restores CocoSketch from serialized bytes.
func DeserializeCocoSketchFromBytes(data []byte) (*CocoSketch, error) {
	var snap cocoSnapshot
	if err := common.DecodeFromBytes(data, &snap); err != nil {
		return nil, err
	}
	if snap.D <= 0 || snap.Length <= 0 {
		return nil, errors.New("invalid snapshot dimensions")
	}
	if len(snap.Keys) != snap.D || len(snap.Counts) != snap.D {
		return nil, errors.New("invalid snapshot depth")
	}
	for i := 0; i < snap.D; i++ {
		if len(snap.Keys[i]) != snap.Length || len(snap.Counts[i]) != snap.Length {
			return nil, errors.New("invalid snapshot row length")
		}
	}

	src := rand.NewSource(time.Now().UnixNano())
	return &CocoSketch{
		d:          snap.D,
		length:     snap.Length,
		keys:       snap.Keys,
		counts:     snap.Counts,
		rng:        rand.New(src),
		defaultAgg: snap.DefaultAgg,
	}, nil
}
