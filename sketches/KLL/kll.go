package kll

import (
	"errors"
	"math"
	"sort"
	"time"
	"unsafe"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

const (
	capacityCacheLen = 20
	maxCacheableK    = 26602
	capacityDecay    = 2.0 / 3.0
	defaultK         = 200
	defaultM         = 8
)

type KLLSketch struct {
	items      []float64
	levels     []int
	itemStore  *storage.Vector1D[float64]
	levelStore *storage.Vector1D[int]
	k          int
	m          int
	numLevels  int
	co         coin
	// seed is the explicit RNG seed (when seedSet is true). Stored so
	// Clear() / Reset() can recreate a deterministic coin instead of
	// silently re-randomizing from time.Now() and breaking the
	// "same seed + same input = same bytes" contract over a sketch's
	// lifetime. Zero-valued (seedSet=false) means the time-based
	// constructor was used and Reset() should keep behaving like
	// before (re-seed from wall clock).
	seed          int64
	seedSet       bool
	capacityCache [capacityCacheLen]uint32
	topHeight     int
	level0Cap     int
}

type KLL = KLLSketch

func (s *KLLSketch) bindStoresFromSlices() {
	s.itemStore = storage.Vector1DFromVec(s.items)
	s.levelStore = storage.Vector1DFromVec(s.levels)
}

type coin struct {
	state         uint64
	bitCache      uint64
	remainingBits uint8
}

func newCoin() coin {
	seed := uint64(time.Now().UnixNano())
	return coinFromSeed(seed)
}

func coinFromSeed(seed uint64) coin {
	return coin{state: normalizeSeed(seed)}
}

func normalizeSeed(seed uint64) uint64 {
	if seed == 0 {
		return 0x9e3779b97f4a7c15
	}
	return seed
}

func xorshiftMult64(x uint64) uint64 {
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	return x * 2685821657736338717
}

func (c *coin) refill() {
	c.state = normalizeSeed(xorshiftMult64(c.state))
	c.bitCache = c.state
	c.remainingBits = 64
}

func (c *coin) toss() bool {
	if c.remainingBits == 0 {
		c.refill()
	}
	bit := (c.bitCache & 1) != 0
	c.bitCache >>= 1
	c.remainingBits--
	return bit
}

// NewKLLSketch creates a new KLL sketch using the Rust default minimum buffer.
// The compaction RNG is seeded from the wall clock (time.Now().UnixNano()) — for
// reproducible sketch state across runs use NewKLLSketchWithSeed.
func NewKLLSketch(k int) (*KLLSketch, error) {
	if k <= 0 {
		return nil, errors.New("k must be positive")
	}
	return Init(k, defaultM), nil
}

// NewKLLSketchWithSeed creates a new KLL sketch with an explicit seed for the
// compaction-RNG ("coin"). Two sketches built with the same seed and fed the
// same sequence of values produce byte-identical SerializePortable output.
//
// This is the deterministic-construction path: prefer it for tests, parity
// harnesses, and any production caller that needs reproducible sketch state
// across processor restarts. Callers that don't care about determinism can
// keep using NewKLLSketch (time-seeded).
func NewKLLSketchWithSeed(k int, seed int64) (*KLLSketch, error) {
	if k <= 0 {
		return nil, errors.New("k must be positive")
	}
	return InitWithSeed(k, defaultM, seed), nil
}

// New returns a sketch using the Rust default K.
func New() *KLLSketch {
	return InitKLL(defaultK)
}

// Init mirrors the Rust constructor naming.
func Init(k, m int) *KLLSketch {
	return initInternal(k, m, newCoin(), 0, false)
}

// InitWithSeed mirrors Init but uses an explicit seed for the compaction RNG.
// See NewKLLSketchWithSeed for the determinism contract.
func InitWithSeed(k, m int, seed int64) *KLLSketch {
	return initInternal(k, m, coinFromSeed(uint64(seed)), seed, true)
}

// initInternal is the shared constructor that injects a pre-built coin so the
// seedable and time-seeded paths share the same allocation / capacity-cache
// rebuild logic. The coin's seed propagates through to every toss() (used in
// compact()) — i.e. the new seed actually drives compaction outcomes, not just
// the struct field.
func initInternal(k, m int, co coin, seed int64, seedSet bool) *KLLSketch {
	normM := m
	if normM < 2 {
		normM = 2
	}
	if normM > maxCacheableK {
		normM = maxCacheableK
	}
	normK := k
	if normK < normM {
		normK = normM
	}
	if normK > maxCacheableK {
		normK = maxCacheableK
	}

	s := &KLLSketch{
		items:     make([]float64, 0, normK*3),
		levels:    []int{0, 0},
		k:         normK,
		m:         normM,
		numLevels: 1,
		co:        co,
		seed:      seed,
		seedSet:   seedSet,
	}
	s.bindStoresFromSlices()
	s.rebuildCapacityCache()
	return s
}

// InitKLL mirrors the Rust helper naming.
func InitKLL(k int) *KLLSketch {
	return Init(k, defaultM)
}

// InitKLLWithSeed mirrors InitKLL but with an explicit RNG seed.
func InitKLLWithSeed(k int, seed int64) *KLLSketch {
	return InitWithSeed(k, defaultM, seed)
}

func (s *KLLSketch) TypeName() string {
	return "kll"
}

func (s *KLLSketch) InsertWithHash(hash uint64) {
	s.Update(float64(hash))
}

func (s *KLLSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	switch q {
	case common.QueryQuantile:
		return s.Quantile(math.Float64frombits(hash)), nil
	case common.QueryFrequency:
		return float64(s.Count()), nil
	default:
		return 0, common.ErrUnsupportedQuery
	}
}

func (s *KLLSketch) Merge(other common.Sketch) error {
	o, ok := other.(*KLLSketch)
	if !ok {
		return errors.New("cannot merge: incompatible sketch type")
	}
	s.mergePacked(o)
	return nil
}

// Update adds a value to the sketch. Mirrors Rust's `update` (sketches/kll.rs).
func (s *KLLSketch) Update(x float64) {
	s.pushValue(x)
}

// Reset clears the sketch, returning it to its empty state.
func (s *KLLSketch) Reset() {
	s.Clear()
}

// Clear resets the sketch while preserving allocation. If the sketch was
// constructed with an explicit seed (NewKLLSketchWithSeed / InitWithSeed),
// Clear re-seeds the coin from that seed so determinism is preserved across
// Reset/Clear cycles. Otherwise the coin is re-seeded from time.Now() (the
// pre-determinism behavior).
func (s *KLLSketch) Clear() {
	s.items = s.items[:0]
	s.levels = []int{0, 0}
	s.bindStoresFromSlices()
	s.numLevels = 1
	if s.seedSet {
		s.co = coinFromSeed(uint64(s.seed))
	} else {
		s.co = newCoin()
	}
	s.rebuildCapacityCache()
}

// GetSize returns the total number of inserted items.
func (s *KLLSketch) GetSize() int {
	return s.Count()
}

// GetRetainedItems returns the number of items physically stored in memory.
func (s *KLLSketch) GetRetainedItems() int {
	return len(s.items)
}

// GetMemoryBytes returns the approximate memory usage in bytes.
func (s *KLLSketch) GetMemoryBytes() float64 {
	return float64(unsafe.Sizeof(*s)) +
		float64(len(s.items))*8 +
		float64(len(s.levels))*float64(unsafe.Sizeof(int(0)))
}

// Rank estimates the number of inserted values less than or equal to x.
func (s *KLLSketch) Rank(x float64) int {
	rank := 0
	for h := 0; h < s.numLevels; h++ {
		weight := 1 << h
		for _, value := range s.levelValues(h) {
			if value <= x {
				rank += weight
			}
		}
	}
	return rank
}

// Count returns the total number of inserted items represented by the sketch.
func (s *KLLSketch) Count() int {
	total := 0
	for h := 0; h < s.numLevels; h++ {
		total += s.levelSize(h) * (1 << h)
	}
	return total
}

// Quantile returns the estimated value at quantile q.
func (s *KLLSketch) Quantile(q float64) float64 {
	return s.CDF().Query(q)
}

// CDF builds the sketch CDF representation.
func (s *KLLSketch) CDF() CDF {
	cdf := make(CDF, 0, len(s.items))
	totalWeight := 0

	for h := 0; h < s.numLevels; h++ {
		weightInt := 1 << h
		weight := float64(weightInt)
		values := s.levelValues(h)
		for _, value := range values {
			cdf = append(cdf, Quantile{Q: weight, V: value})
		}
		totalWeight += len(values) * weightInt
	}
	if totalWeight == 0 {
		return cdf
	}

	sort.Sort(cdf)
	acc := 0.0
	for i := range cdf {
		acc += cdf[i].Q
		cdf[i].Q = acc / float64(totalWeight)
	}
	return cdf
}

func (s *KLLSketch) pushValue(value float64) {
	s.items = append(s.items, value)
	s.levels[s.numLevels] = len(s.items)
	s.bindStoresFromSlices()
	if len(s.items)-s.levels[s.numLevels-1] > s.level0Cap {
		s.compressWhileNeeded()
	}
}

func (s *KLLSketch) compressWhileNeeded() {
	for h := 0; ; h++ {
		if h >= s.numLevels {
			break
		}
		if s.levelSize(h) <= s.capacityForLevel(h) {
			break
		}
		if h == s.numLevels-1 {
			s.addNewTopLevel()
			h--
			continue
		}
		s.compact(h)
	}
}

func (s *KLLSketch) capacityForLevel(level int) int {
	heightFromTop := s.topHeight - level
	if heightFromTop < 0 {
		heightFromTop = 0
	}
	if heightFromTop >= capacityCacheLen {
		heightFromTop = capacityCacheLen - 1
	}
	return int(s.capacityCache[heightFromTop])
}

func (s *KLLSketch) rebuildCapacityCache() {
	s.topHeight = s.numLevels - 1
	scale := 1.0
	for i := 0; i < capacityCacheLen; i++ {
		scaled := int(math.Ceil(float64(s.k) * scale))
		if scaled < s.m {
			scaled = s.m
		}
		s.capacityCache[i] = uint32(scaled)
		scale *= capacityDecay
	}
	s.level0Cap = s.capacityForLevel(0)
}

func (s *KLLSketch) addNewTopLevel() {
	s.levels = append(s.levels, 0)
	copy(s.levels[1:], s.levels[:len(s.levels)-1])
	s.levels[0] = 0
	s.levels[len(s.levels)-1] = len(s.items)
	s.bindStoresFromSlices()
	s.numLevels++
	s.rebuildCapacityCache()
}

func (s *KLLSketch) compact(level int) {
	idx := s.numLevels - 1 - level
	start := s.levels[idx]
	end := s.levels[idx+1]
	count := end - start
	if count <= 1 {
		return
	}

	sort.Float64s(s.items[start:end])

	offset := 0
	if s.co.toss() {
		offset = 1
	}

	survivors := 0
	for i := offset; i < count; i += 2 {
		s.items[start+survivors] = s.items[start+i]
		survivors++
	}

	garbage := count - survivors
	promotedStart := start + survivors
	copy(s.items[promotedStart:], s.items[end:])
	s.items = s.items[:len(s.items)-garbage]

	s.levels[idx] = promotedStart
	for i := idx + 1; i < len(s.levels); i++ {
		s.levels[i] -= garbage
	}
	s.levels[len(s.levels)-1] = len(s.items)
	s.bindStoresFromSlices()
}

func (s *KLLSketch) levelSize(level int) int {
	idx := s.numLevels - 1 - level
	return s.levels[idx+1] - s.levels[idx]
}

func (s *KLLSketch) levelValues(level int) []float64 {
	if level < 0 || level >= s.numLevels {
		return nil
	}
	idx := s.numLevels - 1 - level
	return s.items[s.levels[idx]:s.levels[idx+1]]
}

func (s *KLLSketch) mergePacked(other *KLLSketch) {
	maxLevels := s.numLevels
	if other.numLevels > maxLevels {
		maxLevels = other.numLevels
	}

	levelData := make([][]float64, maxLevels)
	totalItems := 0
	for level := 0; level < maxLevels; level++ {
		selfLevel := s.levelValues(level)
		otherLevel := other.levelValues(level)
		if len(selfLevel)+len(otherLevel) == 0 {
			continue
		}
		merged := make([]float64, 0, len(selfLevel)+len(otherLevel))
		merged = append(merged, selfLevel...)
		merged = append(merged, otherLevel...)
		levelData[level] = merged
		totalItems += len(merged)
	}

	s.items = make([]float64, 0, totalItems)
	s.levels = make([]int, maxLevels+1)
	s.numLevels = maxLevels

	pos := 0
	for level := maxLevels - 1; level >= 0; level-- {
		idx := maxLevels - 1 - level
		s.levels[idx] = pos
		s.items = append(s.items, levelData[level]...)
		pos = len(s.items)
	}
	s.levels[maxLevels] = pos
	s.bindStoresFromSlices()
	s.rebuildCapacityCache()
	s.compressWhileNeeded()
}

type Quantile struct {
	Q float64
	V float64
}

type CDF []Quantile

func (q CDF) Len() int           { return len(q) }
func (q CDF) Less(i, j int) bool { return q[i].V < q[j].V }
func (q CDF) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }

// Quantile returns the quantile rank for value x.
func (q CDF) Quantile(x float64) float64 {
	if len(q) == 0 {
		return 0
	}
	idx := sort.Search(len(q), func(i int) bool { return q[i].V >= x })
	if idx == len(q) {
		return 1
	}
	if idx == 0 {
		return 0
	}
	return q[idx-1].Q
}

// Query returns the estimated value at quantile p.
func (q CDF) Query(p float64) float64 {
	if len(q) == 0 {
		return 0
	}
	idx := sort.Search(len(q), func(i int) bool { return q[i].Q >= p })
	if idx == len(q) {
		return q[len(q)-1].V
	}
	return q[idx].V
}

func (q CDF) QuantileLI(x float64) float64 {
	if len(q) == 0 {
		return 0
	}
	idx := sort.Search(len(q), func(i int) bool { return q[i].V >= x })
	if idx == len(q) {
		return 1
	}
	if idx == 0 {
		return 0
	}
	a, aq := q[idx-1].V, q[idx-1].Q
	b, bq := q[idx].V, q[idx].Q
	return ((a-x)*bq + (x-b)*aq) / (a - b)
}

func (q CDF) QueryLI(p float64) float64 {
	if len(q) == 0 {
		return 0
	}
	idx := sort.Search(len(q), func(i int) bool { return q[i].Q >= p })
	if idx == len(q) {
		return q[len(q)-1].V
	}
	if idx == 0 {
		return q[0].V
	}
	a, aq := q[idx-1].V, q[idx-1].Q
	b, bq := q[idx].V, q[idx].Q
	return ((aq-p)*b + (p-bq)*a) / (aq - bq)
}

