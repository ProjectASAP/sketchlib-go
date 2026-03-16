package hydrasketch

import (
	"errors"
	"strings"
	"sync"

	"github.com/ProjectASAP/sketchlib-go/common"
)

// HydraDimension defines one dimension name and its counter template.
type HydraDimension struct {
	Name    string
	Counter HydraCounter
}

// MultiHeadValue maps one value insertion to selected dimensions.
type MultiHeadValue struct {
	Value      *common.SketchInput
	Dimensions []string
}

// MultiHeadHydra mirrors sketchlib-rust MultiHeadHydra.
type MultiHeadHydra struct {
	D int
	W int

	dimensions []HydraDimension
	dimIndex   map[string]int
	cells      [][]HydraCounter // flattened D*W, each cell owns len(dimensions) counters

	seedHydra int
	seedCM1   uint64
	seedCM2   uint64
	fanout    bool

	mu sync.Mutex
}

func NewMultiHeadHydra(d, w int, dims []HydraDimension) (*MultiHeadHydra, error) {
	if d <= 0 || w <= 0 {
		return nil, errors.New("invalid D/W dimensions")
	}
	if len(dims) == 0 {
		return nil, errors.New("dimensions cannot be empty")
	}

	idx := make(map[string]int, len(dims))
	for i, dim := range dims {
		if dim.Name == "" || dim.Counter == nil {
			return nil, errors.New("invalid dimension config")
		}
		idx[dim.Name] = i
	}

	m := &MultiHeadHydra{
		D:          d,
		W:          w,
		dimensions: dims,
		dimIndex:   idx,
		cells:      make([][]HydraCounter, d*w),
		seedHydra:  defaultHydraSeed,
		seedCM1:    0x1111111111111111,
		seedCM2:    0x2222222222222222,
		fanout:     true,
	}

	for i := range m.cells {
		cell := make([]HydraCounter, len(dims))
		for j := range dims {
			c, err := dims[j].Counter.Clone()
			if err != nil {
				return nil, err
			}
			cell[j] = c
		}
		m.cells[i] = cell
	}

	return m, nil
}

// Update applies one key fan-out and multiple value insertions by dimension.
func (m *MultiHeadHydra) Update(key string, values []MultiHeadValue, count int64) {
	if count <= 0 || len(values) == 0 {
		return
	}

	subkeys := m.expandSubkeys(key)

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, subkey := range subkeys {
		var posStack [16]int
		pos := posStack[:0]
		if m.D <= len(posStack) {
			pos = posStack[:m.D]
		} else {
			pos = make([]int, m.D)
		}
		m.fillPositionsFromSubKey(subkey, pos)

		for _, v := range values {
			if v.Value == nil {
				continue
			}
			for _, dimName := range v.Dimensions {
				dimIdx, ok := m.dimIndex[dimName]
				if !ok {
					continue
				}
				for r := 0; r < m.D; r++ {
					m.cells[r*m.W+pos[r]][dimIdx].InsertWithHash(v.Value, v.Value.Hash, count)
				}
			}
		}
	}
}

// QueryKey queries one dimension counter over a subpopulation key.
func (m *MultiHeadHydra) QueryKey(key []string, dimension string, query HydraQuery) float64 {
	dimIdx, ok := m.dimIndex[dimension]
	if !ok {
		return 0
	}
	joined := strings.Join(key, ";")

	var posStack [16]int
	pos := posStack[:0]
	if m.D <= len(posStack) {
		pos = posStack[:m.D]
	} else {
		pos = make([]int, m.D)
	}
	m.fillPositionsFromSubKey(joined, pos)

	values := make([]float64, m.D)

	m.mu.Lock()
	defer m.mu.Unlock()

	for r := 0; r < m.D; r++ {
		v, err := m.cells[r*m.W+pos[r]][dimIdx].Query(query)
		if err != nil {
			values[r] = 0
			continue
		}
		values[r] = v
	}
	return common.ComputeMedianInlineF64(values)
}

func (m *MultiHeadHydra) expandSubkeys(key string) []string {
	if !m.fanout {
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

func (m *MultiHeadHydra) fillPositionsFromSubKey(key string, out []int) {
	m.fillPositionsFromHash(common.HashIt(m.seedHydra, []byte(key)), out)
}

func (m *MultiHeadHydra) fillPositionsFromHash(hash uint64, out []int) {
	x := hash ^ m.seedCM1
	y := hash ^ m.seedCM2
	if x == 0 {
		x = m.seedCM1
	}
	if y == 0 {
		y = m.seedCM2 | 1
	}
	for r := 0; r < m.D; r++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		y ^= y << 13
		y ^= y >> 7
		y ^= y << 17
		out[r] = int((x ^ (y << 1)) % uint64(m.W))
	}
}
