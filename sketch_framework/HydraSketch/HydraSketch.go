package hydrasketch

import (
	"errors"
	"strings"
	"sync"

	"github.com/ProjectASAP/sketchlib-go/common"
	univmon "github.com/ProjectASAP/sketchlib-go/sketch_framework/UnivMon"
)

const (
	defaultHydraSeed = 6 // aligned with sketchlib-rust HYDRA_SEED
)

// Pair represents a Key-Value pair for TopK results.
type Pair struct {
	Key   string
	Value int64
}

type Hydra struct {
	D int
	W int

	cells       []HydraCounter
	typeToClone HydraCounter
	bigCounter  HydraCounter

	enableTopK    bool
	fanoutSubkeys bool
	seedHydra     int

	// Retained for backward compatibility with old snapshots.
	seedCM1 uint64
	seedCM2 uint64

	mu sync.Mutex
}

// HydraConfig holds Hydra settings.
type HydraConfig struct {
	D int
	W int

	// Rust-aligned generic counter config.
	CounterType HydraCounterType
	Counter     HydraCounter
	CounterRows int
	CounterCols int
	KLLK        int

	UniversalTopK  int
	UniversalRow   int
	UniversalCol   int
	UniversalLayer int

	EnableGlobalCounter bool
	FanoutSubkeys bool
	EnableTopK    bool
	SeedHydra     int
	SeedCM1       uint64
	SeedCM2       uint64
}

// NewHydra creates a Hydra instance.
func NewHydra(cfg HydraConfig) (*Hydra, error) {
	if cfg.D <= 0 || cfg.W <= 0 {
		return nil, errors.New("invalid D/W dimensions")
	}

	template, err := resolveCounterTemplate(cfg)
	if err != nil {
		return nil, err
	}

	h := &Hydra{
		D:             cfg.D,
		W:             cfg.W,
		typeToClone:   template,
		cells:         make([]HydraCounter, cfg.D*cfg.W),
		enableTopK:    true,
		fanoutSubkeys: true,
		seedHydra:     defaultHydraSeed,
		seedCM1:       0x1111111111111111,
		seedCM2:       0x2222222222222222,
	}
	if cfg.EnableTopK == false {
		h.enableTopK = false
	}
	if cfg.FanoutSubkeys == false {
		h.fanoutSubkeys = false
	}
	if cfg.SeedHydra != 0 {
		h.seedHydra = cfg.SeedHydra
	}
	if cfg.SeedCM1 != 0 {
		h.seedCM1 = cfg.SeedCM1
	}
	if cfg.SeedCM2 != 0 {
		h.seedCM2 = cfg.SeedCM2
	}

	for i := range h.cells {
		clone, err := template.Clone()
		if err != nil {
			return nil, err
		}
		clone.SetTopKEnabled(h.enableTopK)
		h.cells[i] = clone
	}

	if cfg.EnableGlobalCounter {
		if template.CounterType() == HydraCounterUniversal {
			topK := cfg.UniversalTopK
			if topK <= 0 {
				topK = 32
			}
			row := cfg.UniversalRow
			if row <= 0 {
				row = 3
			}
			col := cfg.UniversalCol
			if col <= 0 {
				col = 1024
			}
			layer := cfg.UniversalLayer
			if layer <= 0 {
				layer = 8
			}
			h.bigCounter, err = NewHydraUnivMonCounter(topK*2, row, col, layer)
			if err != nil {
				return nil, err
			}
		} else {
			h.bigCounter, err = template.Clone()
			if err != nil {
				return nil, err
			}
		}
		h.bigCounter.SetTopKEnabled(h.enableTopK)
	}
	return h, nil
}

// NewHydraWithDimensions mirrors sketchlib-rust Hydra::with_dimensions.
func NewHydraWithDimensions(rows, cols int, counter HydraCounter) (*Hydra, error) {
	return NewHydra(HydraConfig{
		D:          rows,
		W:          cols,
		Counter:    counter,
		EnableTopK: true,
		// Rust behavior: fan-out subsets is enabled in update(key,...).
		FanoutSubkeys: true,
	})
}

func resolveCounterTemplate(cfg HydraConfig) (HydraCounter, error) {
	if cfg.Counter != nil {
		return cfg.Counter, nil
	}

	counterType := cfg.CounterType
	if counterType == "" {
		counterType = HydraCounterCM
	}

	switch counterType {
	case HydraCounterCM:
		rows := cfg.CounterRows
		if rows <= 0 {
			rows = 3
		}
		cols := cfg.CounterCols
		if cols <= 0 {
			cols = 4096
		}
		return NewHydraCountMinCounter(rows, cols)
	case HydraCounterCS:
		rows := cfg.CounterRows
		if rows <= 0 {
			rows = 3
		}
		cols := cfg.CounterCols
		if cols <= 0 {
			cols = 4096
		}
		return NewHydraCountSketchCounter(rows, cols)
	case HydraCounterHLL:
		return NewHydraHLLCounter(), nil
	case HydraCounterKLL:
		k := cfg.KLLK
		if k <= 0 {
			k = 200
		}
		return NewHydraKLLCounter(k)
	case HydraCounterUniversal:
		topK := cfg.UniversalTopK
		if topK <= 0 {
			topK = 32
		}
		row := cfg.UniversalRow
		if row <= 0 {
			row = 3
		}
		col := cfg.UniversalCol
		if col <= 0 {
			col = 1024
		}
		layer := cfg.UniversalLayer
		if layer <= 0 {
			layer = 8
		}
		return NewHydraUnivMonCounter(topK, row, col, layer)
	default:
		return nil, errors.New("unsupported hydra counter type")
	}
}

