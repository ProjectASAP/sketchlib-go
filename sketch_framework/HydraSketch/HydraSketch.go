package hydrasketch

import (
	"errors"
	"sync"

	"github.com/ProjectASAP/sketchlib-go/common"
	univmon "github.com/ProjectASAP/sketchlib-go/sketch_framework/UnivMon"
)

// Pair represents a Key-Value pair for TopK results.
type Pair struct {
	Key   string
	Value int64
}

// Hydra combines the power of a Grid structure (like Count-Min)
// with UnivMon estimators in each cell.
type Hydra struct {
	D int
	W int

	gridFlat   []*univmon.UnivSketch   // flattened D*W backing storage
	Grid       [][]*univmon.UnivSketch // compatibility 2D view over gridFlat
	Big        *univmon.UnivSketch     // Global UnivMon (optional)
	enableTopK bool

	seedCM1 uint64
	seedCM2 uint64

	mu sync.Mutex
}

// HydraConfig holds the configuration for Hydra and the internal UnivMon
type HydraConfig struct {
	D int
	W int

	// Configuration for each UnivMon inside the cells
	UnivMonLayer int
	UnivMonRow   int
	UnivMonCol   int
	UnivMonTopK  int

	UseBigUM bool
	SeedCM1  uint64
	SeedCM2  uint64
}

// NewHydra creates a new Hydra instance using the UnivMon engine
func NewHydra(cfg HydraConfig) (*Hydra, error) {
	if cfg.D <= 0 || cfg.W <= 0 {
		return nil, errors.New("invalid D/W dimensions")
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
		gridFlat:   make([]*univmon.UnivSketch, cfg.D*cfg.W),
		Grid:       make([][]*univmon.UnivSketch, cfg.D),
		enableTopK: true,
	}

	// Initialize 2D view over flattened D x W storage
	for i := 0; i < h.D; i++ {
		start := i * h.W
		h.Grid[i] = h.gridFlat[start : start+h.W]
		for j := 0; j < h.W; j++ {
			um, err := univmon.NewUnivSketchPyramid(
				cfg.UnivMonTopK,
				cfg.UnivMonRow,
				cfg.UnivMonCol,
				cfg.UnivMonLayer,
			)
			if err != nil {
				return nil, err
			}
			um.SetTopKEnabled(h.enableTopK)
			h.gridFlat[start+j] = um
		}
	}

	// Initialize Big UnivMon (Global) if requested
	if cfg.UseBigUM {
		b, err := univmon.NewUnivSketchPyramid(
			cfg.UnivMonTopK*2,
			cfg.UnivMonRow,
			cfg.UnivMonCol,
			cfg.UnivMonLayer,
		)
		if err != nil {
			return nil, err
		}
		b.SetTopKEnabled(h.enableTopK)
		h.Big = b
	}

	return h, nil
}

func (h *Hydra) gridAt(row, col int) *univmon.UnivSketch {
	return h.gridFlat[row*h.W+col]
}

func (h *Hydra) SetTopKEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enableTopK = enabled
	for i := range h.gridFlat {
		if h.gridFlat[i] != nil {
			h.gridFlat[i].SetTopKEnabled(enabled)
		}
	}
	if h.Big != nil {
		h.Big.SetTopKEnabled(enabled)
	}
}

// Update updates the sketch with a key (+1 count)
func (h *Hydra) Update(key string) {
	h.UpdateN(key, 1)
}

// UpdateN updates the sketch with a key and a specific delta
func (h *Hydra) UpdateN(key string, delta int64) {
	h.UpdateWithInput(common.FromString(key), delta)
}

// UpdateWithInput updates the sketch with prebuilt input (hash+bytes).
func (h *Hydra) UpdateWithInput(input *common.SketchInput, delta int64) {
	if input == nil || delta <= 0 {
		return
	}
	var posStack [16]int
	pos := posStack[:0]
	if h.D <= len(posStack) {
		pos = posStack[:h.D]
	} else {
		pos = make([]int, h.D)
	}
	h.fillPositionsFromHash(input.Hash, pos)

	h.mu.Lock()
	defer h.mu.Unlock()
	useTopK := h.enableTopK && len(input.Bytes) > 0
	for r := 0; r < h.D; r++ {
		cell := h.gridAt(r, pos[r])
		if useTopK {
			cell.Update(input, delta)
		} else {
			cell.UpdateWithHashOnly(input.Hash, delta)
		}
	}
	if h.Big != nil {
		if useTopK {
			h.Big.Update(input, delta)
		} else {
			h.Big.UpdateWithHashOnly(input.Hash, delta)
		}
	}
}

// UpdateWithHash updates the sketch using pre-hashed input (throughput fast path).
func (h *Hydra) UpdateWithHash(hash uint64, delta int64) {
	if delta <= 0 {
		return
	}
	var posStack [16]int
	pos := posStack[:0]
	if h.D <= len(posStack) {
		pos = posStack[:h.D]
	} else {
		pos = make([]int, h.D)
	}
	h.fillPositionsFromHash(hash, pos)

	h.mu.Lock()
	defer h.mu.Unlock()

	// Hash-only path skips key-aware TopK updates.
	for r := 0; r < h.D; r++ {
		h.gridAt(r, pos[r]).UpdateWithHashOnly(hash, delta)
	}

	if h.Big != nil {
		h.Big.UpdateWithHashOnly(hash, delta)
	}
}

// Estimate returns the estimated frequency of the key.
func (h *Hydra) Estimate(key string) int64 {
	return h.EstimateWithHash(common.FromString(key).Hash)
}

