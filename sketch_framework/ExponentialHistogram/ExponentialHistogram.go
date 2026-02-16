package exponentialhistogram

import (
	"errors"
	"sync"

	"github.com/approx-telemetry/sketchlib-go/common"

	// Import specific sketch implementations
	univmon "github.com/approx-telemetry/sketchlib-go/sketch_framework/UnivMon"
	countsketch "github.com/approx-telemetry/sketchlib-go/sketches/CountSketch"
	hll "github.com/approx-telemetry/sketchlib-go/sketches/HLL"
	kll "github.com/approx-telemetry/sketchlib-go/sketches/KLL"
)

// ==============================================================================
// 1. GENERIC EXPONENTIAL HISTOGRAM ENGINE
// ==============================================================================

// Resettable allows sketches to be cleared and reused via sync.Pool.
// Sketches like CocoSketch implement this. KLL/UnivMon should implement this for GC benefits.
type Resettable interface {
	Clear()
}

// Bucket represents a single storage unit within the histogram.
// We use a struct value (not pointer) for better cache locality in the slice.
type Bucket struct {
	Sketch  common.Sketch
	MaxTime int64
	MinTime int64
	Count   int64 // Weight (powers of 2)
}

// BaseEH is the generic engine for Exponential Histograms.
type BaseEH struct {
	buckets    []Bucket // Struct slice for contiguous memory
	k          int      // Precision parameter
	windowSize int64    // Sliding window duration

	// Factory function to create new empty sketches
	sketchFactory func() (common.Sketch, error)

	// Pool for recycling sketches to reduce GC pressure
	pool *sync.Pool

	mu sync.RWMutex
}

// initBase initializes the engine configuration.
func (eh *BaseEH) initBase(k int, windowSize int64, factory func() (common.Sketch, error)) {
	eh.k = k
	eh.windowSize = windowSize
	eh.sketchFactory = factory
	eh.buckets = make([]Bucket, 0, 32) // Pre-allocate some space

	// Initialize sync.Pool
	eh.pool = &sync.Pool{
		New: func() interface{} {
			s, err := factory()
			if err != nil {
				return nil
			}
			return s
		},
	}
}

// getSketch retrieves a sketch from the pool or creates a new one.
func (eh *BaseEH) getSketch() (common.Sketch, error) {
	if item := eh.pool.Get(); item != nil {
		return item.(common.Sketch), nil
	}
	// Fallback if pool fails (factory error handled in New)
	return eh.sketchFactory()
}

// putSketch resets and returns a sketch to the pool.
func (eh *BaseEH) putSketch(s common.Sketch) {
	// Only pool if the sketch can be cleared to a clean state.
	if r, ok := s.(Resettable); ok {
		r.Clear()
		eh.pool.Put(s)
	}
	// If not resettable, let GC handle it.
}

// Update inserts a new sketch into the histogram.
func (eh *BaseEH) Update(newSketch common.Sketch, timestamp int64) error {
	eh.mu.Lock()
	defer eh.mu.Unlock()

	// 1. Remove expired buckets
	eh.expireBuckets(timestamp)

	// 2. Create a new bucket (Size = 1)
	newBucket := Bucket{
		Sketch:  newSketch,
		MaxTime: timestamp,
		MinTime: timestamp,
		Count:   1,
	}

	// Append to the end (Newest)
	eh.buckets = append(eh.buckets, newBucket)

	// 3. O(N) Compression
	return eh.compress()
}

// expireBuckets removes buckets older than the window.
func (eh *BaseEH) expireBuckets(now int64) {
	threshold := now - eh.windowSize
	cutIdx := 0
	for i := range eh.buckets {
		// buckets[i] is a value type, access fields directly
		if eh.buckets[i].MaxTime < threshold {
			// Recycle the sketch before dropping the bucket
			eh.putSketch(eh.buckets[i].Sketch)
			cutIdx = i + 1
		} else {
			break
		}
	}

	if cutIdx > 0 {
		// Shift remaining buckets to front (copy for struct slice)
		// This avoids memory leaks in pointer slices, but here it's just data movement.
		copy(eh.buckets, eh.buckets[cutIdx:])
		// Truncate length
		eh.buckets = eh.buckets[:len(eh.buckets)-cutIdx]
	}
}

