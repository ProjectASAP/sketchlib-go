package exponentialhistogram

import (
	"errors"
	"math"
	"sync"

	"github.com/ProjectASAP/sketchlib-go/common"

	// Import specific sketch implementations
	univmon "github.com/ProjectASAP/sketchlib-go/sketch_framework/UnivMon"
	cocosketch "github.com/ProjectASAP/sketchlib-go/sketches/CocoSketch"
	countmin "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
	ddsketch "github.com/ProjectASAP/sketchlib-go/sketches/DDSketch"
	hll "github.com/ProjectASAP/sketchlib-go/sketches/HLL"
	kll "github.com/ProjectASAP/sketchlib-go/sketches/KLL"
)

// ==============================================================================
// 1. GENERIC EXPONENTIAL HISTOGRAM ENGINE
// ==============================================================================

// Resettable allows sketches to be cleared and reused via sync.Pool.
// Only implement this if the sketch has a working Clear/Reset method.
type Resettable interface {
	Clear()
}

// L2Provider interface for sketches that natively support L2 norm retrieval.
type L2Provider interface {
	GetL2() float64
}

// Bucket represents a single storage unit within the histogram.
type Bucket struct {
	Sketch  common.Sketch
	MaxTime int64
	MinTime int64
	Size    int64   // Represents the number of merges/items
	L2Mass  float64 // L2 Squared (Energy)
}

// SketchNorm defines the merge behavior.
type SketchNorm int

const (
	NormL1 SketchNorm = iota
	NormL2
)

// BaseEH is the generic engine for Exponential Histograms.
type BaseEH struct {
	buckets    []Bucket
	k          int
	windowSize int64
	norm       SketchNorm

	// Function to calculate L2 for a sketch (only needed for NormL2).
	l2Calc func(common.Sketch) float64

	sketchFactory func() (common.Sketch, error)
	pool          *sync.Pool
	mu            sync.RWMutex
}

