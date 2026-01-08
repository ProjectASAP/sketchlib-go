package hydrasketch

import (
	"container/heap"
	"encoding/binary"
	"errors"
	"math"
	"sync"

	"github.com/cespare/xxhash/v2"
)

/////////////////////////
// Pair (ImpPair.java) //
/////////////////////////

type Pair struct {
	Key   string
	Value int
}

//////////////////////////////
// Min-heap for Top-K CS    //
//////////////////////////////

type minItem struct {
	key   string
	count int
}
type minHeap []minItem

func (h minHeap) Len() int            { return len(h) }
func (h minHeap) Less(i, j int) bool  { return h[i].count < h[j].count }
func (h minHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x interface{}) { *h = append(*h, x.(minItem)) }
func (h *minHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

//////////////////////////////
// CountSketch (ImpCount...) //
//////////////////////////////

type CountSketch struct {
	row int
	col int
	k   int

	cs []int // d*w counters (row major)
	// hashing: single 128-bit (xxhash64(key) + xxhash64(seedPrefix|key)) -> split across d rows
	seed1 uint64
	seed2 uint64

	// top-K structure
	topK     minHeap
	topKOnce sync.Once
	mu       sync.Mutex
}

type CountSketchConfig struct {
	Rows int
	Cols int
	TopK int
	// optional seeds (0 = default)
	Seed1 uint64
	Seed2 uint64
}

func NewCountSketch(cfg CountSketchConfig) (*CountSketch, error) {
	if cfg.Rows <= 0 || cfg.Cols <= 0 {
		return nil, errors.New("invalid rows/cols")
	}
	cs := &CountSketch{
		row: cfg.Rows,
		col: cfg.Cols,
		k:   cfg.TopK,
		cs:  make([]int, cfg.Rows*cfg.Cols),
		seed1: func() uint64 {
			if cfg.Seed1 != 0 {
				return cfg.Seed1
			}
			return 0x9e3779b97f4a7c15
		}(),
		seed2: func() uint64 {
			if cfg.Seed2 != 0 {
				return cfg.Seed2
			}
			return 0x243f6a8885a308d3
		}(),
	}
	if cs.k > 0 {
		cs.topK = make([]minItem, 0, cs.k)
		heap.Init(&cs.topK)
	}
	return cs, nil
}

// Update +1 (or delta) for the given key
func (s *CountSketch) Update(key string, delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pos, sign := s.hashPositionsSigns(key)
	for r := 0; r < s.row; r++ {
		idx := r*s.col + pos[r]
		s.cs[idx] += sign[r] * delta
	}

	if s.k > 0 {
		est := s.estimateLocked(key)
		s.updateTopKLocked(key, est)
	}
}

// Estimate frequency of a key: median across d rows
func (s *CountSketch) Estimate(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.estimateLocked(key)
}

func (s *CountSketch) estimateLocked(key string) int {
	pos, sign := s.hashPositionsSigns(key)
	tmp := make([]int, s.row)
	for r := 0; r < s.row; r++ {
		v := s.cs[r*s.col+pos[r]]
		tmp[r] = sign[r] * v
	}
	// median
	quickSelect(tmp, s.row/2)
	med := tmp[s.row/2]
	if med <= 0 {
		// CountSketch can produce zero or negative estimates due to hash collisions and noise.
		// Returning 1 ensures that the estimated frequency is at least 1, which avoids reporting
		// impossible (negative) or misleading (zero) counts. This pattern is common in CountSketch
		// implementations to reflect the assumption that a queried key has appeared at least once.
		return 1
	}
	return med
}

func (s *CountSketch) updateTopKLocked(key string, est int) {
	// linear scan the heap to check existence (heap is small: k is usually small)
	for i := range s.topK {
		if s.topK[i].key == key {
			s.topK[i].count = est
			heap.Fix(&s.topK, i)
			return
		}
	}
	if s.topK.Len() < s.k {
		heap.Push(&s.topK, minItem{key: key, count: est})
		return
	}
	if s.k > 0 && s.topK[0].count < est {
		heap.Pop(&s.topK)
		heap.Push(&s.topK, minItem{key: key, count: est})
	}
}

func (s *CountSketch) TopK() []Pair {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Pair, s.topK.Len())
	for i, it := range s.topK {
		out[i] = Pair{Key: it.key, Value: it.count}
	}
	return out
}