func (h *Hydra) counterAt(row, col int) HydraCounter {
	return h.cells[row*h.W+col]
}

func (h *Hydra) SetTopKEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.enableTopK = enabled
	for i := range h.cells {
		if h.cells[i] != nil {
			h.cells[i].SetTopKEnabled(enabled)
		}
	}
	if h.bigCounter != nil {
		h.bigCounter.SetTopKEnabled(enabled)
	}
}

// UpdateValue mirrors sketchlib-rust: key determines subpopulation routing,
// value is inserted into the selected cell counters.
func (h *Hydra) UpdateValue(key string, value *common.SketchInput, delta int64) {
	if value == nil || delta <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	subkeys := h.expandSubkeys(key)
	for _, subkey := range subkeys {
		var posStack [16]int
		pos := posStack[:0]
		if h.D <= len(posStack) {
			pos = posStack[:h.D]
		} else {
			pos = make([]int, h.D)
		}
		h.fillPositionsFromSubKey(subkey, pos)

		for r := 0; r < h.D; r++ {
			h.counterAt(r, pos[r]).InsertWithHash(value, value.Hash, delta)
		}
	}

	if h.bigCounter != nil {
		h.bigCounter.InsertWithHash(value, value.Hash, delta)
	}
}

// UpdateWithInput updates using prebuilt input.
func (h *Hydra) UpdateWithInput(input *common.SketchInput, delta int64) {
	if input == nil || delta <= 0 {
		return
	}
	h.UpdateValue(string(input.Bytes), input, delta)
}

// UpdateWithHash updates using hash-only fast path.
func (h *Hydra) UpdateWithHash(hash uint64, delta int64) {
	if delta <= 0 {
		return
	}
	value := &common.SketchInput{Hash: hash}

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

	for r := 0; r < h.D; r++ {
		h.counterAt(r, pos[r]).InsertWithHash(value, hash, delta)
	}
	if h.bigCounter != nil {
		h.bigCounter.InsertWithHash(value, hash, delta)
	}
}

// QueryKey mirrors sketchlib-rust Hydra::query_key.
func (h *Hydra) QueryKey(key []string, query HydraQuery) float64 {
	joined := strings.Join(key, ";")

	var posStack [16]int
	pos := posStack[:0]
	if h.D <= len(posStack) {
		pos = posStack[:h.D]
	} else {
		pos = make([]int, h.D)
	}
	h.fillPositionsFromSubKey(joined, pos)

	values := make([]float64, h.D)

	h.mu.Lock()
	defer h.mu.Unlock()

	for r := 0; r < h.D; r++ {
		v, err := h.counterAt(r, pos[r]).Query(query)
		if err != nil {
			values[r] = 0
			continue
		}
		values[r] = v
	}
	return common.ComputeMedianInlineF64(values)
}

func (h *Hydra) QueryFrequency(key []string, value *common.SketchInput) float64 {
	return h.QueryKey(key, FrequencyQuery(value))
}

func (h *Hydra) QueryQuantile(key []string, threshold float64) float64 {
	return h.QueryKey(key, CDFQuery(threshold))
}

// GetEntropy returns the global entropy estimate when big sketch exists.
func (h *Hydra) GetEntropy() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.bigCounter == nil {
		return 0
	}
	v, err := h.bigCounter.Query(EntropyQuery())
	if err != nil {
		return 0
	}
	return v
}

// GetCardinality returns the global cardinality estimate when big sketch exists.
func (h *Hydra) GetCardinality() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.bigCounter == nil {
		return 0
	}
	v, err := h.bigCounter.Query(CardinalityQuery())
	if err != nil {
		return 0
	}
	return v
}

// TopK returns heavy hitters when global counter supports it.
func (h *Hydra) TopK(k int) []Pair {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.bigCounter == nil || !h.enableTopK {
		return nil
	}
	return h.bigCounter.TopK(k)
}

func (h *Hydra) expandSubkeys(key string) []string {
	if !h.fanoutSubkeys {
		return []string{key}
	}
	parts := strings.Split(key, ";")
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) <= 1 {
		return []string{key}
	}

	n := len(filtered)
	out := make([]string, 0, (1<<n)-1)
	var b strings.Builder
	for mask := 1; mask < (1 << n); mask++ {
		b.Reset()
		first := true
		for j := 0; j < n; j++ {
			if (mask>>j)&1 == 0 {
				continue
			}
			if !first {
				b.WriteByte(';')
			}
			b.WriteString(filtered[j])
			first = false
		}
		out = append(out, b.String())
	}
	return out
}