// initBase initializes the engine.
func (eh *BaseEH) initBase(k int, windowSize int64, norm SketchNorm, factory func() (common.Sketch, error), l2Calc func(common.Sketch) float64) {
	if k < 1 {
		k = 1
	}
	eh.k = k
	eh.windowSize = windowSize
	eh.norm = norm
	eh.l2Calc = l2Calc
	eh.sketchFactory = factory
	eh.buckets = make([]Bucket, 0, 32)

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

func (eh *BaseEH) getSketch() (common.Sketch, error) {
	if item := eh.pool.Get(); item != nil {
		return item.(common.Sketch), nil
	}
	return eh.sketchFactory()
}

func (eh *BaseEH) putSketch(s common.Sketch) {
	// Only pool if the sketch can be explicitly cleared.
	if r, ok := s.(Resettable); ok {
		r.Clear()
		eh.pool.Put(s)
	}
}

// computeL2Mass calculates the metric for NormL2 sketches.
func (eh *BaseEH) computeL2Mass(s common.Sketch) float64 {
	val := 0.0
	if eh.l2Calc != nil {
		val = eh.l2Calc(s)
	} else if p, ok := s.(L2Provider); ok {
		val = p.GetL2()
	} else {
		val = 1.0
	}
	return val * val
}

// Update inserts a new sketch into the histogram.
func (eh *BaseEH) Update(newSketch common.Sketch, timestamp int64) error {
	eh.mu.Lock()
	defer eh.mu.Unlock()

	eh.expireBuckets(timestamp)

	l2Mass := 0.0
	if eh.norm == NormL2 {
		l2Mass = eh.computeL2Mass(newSketch)
	}

	newBucket := Bucket{
		Sketch:  newSketch,
		MaxTime: timestamp,
		MinTime: timestamp,
		Size:    1,
		L2Mass:  l2Mass,
	}

	eh.buckets = append(eh.buckets, newBucket)

	if eh.norm == NormL1 {
		return eh.compressCountInvariant()
	}
	return eh.compressL2Invariant()
}

func (eh *BaseEH) expireBuckets(now int64) {
	threshold := now - eh.windowSize
	cutIdx := 0
	for i := range eh.buckets {
		if eh.buckets[i].MaxTime < threshold {
			eh.putSketch(eh.buckets[i].Sketch)
			cutIdx = i + 1
		} else {
			break
		}
	}
	if cutIdx > 0 {
		copy(eh.buckets, eh.buckets[cutIdx:])
		eh.buckets = eh.buckets[:len(eh.buckets)-cutIdx]
	}
}

// compressCountInvariant implements "k/2 + 2" buffer logic for L1 sketches
func (eh *BaseEH) compressCountInvariant() error {
	limit := float64(eh.k)/2.0 + 2.0
	idx := len(eh.buckets) - 1

	for idx > 0 {
		currentSize := eh.buckets[idx].Size
		startIdx := idx
		for startIdx > 0 && eh.buckets[startIdx-1].Size == currentSize {
			startIdx--
		}
		count := float64(idx - startIdx + 1)

		if count >= limit {
			// Merge the two OLDEST buckets of this size block
			targetIdx := startIdx
			sourceIdx := startIdx + 1

			bTarget := &eh.buckets[targetIdx]
			bSource := &eh.buckets[sourceIdx]

			err := bTarget.Sketch.Merge(bSource.Sketch)
			if err != nil {
				return err
			}

			bTarget.Size += bSource.Size
			bTarget.L2Mass += bSource.L2Mass

			if bSource.MaxTime > bTarget.MaxTime {
				bTarget.MaxTime = bSource.MaxTime
			}
			if bSource.MinTime < bTarget.MinTime {
				bTarget.MinTime = bSource.MinTime
			}

			eh.putSketch(bSource.Sketch)
			copy(eh.buckets[sourceIdx:], eh.buckets[sourceIdx+1:])
			eh.buckets = eh.buckets[:len(eh.buckets)-1]

			idx = startIdx
			if idx >= len(eh.buckets) {
				idx = len(eh.buckets) - 1
			}
			continue
		}
		idx = startIdx - 1
	}
	return nil
}

// compressL2Invariant implements error threshold logic for L2 sketches
func (eh *BaseEH) compressL2Invariant() error {
	sumL2Newer := 0.0
	thresholdFactor := 1.0 / float64(eh.k)
	epsilon := 1e-9

	for {
		mergeIdx := -1
		sumL2Newer = 0.0

		for i := len(eh.buckets) - 2; i >= 0; i-- {
			bOlder := &eh.buckets[i]
			bNewer := &eh.buckets[i+1]

			pairL2 := bOlder.L2Mass + bNewer.L2Mass
			threshold := sumL2Newer * thresholdFactor

			if pairL2 <= threshold+epsilon {
				mergeIdx = i
				break
			}
			sumL2Newer += bNewer.L2Mass
		}

		if mergeIdx == -1 {
			break
		}

		targetIdx := mergeIdx
		sourceIdx := mergeIdx + 1

		bTarget := &eh.buckets[targetIdx]
		bSource := &eh.buckets[sourceIdx]

		err := bTarget.Sketch.Merge(bSource.Sketch)
		if err != nil {
			return err
		}

		bTarget.Size += bSource.Size
		bTarget.MaxTime = bSource.MaxTime
		bTarget.L2Mass = eh.computeL2Mass(bTarget.Sketch)

		eh.putSketch(bSource.Sketch)
		copy(eh.buckets[sourceIdx:], eh.buckets[sourceIdx+1:])
		eh.buckets = eh.buckets[:len(eh.buckets)-1]
	}
	return nil
}

func (eh *BaseEH) QueryInterval(t1, t2 int64) (common.Sketch, error) {
	eh.mu.RLock()
	defer eh.mu.RUnlock()

	if len(eh.buckets) == 0 {
		return nil, errors.New("no data found")
	}

	fromVolume := 0
	toVolume := 0
	foundStart := false

	for i, b := range eh.buckets {
		if t1 >= b.MinTime && t1 <= b.MaxTime {
			fromVolume = i
			foundStart = true
		}
		if t2 >= b.MinTime && t2 <= b.MaxTime {
			toVolume = i
		}
	}

	lastIdx := len(eh.buckets) - 1
	if t2 > eh.buckets[lastIdx].MaxTime {
		toVolume = lastIdx
	}
	if t1 < eh.buckets[0].MinTime {
		fromVolume = 0
		foundStart = true
	}

	if foundStart {
		b := &eh.buckets[fromVolume]
		diffMin := math.Abs(float64(t1 - b.MinTime))
		diffMax := math.Abs(float64(t1 - b.MaxTime))

		if diffMin > diffMax && fromVolume+1 < len(eh.buckets) {
			fromVolume++
		}
	}

	if toVolume >= len(eh.buckets) {
		toVolume = len(eh.buckets) - 1
	}

	if fromVolume > toVolume {
		if fromVolume < len(eh.buckets) {
			res, err := eh.getSketch()
			if err != nil {
				return nil, err
			}
			res.Merge(eh.buckets[fromVolume].Sketch)
			return res, nil
		}
		return nil, errors.New("invalid interval")
	}

	res, err := eh.getSketch()
	if err != nil {
		return nil, err
	}

	if err := res.Merge(eh.buckets[fromVolume].Sketch); err != nil {
		return nil, err
	}

	for i := fromVolume + 1; i <= toVolume; i++ {
		if err := res.Merge(eh.buckets[i].Sketch); err != nil {
			return nil, err
		}
	}

	return res, nil
}

// ==============================================================================
// 2. HYBRID SKETCH (Map -> UnivMon)
// ==============================================================================

const DefaultMaxMapSize = 512

type HybridSketch struct {
	isSketch   bool
	mapCounts  map[uint64]uint64
	l2         float64
	univSketch *univmon.UnivSketch

	k, row, col, layer int
	maxMapSize         int
}

func NewHybridSketch(k, row, col, layer, maxMapSize int) *HybridSketch {
	return &HybridSketch{
		isSketch:   false,
		mapCounts:  make(map[uint64]uint64),
		l2:         0.0,
		k:          k,
		row:        row,
		col:        col,
		layer:      layer,
		maxMapSize: maxMapSize,
	}
}

func (h *HybridSketch) InsertWithHash(hash uint64) {
	if h.isSketch {
		h.univSketch.InsertWithHash(hash)
		return
	}

	v := h.mapCounts[hash]
	h.mapCounts[hash]++
	h.l2 += 2*float64(v) + 1

	if 2*len(h.mapCounts) >= h.maxMapSize {
		h.promoteToSketch()
	}
}

func (h *HybridSketch) promoteToSketch() {
	if h.isSketch {
		return
	}
	sk, _ := univmon.NewUnivSketchPyramid(h.k, h.row, h.col, h.layer)
	for hash, count := range h.mapCounts {
		for i := uint64(0); i < count; i++ {
			sk.InsertWithHash(hash)
		}
	}
	h.univSketch = sk
	h.mapCounts = nil
	h.isSketch = true
}

func (h *HybridSketch) GetL2() float64 {
	if h.isSketch {
		l2, _ := h.univSketch.QueryWithHash(common.QuerySum2, 0)
		return l2
	}
	return math.Sqrt(h.l2)
}

func (h *HybridSketch) GetL2Sq() float64 {
	if h.isSketch {
		l2, _ := h.univSketch.QueryWithHash(common.QuerySum2, 0)
		return l2 * l2
	}
	return h.l2
}

func (h *HybridSketch) Merge(other common.Sketch) error {
	o, ok := other.(*HybridSketch)
	if !ok {
		return errors.New("type mismatch")
	}

	if h.isSketch && o.isSketch {
		return h.univSketch.Merge(o.univSketch)
	}
	if h.isSketch && !o.isSketch {
		o.promoteToSketch()
		return h.univSketch.Merge(o.univSketch)
	}
	if !h.isSketch && o.isSketch {
		h.promoteToSketch()
		return h.univSketch.Merge(o.univSketch)
	}

	for hash, countB := range o.mapCounts {
		countA := h.mapCounts[hash]
		oldSq := float64(countA * countA)
		newSq := float64((countA + countB) * (countA + countB))
		h.l2 = h.l2 - oldSq + newSq
		h.mapCounts[hash] = countA + countB
	}

	if 2*len(h.mapCounts) >= h.maxMapSize {
		h.promoteToSketch()
	}
	return nil
}

func (h *HybridSketch) TypeName() string { return "HybridSketch" }
func (h *HybridSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if h.isSketch {
		return h.univSketch.QueryWithHash(q, hash)
	}
	switch q {
	case common.QueryFrequency:
		return float64(h.mapCounts[hash]), nil
	case common.QuerySum2:
		return math.Sqrt(h.l2), nil
	default:
		return 0, nil
	}
}
func (h *HybridSketch) Clear() {
	h.isSketch = false
	h.mapCounts = make(map[uint64]uint64)
	h.l2 = 0
	h.univSketch = nil
}

// ==============================================================================
// 3. IMPLEMENTATION WRAPPERS
// ==============================================================================

// --- A. ExpoHistogramKLL (L1) ---

type KLLAdapter struct {
	*kll.KLLSketch
}

func (k *KLLAdapter) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if q == common.QueryQuantile {
		qt := math.Float64frombits(hash)
		// [FIX] Use CDF().Query() to retrieve the Value at Quantile (Inverse CDF)
		// kll.Quantile(qt) returns the Rank of value 'qt', which is not what we want here.
		return k.KLLSketch.CDF().Query(qt), nil
	}
	return 0, common.ErrUnsupportedQuery
}