func (s *CountSketch) hashPositionsSigns(key string) ([]int, []int) {
	// build 128-bit: H1(key|seed1) and H2(seed2|key) -> mix
	var buf1 [8]byte
	var buf2 [8]byte
	binary.LittleEndian.PutUint64(buf1[:], s.seed1)
	binary.LittleEndian.PutUint64(buf2[:], s.seed2)

	h1 := xxhash.Sum64(append(buf1[:], []byte(key)...))
	h2 := xxhash.Sum64(append(buf2[:], []byte(key)...))

	// derive d "independent" hashes from the two 64-bit values above
	pos := make([]int, s.row)
	sgn := make([]int, s.row)
	x := h1
	y := h2
	for r := 0; r < s.row; r++ {
		// xorshift-ish
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		y ^= y << 13
		y ^= y >> 7
		y ^= y << 17

		pos[r] = int((x ^ y) % uint64(s.col))
		sgn[r] = 1
		if ((x ^ (y << 1)) & 1) == 0 {
			sgn[r] = -1
		}
	}
	return pos, sgn
}

//////////////////////////////
// UnivMon (ImpUnivMon...)  //
//////////////////////////////

type UnivMon struct {
	Layers int
	CS     []*CountSketch

	// hashing for coin toss per layer
	seed uint64

	mu sync.Mutex
}

type UnivMonConfig struct {
	Layers int
	Rows   int
	Cols   int
	TopK   int
	Seed   uint64 // for coin toss
}

func NewUnivMon(cfg UnivMonConfig) (*UnivMon, error) {
	if cfg.Layers <= 0 {
		return nil, errors.New("invalid layers")
	}
	u := &UnivMon{
		Layers: cfg.Layers,
		seed: func() uint64 {
			if cfg.Seed != 0 {
				return cfg.Seed
			}
			return 0x51f0c911c0debabe
		}(),
	}
	u.CS = make([]*CountSketch, u.Layers)
	for i := 0; i < u.Layers; i++ {
		cs, err := NewCountSketch(CountSketchConfig{
			Rows:  cfg.Rows,
			Cols:  cfg.Cols,
			TopK:  cfg.TopK,
			Seed1: uint64(0x10001 + i*131),
			Seed2: uint64(0x20011 + i*131),
		})
		if err != nil {
			return nil, err
		}
		u.CS[i] = cs
	}
	return u, nil
}

// coin toss to determine the bottom layer (similar to findBottomLayerNum in Java)
func (u *UnivMon) bottomLayer(key string) int {
	// use the number of trailing zeros of the hash as an approximate level
	h := xxhash.Sum64(append(u.seedBytes(), []byte(key)...))
	// clamp to [0, Layers-1]
	tz := trailingZeros64(h)
	if tz >= uint(u.Layers) {
		return u.Layers - 1
	}
	return int(tz)
}

func (u *UnivMon) seedBytes() []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], u.seed)
	return b[:]
}

func trailingZeros64(x uint64) uint {
	if x == 0 {
		return 64
	}
	return uint(bitsTrailingZeros64(x))
}

// portable trailing zeros
func bitsTrailingZeros64(x uint64) int {
	n := 0
	for (x & 1) == 0 {
		n++
		x >>= 1
	}
	return n
}

// Update a single key (+1)
func (u *UnivMon) Update(key string) {
	u.UpdateN(key, 1)
}

func (u *UnivMon) UpdateN(key string, delta int) {
	if delta <= 0 {
		return
	}
	// determine the bottom layer based on subsampling
	b := u.bottomLayer(key)
	u.mu.Lock()
	defer u.mu.Unlock()
	for i := b; i >= 0; i-- { // propagate to every layer above it
		u.CS[i].Update(key, delta)
	}
}

// Estimate frequency via layer 0 (top CountSketch)
func (u *UnivMon) Estimate(key string) int {
	return u.CS[0].Estimate(key)
}

func (u *UnivMon) TopK() []Pair {
	return u.CS[0].TopK()
}

// Placeholder: compute G-sum (L1/L2/entropy/cardinality) -> requires an HH component per layer.
// The original Java implementation had a "heuristic" table; this provides a simple hook.
type GFunc string

const (
	GF_L1       GFunc = "L1"
	GF_L2       GFunc = "L2"
	GF_ENTROPY  GFunc = "entropy"
	GF_CARD     GFunc = "cardinality"
	GF_ALPHA_HH GFunc = "alpha_hh"
)

func (u *UnivMon) GSum(_ GFunc) float64 {
	// Simple: use the total estimate from layer 0 (naive).
	// For production: implement the UnivMon algorithm (paper) for each G.
	sum := 0.0
	// naive: sum(|counter|) / d (coarse), or keep the total inserts in the caller.
	h := u.CS[0]
	h.mu.Lock()
	defer h.mu.Unlock()
	d := float64(h.row)
	for r := 0; r < h.row; r++ {
		for c := 0; c < h.col; c++ {
			v := h.cs[r*h.col+c]
			sum += math.Abs(float64(v))
		}
	}
	return sum / d
}