type kllSnapshot struct {
	K             int
	M             int
	Items         []float64
	Levels        []int
	CoinState     uint64
	CoinBitCache  uint64
	CoinRemaining uint8
}

func (s *KLLSketch) SerializeToBytes() ([]byte, error) {
	return common.EncodeToBytes(kllSnapshot{
		K:             s.k,
		M:             s.m,
		Items:         append([]float64(nil), s.items...),
		Levels:        append([]int(nil), s.levels...),
		CoinState:     s.co.state,
		CoinBitCache:  s.co.bitCache,
		CoinRemaining: s.co.remainingBits,
	})
}

func DeserializeKLLSketchFromBytes(data []byte) (*KLLSketch, error) {
	var snap kllSnapshot
	if err := common.DecodeFromBytes(data, &snap); err != nil {
		return nil, err
	}
	if snap.K <= 0 || snap.M <= 0 {
		return nil, errors.New("invalid snapshot parameters")
	}
	if len(snap.Levels) < 2 {
		return nil, errors.New("invalid snapshot levels")
	}
	if snap.Levels[len(snap.Levels)-1] != len(snap.Items) {
		return nil, errors.New("invalid snapshot item layout")
	}

	s := &KLLSketch{
		items:     append([]float64(nil), snap.Items...),
		levels:    append([]int(nil), snap.Levels...),
		k:         snap.K,
		m:         snap.M,
		numLevels: len(snap.Levels) - 1,
		co: coin{
			state:         normalizeSeed(snap.CoinState),
			bitCache:      snap.CoinBitCache,
			remainingBits: snap.CoinRemaining,
		},
	}
	s.bindStoresFromSlices()
	s.rebuildCapacityCache()
	return s, nil
}
