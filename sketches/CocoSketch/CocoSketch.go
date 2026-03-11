package cocosketch

import (
	"errors"
	"math/rand"
	"sort"
	"time"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
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
	d          int // number of arrays
	length     int // number of buckets per array
	keysStore  *storage.Vector2D[uint64]
	countStore *storage.Vector2D[uint64]
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

	keysStore, err := storage.InitVector2D[uint64](d, length)
	if err != nil {
		return nil, err
	}
	countStore, err := storage.InitVector2D[uint64](d, length)
	if err != nil {
		return nil, err
	}

	// Use local random source
	src := rand.NewSource(time.Now().UnixNano())

	return &CocoSketch{
		d:          d,
		length:     length,
		keysStore:  keysStore,
		countStore: countStore,
		keys:       keysStore.As2D(),
		counts:     countStore.As2D(),
		rng:        rand.New(src),
		defaultAgg: AggregateMedian, // Recommended default
	}, nil
}

// SetAggregation changes the default aggregation strategy used by QueryWithHash.
func (c *CocoSketch) SetAggregation(agg Aggregation) {
	c.defaultAgg = agg
}

// SetSeed sets a deterministic RNG seed (useful for tests/bench reproducibility).
func (c *CocoSketch) SetSeed(seed int64) {
	c.rng = rand.New(rand.NewSource(seed))
}

// TypeName returns the name of the sketch.
func (c *CocoSketch) TypeName() string {
	return "CocoSketch"
}

func splitHash(hash uint64) (uint64, uint64) {
	h1 := uint64(uint32(hash & 0xffffffff))
	h2 := uint64(uint32((hash >> 32) | 1)) // ensure odd
	return h1, h2
}

func positionAt(i int, h1, h2, mod uint64) int {
	return int((h1 + uint64(i)*h2) % mod)
}

// InsertWithHash inserts a hash into the sketch (value = 1).
// Implements common.Sketch.
func (c *CocoSketch) InsertWithHash(hash uint64) {
	h1, h2 := splitHash(hash)
	mod := uint64(c.length)
	keys := c.keys
	counts := c.counts

	// 1) If same hash is found in candidates -> increment and finish
	for i := 0; i < c.d; i++ {
		p := positionAt(i, h1, h2, mod)
		if keys[i][p] == hash {
			counts[i][p]++
			return
		}
	}

	// 2) Choose the bucket with minimum counter
	minIdx := 0
	minPos := positionAt(0, h1, h2, mod)
	minVal := counts[0][minPos]

	for i := 1; i < c.d; i++ {
		p := positionAt(i, h1, h2, mod)
		v := counts[i][p]
		if v < minVal {
			minVal = v
			minIdx = i
			minPos = p
		}
	}

	// 3) Increment counter of the victim/target
	counts[minIdx][minPos]++
	C := counts[minIdx][minPos]

	// 4) Probabilistic replacement: replace key with probability 1/C
	if C == 0 {
		C = 1 // Should not happen after increment
	}
	if (c.rng.Uint64() % C) == 0 {
		keys[minIdx][minPos] = hash
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
	h1, h2 := splitHash(hash)
	mod := uint64(c.length)
	keys := c.keys
	counts := c.counts

	// Collect all counter values that match this hash
	var valuesStack [16]uint64
	values := valuesStack[:0]
	if c.d > len(valuesStack) {
		values = make([]uint64, 0, c.d)
	}

	for i := 0; i < c.d; i++ {
		p := positionAt(i, h1, h2, mod)
		if keys[i][p] == hash {
			val := counts[i][p]
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
		countRow := c.counts[i]
		keyRow := c.keys[i]
		for j := 0; j < c.length; j++ {
			add := o.counts[i][j]
			if add == 0 {
				continue
			}

			countRow[j] += add
			total := countRow[j]

			// Adopt key from other with probability add/total
			if total > 0 && (c.rng.Uint64()%total) < add {
				keyRow[j] = o.keys[i][j]
			}
		}
	}
	return nil
}

// Clear resets the sketch.
func (c *CocoSketch) Clear() {
	for i := 0; i < c.d; i++ {
		keyRow := c.keys[i]
		countRow := c.counts[i]
		for j := 0; j < c.length; j++ {
			keyRow[j] = 0
			countRow[j] = 0
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

	keysStore, err := storage.Vector2DFrom2D[uint64](snap.Keys)
	if err != nil {
		return nil, err
	}
	countStore, err := storage.Vector2DFrom2D[uint64](snap.Counts)
	if err != nil {
		return nil, err
	}

	src := rand.NewSource(time.Now().UnixNano())
	return &CocoSketch{
		d:          snap.D,
		length:     snap.Length,
		keysStore:  keysStore,
		countStore: countStore,
		keys:       keysStore.As2D(),
		counts:     countStore.As2D(),
		rng:        rand.New(src),
		defaultAgg: snap.DefaultAgg,
	}, nil
}