//////////////////////////////
// Hydra (ImpHydraStruct)   //
//////////////////////////////

type Hydra struct {
	// d x w UnivMon grid
	D int
	W int

	Grid [][]*UnivMon // [D][W]
	Big  *UnivMon     // bigUM / global UM (optional)

	seedCM1 uint64
	seedCM2 uint64

	mu sync.Mutex
}

type HydraConfig struct {
	D        int // CM rows
	W        int // CM columns
	UM       UnivMonConfig
	UseBigUM bool
	SeedCM1  uint64
	SeedCM2  uint64
}

func NewHydra(cfg HydraConfig) (*Hydra, error) {
	if cfg.D <= 0 || cfg.W <= 0 {
		return nil, errors.New("invalid D/W")
	}
	h := &Hydra{
		D: cfg.D,
		W: cfg.W,
		seedCM1: func() uint64 {
			if cfg.SeedCM1 != 0 {
				return cfg.SeedCM1
			}
			return 0x1111111111111111
		}(),
		seedCM2: func() uint64 {
			if cfg.SeedCM2 != 0 {
				return cfg.SeedCM2
			}
			return 0x2222222222222222
		}(),
	}
	h.Grid = make([][]*UnivMon, h.D)
	for i := 0; i < h.D; i++ {
		h.Grid[i] = make([]*UnivMon, h.W)
		for j := 0; j < h.W; j++ {
			um, err := NewUnivMon(cfg.UM)
			if err != nil {
				return nil, err
			}
			h.Grid[i][j] = um
		}
	}
	if cfg.UseBigUM {
		b, err := NewUnivMon(cfg.UM)
		if err != nil {
			return nil, err
		}
		h.Big = b
	}
	return h, nil
}

func (h *Hydra) Update(key string) {
	h.UpdateN(key, 1)
}

func (h *Hydra) UpdateN(key string, delta int) {
	if delta <= 0 {
		return
	}
	pos := h.hashCM(key)
	h.mu.Lock()
	for r := 0; r < h.D; r++ {
		h.Grid[r][pos[r]].UpdateN(key, delta)
	}
	if h.Big != nil {
		h.Big.UpdateN(key, delta)
	}
	h.mu.Unlock()
}

// Estimate CS (layer 0) via median across rows (CM-style)
func (h *Hydra) Estimate(key string) int {
	pos := h.hashCM(key)
	vals := make([]int, h.D)
	for r := 0; r < h.D; r++ {
		vals[r] = h.Grid[r][pos[r]].Estimate(key)
	}
	quickSelect(vals, h.D/2)
	return vals[h.D/2]
}

func (h *Hydra) hashCM(key string) []int {
	// derive D hashes and then take modulo W
	var b1 [8]byte
	var b2 [8]byte
	binary.LittleEndian.PutUint64(b1[:], h.seedCM1)
	binary.LittleEndian.PutUint64(b2[:], h.seedCM2)
	x := xxhash.Sum64(append(b1[:], []byte(key)...))
	y := xxhash.Sum64(append(b2[:], []byte(key)...))
	out := make([]int, h.D)
	for r := 0; r < h.D; r++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		y ^= y << 13
		y ^= y >> 7
		y ^= y << 17
		out[r] = int((x ^ (y << 1)) % uint64(h.W))
	}
	return out
}

/////////////////////////////////////
// Parallel update (ImpParallel)   //
/////////////////////////////////////

type UpdateJob struct {
	Key   string
	Count int
}

func ParallelUpdate(h *Hydra, jobs []UpdateJob, workers int) {
	if workers <= 0 {
		workers = 1
	}
	wg := sync.WaitGroup{}
	ch := make(chan UpdateJob, 1024)

	// workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				h.UpdateN(j.Key, j.Count)
			}
		}()
	}

	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()
}

///////////////////////////
// util: quickselect     //
///////////////////////////

func quickSelect(a []int, k int) {
	l, r := 0, len(a)-1
	for l < r {
		p := partition(a, l, r)
		if p == k {
			return
		} else if p < k {
			l = p + 1
		} else {
			r = p - 1
		}
	}
}

func partition(a []int, l, r int) int {
	p := a[r]
	i := l
	for j := l; j < r; j++ {
		if a[j] < p {
			a[i], a[j] = a[j], a[i]
			i++
		}
	}
	a[i], a[r] = a[r], a[i]
	return i
}