// compress uses O(N) backward scanning to merge buckets.
// It merges the oldest two buckets of the same size when the limit is exceeded.
func (eh *BaseEH) compress() error {
	// Standard EH Limit: k/2 + 1 buckets of the same size.
	limit := eh.k/2 + 1

	// Scan backwards from newest to oldest
	idx := len(eh.buckets) - 1

	for idx > 0 {
		currentSize := eh.buckets[idx].Count

		// Find the start of the block of 'currentSize' (scanning left)
		startIdx := idx
		for startIdx > 0 && eh.buckets[startIdx-1].Count == currentSize {
			startIdx--
		}

		// Count of buckets with 'currentSize'
		count := idx - startIdx + 1

		if count > limit {
			// Merge the two OLDEST buckets of this size (startIdx and startIdx+1).
			// We merge buckets[startIdx] INTO buckets[startIdx+1] to reuse the newer sketch.
			// buckets is sorted Old -> New.

			// Note: Accessing slice directly modifies the struct if we use pointers,
			// but here we have a slice of structs. We need to be careful with updates.

			// Target: buckets[startIdx+1] (Newer)
			// Source: buckets[startIdx]   (Older)

			err := eh.buckets[startIdx+1].Sketch.Merge(eh.buckets[startIdx].Sketch)
			if err != nil {
				return err
			}

			// Update metadata of the survivor (Newer)
			eh.buckets[startIdx+1].Count += eh.buckets[startIdx].Count
			eh.buckets[startIdx+1].MinTime = eh.buckets[startIdx].MinTime // Extend range backwards

			// Recycle the consumed sketch
			eh.putSketch(eh.buckets[startIdx].Sketch)

			// Remove buckets[startIdx] from the slice
			// Shift everything left by 1 starting from startIdx
			copy(eh.buckets[startIdx:], eh.buckets[startIdx+1:])
			eh.buckets = eh.buckets[:len(eh.buckets)-1]

			// The merged bucket is now at startIdx. It has double the size.
			// We must re-evaluate the block at startIdx because it might now cascade.
			// Reset idx to startIdx (clamped) to check the new size group.
			idx = startIdx
			if idx >= len(eh.buckets) {
				idx = len(eh.buckets) - 1
			}
			continue
		}

		// Move to the next block (older buckets)
		idx = startIdx - 1
	}
	return nil
}

// QueryInterval returns a merged Sketch covering [t1, t2] with symmetric boundary adjustment.
func (eh *BaseEH) QueryInterval(t1, t2 int64) (common.Sketch, error) {
	eh.mu.RLock()
	defer eh.mu.RUnlock()

	// 1. Find overlapping range [startIdx, endIdx]
	startIdx := -1
	endIdx := -1

	for i := range eh.buckets {
		b := &eh.buckets[i]
		// Overlap condition: Bucket [Min, Max] intersects [t1, t2]
		// i.e., Max >= t1 AND Min <= t2
		if b.MaxTime >= t1 && b.MinTime <= t2 {
			if startIdx == -1 {
				startIdx = i
			}
			endIdx = i
		}
	}

	if startIdx == -1 {
		return nil, errors.New("no data found in interval")
	}

	// 2. Symmetric Boundary Adjustment Heuristic
	// "If (t - Min) > (bucket_size / 2)" logic

	// A. Adjust Start (t1)
	// If t1 cuts off more than half of the first bucket, skip it.
	firstB := &eh.buckets[startIdx]
	bucketSizeStart := firstB.MaxTime - firstB.MinTime
	if (t1 - firstB.MinTime) > (bucketSizeStart / 2) {
		startIdx++
	}

	// Check if range became invalid
	if startIdx > endIdx {
		return nil, errors.New("no data found after start boundary adjustment")
	}

	// B. Adjust End (t2)
	// If t2 includes less than half of the last bucket, ignore it.
	// We want the part [Min, t2]. If (t2 - Min) < Size/2, it's too small.
	lastB := &eh.buckets[endIdx]
	bucketSizeEnd := lastB.MaxTime - lastB.MinTime
	if (t2 - lastB.MinTime) < (bucketSizeEnd / 2) {
		endIdx--
	}

	if startIdx > endIdx {
		return nil, errors.New("no data found after end boundary adjustment")
	}

	// 3. Create Result Accumulator from Pool
	res, err := eh.getSketch()
	if err != nil {
		return nil, err
	}

	// 4. Merge applicable buckets
	// Note: If 'res' comes from pool, it might be the same type but we need to ensure
	// it's empty. putSketch calls Clear(), so getSketch returns clean sketches (if Resettable).
	// If not resettable, getSketch calls factory, so it's fresh.

	count := 0
	for i := startIdx; i <= endIdx; i++ {
		if err := res.Merge(eh.buckets[i].Sketch); err != nil {
			// On error, try to return sketch to pool (optional, simplified here)
			return nil, err
		}
		count++
	}

	return res, nil
}

// ==============================================================================
// 2. IMPLEMENTATION WRAPPERS
// ==============================================================================

// --- A. ExpoHistogramKLL (Quantiles) ---

type ExpoHistogramKLL struct {
	BaseEH
	kllK int
}