func (h *Hydra) fillPositionsFromSubKey(key string, out []int) {
	h.fillPositionsFromHash(common.HashIt(h.seedHydra, []byte(key)), out)
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
				h.UpdateValue(j.Key, common.FromString(j.Key), j.Count)
			}
		}()
	}

	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()
}

type hydraSnapshot struct {
	Version       int
	D             int
	W             int
	CounterType   HydraCounterType
	EnableTopK    bool
	FanoutSubkeys bool
	SeedHydra     int
	SeedCM1       uint64
	SeedCM2       uint64
	Cells         [][]byte
	Big           []byte

	// Legacy payload compatibility (v1)
	Grid [][][]byte
}

// SerializeToBytes serializes Hydra into bytes.
func (h *Hydra) SerializeToBytes() ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.cells) == 0 {
		return nil, errors.New("empty hydra")
	}

	cells := make([][]byte, len(h.cells))
	for i := range h.cells {
		b, err := h.cells[i].SerializeToBytes()
		if err != nil {
			return nil, err
		}
		cells[i] = b
	}

	var bigBytes []byte
	if h.bigCounter != nil {
		b, err := h.bigCounter.SerializeToBytes()
		if err != nil {
			return nil, err
		}
		bigBytes = b
	}

	return common.EncodeToBytes(hydraSnapshot{
		Version:       2,
		D:             h.D,
		W:             h.W,
		CounterType:   h.cells[0].CounterType(),
		EnableTopK:    h.enableTopK,
		FanoutSubkeys: h.fanoutSubkeys,
		SeedHydra:     h.seedHydra,
		SeedCM1:       h.seedCM1,
		SeedCM2:       h.seedCM2,
		Cells:         cells,
		Big:           bigBytes,
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

	// Legacy snapshot fallback (v1 UnivMon grid)
	if len(snap.Cells) == 0 && len(snap.Grid) > 0 {
		if len(snap.Grid) != snap.D {
			return nil, errors.New("invalid snapshot grid depth")
		}
		h := &Hydra{
			D:             snap.D,
			W:             snap.W,
			enableTopK:    true,
			fanoutSubkeys: true,
			seedHydra:     defaultHydraSeed,
			seedCM1:       0x1111111111111111,
			seedCM2:       0x2222222222222222,
			cells:         make([]HydraCounter, snap.D*snap.W),
		}
		if snap.Version >= 1 {
			h.enableTopK = snap.EnableTopK
			h.seedCM1 = snap.SeedCM1
			h.seedCM2 = snap.SeedCM2
		}

		for i := 0; i < snap.D; i++ {
			if len(snap.Grid[i]) != snap.W {
				return nil, errors.New("invalid snapshot grid width")
			}
			for j := 0; j < snap.W; j++ {
				um, err := univmon.DeserializeUnivSketchFromBytes(snap.Grid[i][j])
				if err != nil {
					return nil, err
				}
				um.SetTopKEnabled(h.enableTopK)
				h.cells[i*h.W+j] = &univCounter{s: um}
			}
		}
		if len(snap.Big) > 0 {
			um, err := univmon.DeserializeUnivSketchFromBytes(snap.Big)
			if err != nil {
				return nil, err
			}
			um.SetTopKEnabled(h.enableTopK)
			h.bigCounter = &univCounter{s: um}
		}
		h.typeToClone = &univCounter{}
		return h, nil
	}

	if len(snap.Cells) != snap.D*snap.W {
		return nil, errors.New("invalid snapshot cells length")
	}
	if snap.CounterType == "" {
		return nil, errors.New("invalid snapshot counter type")
	}

	h := &Hydra{
		D:             snap.D,
		W:             snap.W,
		enableTopK:    true,
		fanoutSubkeys: true,
		seedHydra:     defaultHydraSeed,
		seedCM1:       0x1111111111111111,
		seedCM2:       0x2222222222222222,
		cells:         make([]HydraCounter, len(snap.Cells)),
	}
	if snap.Version >= 2 {
		h.enableTopK = snap.EnableTopK
		h.fanoutSubkeys = snap.FanoutSubkeys
		if snap.SeedHydra != 0 {
			h.seedHydra = snap.SeedHydra
		}
		if snap.SeedCM1 != 0 {
			h.seedCM1 = snap.SeedCM1
		}
		if snap.SeedCM2 != 0 {
			h.seedCM2 = snap.SeedCM2
		}
	}

	for i := range snap.Cells {
		counter, err := decodeCounter(snap.CounterType, snap.Cells[i])
		if err != nil {
			return nil, err
		}
		counter.SetTopKEnabled(h.enableTopK)
		h.cells[i] = counter
	}
	if len(h.cells) > 0 {
		h.typeToClone, _ = h.cells[0].Clone()
	}

	if len(snap.Big) > 0 {
		counter, err := decodeCounter(snap.CounterType, snap.Big)
		if err != nil {
			return nil, err
		}
		counter.SetTopKEnabled(h.enableTopK)
		h.bigCounter = counter
	}
	return h, nil
}