func (k *KLLAdapter) Merge(other common.Sketch) error {
	o, ok := other.(*KLLAdapter)
	if !ok {
		return errors.New("cannot merge different sketch types")
	}
	k.KLLSketch.Merge(o.KLLSketch)
	return nil
}

func (k *KLLAdapter) InsertWithHash(hash uint64) {}

// NOTE: KLLAdapter does NOT implement Clear(), disabling pooling.

type ExpoHistogramKLL struct {
	BaseEH
	kllK int
}

func NewExpoHistogramKLL(ehK int, windowSize int64, kllK int) *ExpoHistogramKLL {
	eh := &ExpoHistogramKLL{kllK: kllK}
	factory := func() (common.Sketch, error) {
		sk, err := kll.NewKLLSketch(kllK)
		if err != nil {
			return nil, err
		}
		return &KLLAdapter{KLLSketch: sk}, nil
	}
	eh.initBase(ehK, windowSize, NormL1, factory, nil)
	return eh
}

func (eh *ExpoHistogramKLL) UpdateValue(val float64, timestamp int64) error {
	s, _ := kll.NewKLLSketch(eh.kllK)
	s.Insert(val)
	return eh.Update(&KLLAdapter{KLLSketch: s}, timestamp)
}

// --- B. ExpoHistogramUniv (L2 / Hybrid) ---
type ExpoHistogramUniv struct {
	BaseEH
	univK, univRow, univCol, univLayer int
	maxMapSize                         int
}