func NewExpoHistogramKLL(ehK int, windowSize int64, kllK int) *ExpoHistogramKLL {
	eh := &ExpoHistogramKLL{kllK: kllK}
	factory := func() (common.Sketch, error) {
		return kll.NewKLLSketch(kllK)
	}
	eh.initBase(ehK, windowSize, factory)
	return eh
}

func (eh *ExpoHistogramKLL) UpdateValue(val float64, timestamp int64) error {
	// We could optimize this by pooling the single-item sketches too,
	// but usually Update is dominated by compression, not the single insert.
	s, _ := kll.NewKLLSketch(eh.kllK)
	s.Insert(val)
	return eh.Update(s, timestamp)
}

// --- B. ExpoHistogramUniv (Heavy Hitters, Entropy) ---

type ExpoHistogramUniv struct {
	BaseEH
	univK, univRow, univCol, univLayer int
}

func NewExpoHistogramUniv(ehK int, windowSize int64, k, row, col, layer int) *ExpoHistogramUniv {
	eh := &ExpoHistogramUniv{
		univK: k, univRow: row, univCol: col, univLayer: layer,
	}
	factory := func() (common.Sketch, error) {
		return univmon.NewUnivSketchPyramid(k, row, col, layer)
	}
	eh.initBase(ehK, windowSize, factory)
	return eh
}

func (eh *ExpoHistogramUniv) UpdateItem(key string, timestamp int64) error {
	s, _ := univmon.NewUnivSketchPyramid(eh.univK, eh.univRow, eh.univCol, eh.univLayer)
	s.Update(common.FromString(key), 1)
	return eh.Update(s, timestamp)
}

// --- C. ExpoHistogramCountSketch (Frequency) ---

type ExpoHistogramCountSketch struct {
	BaseEH
	rows int
	cols int
}

func NewExpoHistogramCountSketch(ehK int, windowSize int64, rows, cols int) *ExpoHistogramCountSketch {
	eh := &ExpoHistogramCountSketch{rows: rows, cols: cols}
	factory := func() (common.Sketch, error) { return countsketch.NewCountSketch(rows, cols) }
	eh.initBase(ehK, windowSize, factory)
	return eh
}

func (eh *ExpoHistogramCountSketch) UpdateItem(key string, timestamp int64) error {
	s, _ := countsketch.NewCountSketch(eh.rows, eh.cols)
	s.UpdateString(key, 1)
	return eh.Update(s, timestamp)
}

// --- D. ExpoHistogramHLL (Cardinality) ---

type ExpoHistogramHLL struct {
	BaseEH
}

func NewExpoHistogramHLL(ehK int, windowSize int64) *ExpoHistogramHLL {
	eh := &ExpoHistogramHLL{}
	factory := func() (common.Sketch, error) {
		return hll.NewHyperLogLog(), nil
	}
	eh.initBase(ehK, windowSize, factory)
	return eh
}

func (eh *ExpoHistogramHLL) UpdateItem(key string, timestamp int64) error {
	s := hll.NewHyperLogLog()
	s.InsertWithHash(common.FromString(key).Hash)
	return eh.Update(s, timestamp)
}

// --- E. ExpoHistogramCount (Exact Counting) ---

type SimpleCounter struct {
	Val int64
}

func (s *SimpleCounter) Merge(other common.Sketch) error {
	o, ok := other.(*SimpleCounter)
	if !ok {
		return errors.New("type mismatch")
	}
	s.Val += o.Val
	return nil
}
func (s *SimpleCounter) InsertWithHash(h uint64) {}
func (s *SimpleCounter) QueryWithHash(q common.QueryType, h uint64) (float64, error) {
	return float64(s.Val), nil
}
func (s *SimpleCounter) TypeName() string { return "simple_counter" }
func (s *SimpleCounter) Clear()           { s.Val = 0 } // Implement Resettable

type ExpoHistogramCount struct {
	BaseEH
}

func NewExpoHistogramCount(ehK int, windowSize int64) *ExpoHistogramCount {
	eh := &ExpoHistogramCount{}
	factory := func() (common.Sketch, error) { return &SimpleCounter{Val: 0}, nil }
	eh.initBase(ehK, windowSize, factory)
	return eh
}

func (eh *ExpoHistogramCount) UpdateCount(count int64, timestamp int64) error {
	s := &SimpleCounter{Val: count}
	return eh.Update(s, timestamp)
}

func (eh *ExpoHistogramCount) GetTotalCount(t1, t2 int64) (int64, error) {
	resSketch, err := eh.QueryInterval(t1, t2)
	if err != nil {
		return 0, err
	}
	counter := resSketch.(*SimpleCounter)
	return counter.Val, nil
}
