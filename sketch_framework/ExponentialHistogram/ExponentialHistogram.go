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

// Bucket represents a single storage unit within the histogram.
// It contains a merged sketch covering a specific time range.
type Bucket struct {
	Sketch  common.Sketch
	MaxTime int64
	MinTime int64
	Count   int64 // Represents the weight (number of base items) in this bucket (powers of 2)
}

// BaseEH is the generic engine for Exponential Histograms.
// It manages the sliding window lifecycle, bucket expiration, and merging logic.
type BaseEH struct {
	buckets    []*Bucket
	k          int   // Precision parameter (controls number of buckets per size level)
	windowSize int64 // Duration of the sliding window in milliseconds/nanoseconds

	// Factory function to create new empty sketches during merges
	sketchFactory func() (common.Sketch, error)

	mu sync.RWMutex
}

// initBase initializes the engine configuration.
func (eh *BaseEH) initBase(k int, windowSize int64, factory func() (common.Sketch, error)) {
	eh.k = k
	eh.windowSize = windowSize
	eh.sketchFactory = factory
	eh.buckets = make([]*Bucket, 0)
}

// Update inserts a new sketch (representing 1 recent item/batch) into the histogram.
func (eh *BaseEH) Update(newSketch common.Sketch, timestamp int64) error {
	eh.mu.Lock()
	defer eh.mu.Unlock()

	// 1. Remove expired buckets
	eh.expireBuckets(timestamp)

	// 2. Create a new bucket for this item (Size = 1)
	newBucket := &Bucket{
		Sketch:  newSketch,
		MaxTime: timestamp,
		MinTime: timestamp,
		Count:   1, // Base unit
	}

	// Append to the end (newest)
	eh.buckets = append(eh.buckets, newBucket)

	// 3. Compress / Merge buckets if they violate the exponential rule
	return eh.compress()
}

// expireBuckets removes buckets that have fallen out of the sliding window.
func (eh *BaseEH) expireBuckets(now int64) {
	threshold := now - eh.windowSize

	// Buckets[0] is the oldest. Find the split point.
	cutIdx := 0
	for i, b := range eh.buckets {
		// If the bucket's newest data is older than the threshold, it expires.
		if b.MaxTime < threshold {
			cutIdx = i + 1
		} else {
			break
		}
	}

	if cutIdx > 0 {
		// Slice off the expired buckets
		// Setting pointers to nil helps GC (optional but good practice)
		for i := 0; i < cutIdx; i++ {
			eh.buckets[i] = nil
		}
		eh.buckets = eh.buckets[cutIdx:]
	}
}

// compress performs the standard EH merging logic (Datar et al.).
func (eh *BaseEH) compress() error {
	for {
		changed := false

		// Map size -> list of indices
		sizeMap := make(map[int64][]int)
		for i, b := range eh.buckets {
			sizeMap[b.Count] = append(sizeMap[b.Count], i)
		}

		limit := eh.k/2 + 2

		for _, indices := range sizeMap {
			if len(indices) > limit {
				// Merge the two OLDEST buckets of this size.
				idx1 := indices[0]
				idx2 := indices[1]

				b1 := eh.buckets[idx1]
				b2 := eh.buckets[idx2]

				mergedSketch, err := eh.sketchFactory()
				if err != nil {
					return err
				}

				// Merge sketches
				if err := mergedSketch.Merge(b1.Sketch); err != nil {
					return err
				}
				if err := mergedSketch.Merge(b2.Sketch); err != nil {
					return err
				}

				// Create merged bucket
				newBucket := &Bucket{
					Sketch:  mergedSketch,
					MaxTime: b2.MaxTime, // Newest time of the pair
					MinTime: b1.MinTime, // Oldest time of the pair
					Count:   b1.Count + b2.Count,
				}

				// Replace the two old buckets with the new one.
				prefix := eh.buckets[:idx1]
				suffix := eh.buckets[idx2+1:]

				eh.buckets = append(prefix, append([]*Bucket{newBucket}, suffix...)...)

				changed = true
				break // Restart loop as indices shifted
			}
		}

		if !changed {
			break
		}
	}
	return nil
}

// QueryInterval returns a merged Sketch covering the requested time range [t1, t2].
func (eh *BaseEH) QueryInterval(t1, t2 int64) (common.Sketch, error) {
	eh.mu.RLock()
	defer eh.mu.RUnlock()

	// 1. Create a result sketch
	res, err := eh.sketchFactory()
	if err != nil {
		return nil, err
	}

	found := false
	for _, b := range eh.buckets {
		// Check for overlap between bucket time range and query interval
		// Bucket: [MinTime, MaxTime], Query: [t1, t2]
		if b.MaxTime >= t1 && b.MinTime <= t2 {
			err := res.Merge(b.Sketch)
			if err != nil {
				return nil, err
			}
			found = true
		}
	}

	if !found {
		return nil, errors.New("no data found in the specified interval")
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

// UpdateItem fixes the "UpdateString undefined" error by using generic Update.
func (eh *ExpoHistogramUniv) UpdateItem(key string, timestamp int64) error {
	s, _ := univmon.NewUnivSketchPyramid(eh.univK, eh.univRow, eh.univCol, eh.univLayer)

	// FIX: Gunakan common.FromString() + s.Update()
	// karena univmon.go Anda saat ini tidak memiliki method UpdateString.
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

// CountSketch menggunakan UpdateString (untuk TopK support)
func (eh *ExpoHistogramCountSketch) UpdateItem(key string, timestamp int64) error {
	s, _ := countsketch.NewCountSketch(eh.rows, eh.cols)
	s.UpdateString(key, 1) // Ini memanggil InsertWithHash + Update TopK
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
	// HLL Update logic sesuai file hll.go Anda
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