func NewExpoHistogramUniv(ehK int, windowSize int64, k, row, col, layer int) *ExpoHistogramUniv {
	eh := &ExpoHistogramUniv{
		univK:      k,
		univRow:    row,
		univCol:    col,
		univLayer:  layer,
		maxMapSize: DefaultMaxMapSize,
	}
	factory := func() (common.Sketch, error) {
		return NewHybridSketch(k, row, col, layer, eh.maxMapSize), nil
	}
	l2Calc := func(s common.Sketch) float64 {
		if h, ok := s.(*HybridSketch); ok {
			return h.GetL2()
		}
		return 1.0
	}
	eh.initBase(ehK, windowSize, NormL2, factory, l2Calc)
	return eh
}

func (eh *ExpoHistogramUniv) UpdateItem(key string, timestamp int64) error {
	s := NewHybridSketch(eh.univK, eh.univRow, eh.univCol, eh.univLayer, eh.maxMapSize)
	s.InsertWithHash(common.FromString(key).Hash)
	return eh.Update(s, timestamp)
}

// --- C. ExpoHistogramCountSketch (L1) ---
type ExpoHistogramCountSketch struct {
	BaseEH
	rows int
	cols int
}

func NewExpoHistogramCountSketch(ehK int, windowSize int64, rows, cols int) *ExpoHistogramCountSketch {
	eh := &ExpoHistogramCountSketch{rows: rows, cols: cols}
	factory := func() (common.Sketch, error) { return countsketch.NewCountSketch(rows, cols) }
	eh.initBase(ehK, windowSize, NormL1, factory, nil)
	return eh
}

func (eh *ExpoHistogramCountSketch) UpdateItem(key string, timestamp int64) error {
	s, _ := countsketch.NewCountSketch(eh.rows, eh.cols)
	s.UpdateString(key, 1)
	return eh.Update(s, timestamp)
}

// --- D. ExpoHistogramCountMin (L1) ---
type ExpoHistogramCountMin struct {
	BaseEH
	rows int
	cols int
}