func (h *Hydra) EstimateWithHash(inputHash uint64) int64 {
	var posStack [16]int
	pos := posStack[:0]
	if h.D <= len(posStack) {
		pos = posStack[:h.D]
	} else {
		pos = make([]int, h.D)
	}
	h.fillPositionsFromHash(inputHash, pos)
	var valsStack [16]int64
	vals := valsStack[:0]
	if h.D <= len(valsStack) {
		vals = valsStack[:h.D]
	} else {
		vals = make([]int64, h.D)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for r := 0; r < h.D; r++ {
		// Query with the pre-calculated hash
		val, err := h.gridAt(r, pos[r]).QueryWithHash(common.QueryFrequency, inputHash)
		if err != nil {
			vals[r] = 0
		} else {
			vals[r] = int64(val)
		}
	}

	// Median
	quickSelect(vals, h.D/2)
	return vals[h.D/2]
}

// GetEntropy returns the estimated Shannon entropy.
func (h *Hydra) GetEntropy() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.Big != nil {
		return h.Big.GetEntropy()
	}
	return 0.0
}

// GetCardinality returns the estimated cardinality.
func (h *Hydra) GetCardinality() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.Big != nil {
		return h.Big.GetCardinality()
	}
	return 0.0
}

// TopK returns the Heavy Hitters elements.
func (h *Hydra) TopK(k int) []Pair {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.Big != nil {
		heapStruct := h.Big.QueryTopK(k)
		out := make([]Pair, 0, len(heapStruct.Heap))
		for _, item := range heapStruct.Heap {
			out = append(out, Pair{
				Key:   item.Key,
				Value: item.Count,
			})
		}
		return out
	}

	return nil
}

// hashCM determines the column position for each row in the Hydra Grid
func (h *Hydra) hashCM(key string) []int {
	out := make([]int, h.D)
	h.fillPositionsFromHash(common.FromString(key).Hash, out)
	return out
}

func (h *Hydra) fillPositionsFromHash(hash uint64, out []int) {
	x := hash ^ h.seedCM1
	y := hash ^ h.seedCM2
	if x == 0 {
		x = h.seedCM1
	}
	if y == 0 {
		y = h.seedCM2 | 1
	}
	for r := 0; r < h.D; r++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		y ^= y << 13
		y ^= y >> 7
		y ^= y << 17
		out[r] = int((x ^ (y << 1)) % uint64(h.W))
	}
}

/////////////////////////////////////
// Parallel update Utility         //
/////////////////////////////////////

type UpdateJob struct {
	Key   string
	Count int64
}

func ParallelUpdate(h *Hydra, jobs []UpdateJob, workers int) {
	if workers <= 0 {
		workers = 1
	}
	wg := sync.WaitGroup{}
	ch := make(chan UpdateJob, 1024)

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

func quickSelect(a []int64, k int) {
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

func partition(a []int64, l, r int) int {
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

type hydraSnapshot struct {
	Version    int
	D          int
	W          int
	SeedCM1    uint64
	SeedCM2    uint64
	EnableTopK bool
	Grid       [][][]byte
	Big        []byte
}

// SerializeToBytes serializes Hydra into bytes.
func (h *Hydra) SerializeToBytes() ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	grid := make([][][]byte, h.D)
	for i := 0; i < h.D; i++ {
		grid[i] = make([][]byte, h.W)
		for j := 0; j < h.W; j++ {
			if h.Grid[i][j] == nil {
				continue
			}
			b, err := h.Grid[i][j].SerializeToBytes()
			if err != nil {
				return nil, err
			}
			grid[i][j] = b
		}
	}

	var bigBytes []byte
	if h.Big != nil {
		b, err := h.Big.SerializeToBytes()
		if err != nil {
			return nil, err
		}
		bigBytes = b
	}

	return common.EncodeToBytes(hydraSnapshot{
		Version:    1,
		D:          h.D,
		W:          h.W,
		SeedCM1:    h.seedCM1,
		SeedCM2:    h.seedCM2,
		EnableTopK: h.enableTopK,
		Grid:       grid,
		Big:        bigBytes,
	})
}

// DeserializeHydraFromBytes restores Hydra from serialized bytes.
func DeserializeHydraFromBytes(data []byte) (*Hydra, error) {
	var snap hydraSnapshot
	if err := common.DecodeFromBytes(data, &snap); err != nil {
		return nil, err
	}
	if snap.D <= 0 || snap.W <= 0 {
		return nil, errors.New("invalid snapshot dimensions")
	}
	if len(snap.Grid) != snap.D {
		return nil, errors.New("invalid snapshot grid depth")
	}

	enableTopK := true
	if snap.Version >= 1 {
		enableTopK = snap.EnableTopK
	}

	h := &Hydra{
		D:          snap.D,
		W:          snap.W,
		seedCM1:    snap.SeedCM1,
		seedCM2:    snap.SeedCM2,
		gridFlat:   make([]*univmon.UnivSketch, snap.D*snap.W),
		Grid:       make([][]*univmon.UnivSketch, snap.D),
		enableTopK: enableTopK,
	}

	for i := 0; i < snap.D; i++ {
		if len(snap.Grid[i]) != snap.W {
			return nil, errors.New("invalid snapshot grid width")
		}
		start := i * h.W
		h.Grid[i] = h.gridFlat[start : start+h.W]
		for j := 0; j < snap.W; j++ {
			um, err := univmon.DeserializeUnivSketchFromBytes(snap.Grid[i][j])
			if err != nil {
				return nil, err
			}
			um.SetTopKEnabled(h.enableTopK)
			h.gridFlat[start+j] = um
		}
	}

	if len(snap.Big) > 0 {
		um, err := univmon.DeserializeUnivSketchFromBytes(snap.Big)
		if err != nil {
			return nil, err
		}
		um.SetTopKEnabled(h.enableTopK)
		h.Big = um
	}

	return h, nil
}
