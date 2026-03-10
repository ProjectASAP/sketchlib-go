package hydrasketch

import (
	"encoding/binary"
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

	Grid [][]*univmon.UnivSketch // [D][W]
	Big  *univmon.UnivSketch     // Global UnivMon (optional)

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
	}

	// Initialize Grid D x W
	h.Grid = make([][]*univmon.UnivSketch, h.D)
	for i := 0; i < h.D; i++ {
		h.Grid[i] = make([]*univmon.UnivSketch, h.W)
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
			h.Grid[i][j] = um
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
		h.Big = b
	}

	return h, nil
}

// Update updates the sketch with a key (+1 count)
func (h *Hydra) Update(key string) {
	h.UpdateN(key, 1)
}

// UpdateN updates the sketch with a key and a specific delta
func (h *Hydra) UpdateN(key string, delta int64) {
	if delta <= 0 {
		return
	}

	// 1. Determine grid position
	pos := h.hashCM(key)

	// 2. Prepare input using common.FromString.
	// This uses common.Hash64 internally, ensuring consistency.
	input := common.FromString(key)

	h.mu.Lock()
	defer h.mu.Unlock()

	// 3. Update every row in the Grid
	for r := 0; r < h.D; r++ {
		h.Grid[r][pos[r]].Update(input, delta)
	}

	// 4. Update Big UnivMon if it exists
	if h.Big != nil {
		h.Big.Update(input, delta)
	}
}

// Estimate returns the estimated frequency of the key.
func (h *Hydra) Estimate(key string) int64 {
	// 1. Use the exact same hash as UpdateN via common.FromString
	inputHash := common.FromString(key).Hash

	pos := h.hashCM(key)
	vals := make([]int64, h.D)

	h.mu.Lock()
	defer h.mu.Unlock()

	for r := 0; r < h.D; r++ {
		// Query with the pre-calculated hash
		val, err := h.Grid[r][pos[r]].QueryWithHash(common.QueryFrequency, inputHash)
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
	var b1 [8]byte
	var b2 [8]byte
	binary.LittleEndian.PutUint64(b1[:], h.seedCM1)
	binary.LittleEndian.PutUint64(b2[:], h.seedCM2)

	keyBytes := []byte(key)

	// FIX: Use common.Hash64 instead of direct xxhash dependency
	x := common.Hash64(append(b1[:], keyBytes...))
	y := common.Hash64(append(b2[:], keyBytes...))

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
	D       int
	W       int
	SeedCM1 uint64
	SeedCM2 uint64
	Grid    [][][]byte
	Big     []byte
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
		D:       h.D,
		W:       h.W,
		SeedCM1: h.seedCM1,
		SeedCM2: h.seedCM2,
		Grid:    grid,
		Big:     bigBytes,
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

	h := &Hydra{
		D:       snap.D,
		W:       snap.W,
		seedCM1: snap.SeedCM1,
		seedCM2: snap.SeedCM2,
		Grid:    make([][]*univmon.UnivSketch, snap.D),
	}

	for i := 0; i < snap.D; i++ {
		if len(snap.Grid[i]) != snap.W {
			return nil, errors.New("invalid snapshot grid width")
		}
		h.Grid[i] = make([]*univmon.UnivSketch, snap.W)
		for j := 0; j < snap.W; j++ {
			um, err := univmon.DeserializeUnivSketchFromBytes(snap.Grid[i][j])
			if err != nil {
				return nil, err
			}
			h.Grid[i][j] = um
		}
	}

	if len(snap.Big) > 0 {
		um, err := univmon.DeserializeUnivSketchFromBytes(snap.Big)
		if err != nil {
			return nil, err
		}
		h.Big = um
	}

	return h, nil
}