func NewExpoHistogramCountMin(ehK int, windowSize int64, rows, cols int) *ExpoHistogramCountMin {
	eh := &ExpoHistogramCountMin{rows: rows, cols: cols}
	factory := func() (common.Sketch, error) {
		return countmin.NewCountMinSketch(rows, cols)
	}
	eh.initBase(ehK, windowSize, NormL1, factory, nil)
	return eh
}

func (eh *ExpoHistogramCountMin) UpdateItem(key string, timestamp int64) error {
	s, _ := countmin.NewCountMinSketch(eh.rows, eh.cols)
	s.InsertWithHash(common.FromString(key).Hash)
	return eh.Update(s, timestamp)
}

// --- E. ExpoHistogramHLL (L1) ---
type ExpoHistogramHLL struct {
	BaseEH
}

func NewExpoHistogramHLL(ehK int, windowSize int64) *ExpoHistogramHLL {
	eh := &ExpoHistogramHLL{}
	factory := func() (common.Sketch, error) {
		return hll.NewHyperLogLog(), nil
	}
	eh.initBase(ehK, windowSize, NormL1, factory, nil)
	return eh
}

func (eh *ExpoHistogramHLL) UpdateItem(key string, timestamp int64) error {
	s := hll.NewHyperLogLog()
	s.InsertWithHash(common.FromString(key).Hash)
	return eh.Update(s, timestamp)
}

// --- F. ExpoHistogramDDS (L1) ---
type DDAdapter struct {
	*ddsketch.DDSketch
}

func (d *DDAdapter) Merge(other common.Sketch) error {
	o, ok := other.(*DDAdapter)
	if !ok {
		return errors.New("cannot merge different sketch types")
	}
	return d.DDSketch.Merge(o.DDSketch)
}
func (d *DDAdapter) InsertWithHash(hash uint64) { /* No-op */ }

// Supports QueryQuantile via bit-casting
func (d *DDAdapter) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	if q == common.QueryQuantile {
		qt := math.Float64frombits(hash)
		val, ok := d.DDSketch.GetValueAtQuantile(qt)
		if !ok {
			return 0, errors.New("ddsketch empty or invalid quantile")
		}
		return val, nil
	}
	return 0, errors.New("unsupported")
}

func (d *DDAdapter) TypeName() string { return "DDSketch" }

// NOTE: Clear() REMOVED to disable pooling.

type ExpoHistogramDDS struct {
	BaseEH
	alpha float64
}

func NewExpoHistogramDDS(ehK int, windowSize int64, alpha float64) *ExpoHistogramDDS {
	eh := &ExpoHistogramDDS{alpha: alpha}
	factory := func() (common.Sketch, error) {
		return &DDAdapter{DDSketch: ddsketch.NewDDSketch(alpha)}, nil
	}
	eh.initBase(ehK, windowSize, NormL1, factory, nil)
	return eh
}

func (eh *ExpoHistogramDDS) UpdateValue(val float64, timestamp int64) error {
	adapter := &DDAdapter{DDSketch: ddsketch.NewDDSketch(eh.alpha)}
	adapter.Add(val)
	return eh.Update(adapter, timestamp)
}

// --- G. ExpoHistogramCoco (L1) ---
type ExpoHistogramCoco struct {
	BaseEH
	d      int
	length int
}

func NewExpoHistogramCoco(ehK int, windowSize int64, d, length int) *ExpoHistogramCoco {
	eh := &ExpoHistogramCoco{d: d, length: length}
	factory := func() (common.Sketch, error) {
		return cocosketch.NewCocoSketch(d, length)
	}
	eh.initBase(ehK, windowSize, NormL1, factory, nil)
	return eh
}

func (eh *ExpoHistogramCoco) UpdateItem(key string, timestamp int64) error {
	s, _ := cocosketch.NewCocoSketch(eh.d, eh.length)
	h := common.FromString(key).Hash
	s.InsertWithHash(h)
	return eh.Update(s, timestamp)
}

// --- H. ExpoHistogramCount (L1) ---
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
func (s *SimpleCounter) Clear()           { s.Val = 0 }

type ExpoHistogramCount struct {
	BaseEH
}

func NewExpoHistogramCount(ehK int, windowSize int64) *ExpoHistogramCount {
	eh := &ExpoHistogramCount{}
	factory := func() (common.Sketch, error) { return &SimpleCounter{Val: 0}, nil }
	eh.initBase(ehK, windowSize, NormL1, factory, nil)
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
