// Package hydrasketch implements Hydra (Manousis et al., VLDB 2022): a grid of
// counters over named key columns, each record fanned out into its 2^D - 1
// subpopulations and each query answered by the median of the rows.
package hydrasketch

import (
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

// MaxKeyColumns is the most key columns a schema declares.
const MaxKeyColumns = 16

// hydraSeed is the seed index subkeys are hashed at.
const hydraSeed = 6

// keySchema is the key columns' labels and their escaped forms. A
// subpopulation encodes, in declaration order, as label ":" value joined by
// ";", with "\", ":" and ";" escaped by "\" in labels and values.
type keySchema struct {
	labels  []string
	escaped []string
}

func newKeySchema(labels []string) (keySchema, error) {
	if len(labels) == 0 {
		return keySchema{}, errors.New("hydra: schema must declare at least one key column")
	}
	if len(labels) > MaxKeyColumns {
		return keySchema{}, fmt.Errorf("hydra: schema supports at most %d key columns, got %d", MaxKeyColumns, len(labels))
	}
	sorted := slices.Sorted(slices.Values(labels))
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			return keySchema{}, fmt.Errorf("hydra: schema contains duplicate column label %q", sorted[i])
		}
	}
	s := keySchema{labels: slices.Clone(labels), escaped: make([]string, len(labels))}
	for i, l := range labels {
		if !utf8.ValidString(l) {
			return keySchema{}, fmt.Errorf("hydra: column label %q is not valid UTF-8", l)
		}
		var b strings.Builder
		writeEscaped(&b, l)
		s.escaped[i] = b.String()
	}
	return s, nil
}

func writeEscaped(b *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '\\' || c == ':' || c == ';' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
}

func (s keySchema) checkArity(got int) error {
	if got != len(s.labels) {
		return fmt.Errorf("hydra: key arity mismatch: schema declares %d columns, got %d", len(s.labels), got)
	}
	return nil
}

// subkey returns the encoding of {labels[i] = values[i] : bit i of mask set}.
func (s keySchema) subkey(b *strings.Builder, values []string, mask uint32) string {
	b.Reset()
	first := true
	for col, label := range s.escaped {
		if mask>>col&1 == 0 {
			continue
		}
		if !first {
			b.WriteByte(';')
		}
		b.WriteString(label)
		b.WriteByte(':')
		writeEscaped(b, values[col])
		first = false
	}
	return b.String()
}

// Hydra is a rows x cols grid of counters, every cell a clone of one
// prototype, over the key columns of its schema.
type Hydra struct {
	rows, cols int
	maskBits   uint
	mask       uint64
	schema     keySchema
	cells      []HydraCounter
	proto      HydraCounter
}

// NewHydra returns a rows x cols grid over the named key columns, each cell
// and the grid's prototype a clone of counter.
func NewHydra(rows, cols int, schema []string, counter HydraCounter) (*Hydra, error) {
	if rows <= 0 || cols <= 0 {
		return nil, fmt.Errorf("hydra: grid dimensions must be positive, got %dx%d", rows, cols)
	}
	if counter == nil {
		return nil, errors.New("hydra: nil counter")
	}
	ks, err := newKeySchema(schema)
	if err != nil {
		return nil, err
	}
	cells := make([]HydraCounter, rows*cols)
	for i := range cells {
		if cells[i], err = counter.Clone(); err != nil {
			return nil, err
		}
	}
	proto, err := counter.Clone()
	if err != nil {
		return nil, err
	}
	return newGrid(rows, cols, ks, cells, proto), nil
}

func newGrid(rows, cols int, schema keySchema, cells []HydraCounter, proto HydraCounter) *Hydra {
	maskBits := uint(bits.Len(uint(cols - 1)))
	return &Hydra{
		rows: rows, cols: cols,
		maskBits: maskBits, mask: 1<<maskBits - 1,
		schema: schema, cells: cells, proto: proto,
	}
}

// Rows returns the number of grid rows.
func (h *Hydra) Rows() int { return h.rows }

// Cols returns the number of grid columns.
func (h *Hydra) Cols() int { return h.cols }

// Schema returns the key-column labels in declaration order.
func (h *Hydra) Schema() []string { return slices.Clone(h.schema.labels) }

// CounterType returns the variant of the grid's counters.
func (h *Hydra) CounterType() HydraCounterType { return h.proto.CounterType() }

// Cell returns the counter at grid position (row, col).
func (h *Hydra) Cell(row, col int) HydraCounter { return h.cells[row*h.cols+col] }

// columns writes each row's column for subkey into out.
func (h *Hydra) columns(subkey string, out []int) {
	hashed := storage.BuildMatrixHashFromInputSeeded(hydraSeed, &common.SketchInput{Bytes: []byte(subkey)}, h.rows, h.cols)
	for r := range out {
		out[r] = int(hashed.RowHash(r, h.maskBits, h.mask) % uint64(h.cols))
	}
}

// Update inserts value with weight count into the cells of each of the 2^D - 1
// subpopulations of key, which holds one value per schema column. A counter
// error stops the update; cells already updated keep the insert.
func (h *Hydra) Update(key []string, value *common.SketchInput, count int64) error {
	if err := h.schema.checkArity(len(key)); err != nil {
		return err
	}
	if value == nil {
		return errNilValue
	}
	var b strings.Builder
	cols := make([]int, h.rows)
	for mask := uint32(1); mask < 1<<len(key); mask++ {
		h.columns(h.schema.subkey(&b, key, mask), cols)
		for r, c := range cols {
			if err := h.cells[r*h.cols+c].Insert(value, count); err != nil {
				return err
			}
		}
	}
	return nil
}

// Eq returns a query key entry constraining its column to v.
func Eq(v string) *string { return &v }

// QueryKey returns the median over rows of q answered by the subpopulation's
// cell. key holds one entry per schema column: a value constrains the column,
// nil leaves it unconstrained; at least one column must be constrained.
func (h *Hydra) QueryKey(key []*string, q HydraQuery) (float64, error) {
	if err := h.schema.checkArity(len(key)); err != nil {
		return 0, err
	}
	var mask uint32
	values := make([]string, len(key))
	for col, v := range key {
		if v != nil {
			mask |= 1 << col
			values[col] = *v
		}
	}
	if mask == 0 {
		return 0, errors.New("hydra: query must constrain at least one column")
	}
	if _, err := h.proto.Query(q); err != nil {
		return 0, err
	}
	var b strings.Builder
	cols := make([]int, h.rows)
	h.columns(h.schema.subkey(&b, values, mask), cols)
	estimates := make([]float64, h.rows)
	for r, c := range cols {
		if v, err := h.cells[r*h.cols+c].Query(q); err == nil {
			estimates[r] = v
		}
	}
	return common.ComputeMedianInlineF64(estimates), nil
}

// QueryFrequency returns the subpopulation's frequency estimate for value.
func (h *Hydra) QueryFrequency(key []*string, value *common.SketchInput) (float64, error) {
	return h.QueryKey(key, FrequencyQuery(value))
}

// QueryQuantile returns the subpopulation's CDF estimate at threshold.
func (h *Hydra) QueryQuantile(key []*string, threshold float64) (float64, error) {
	return h.QueryKey(key, CDFQuery(threshold))
}

// Merge adds o's cells into h's. The grids must share dimensions, counter
// variant and schema, labels in the same order. A counter that fails to merge
// leaves the cells before it merged.
func (h *Hydra) Merge(o *Hydra) error {
	if h.rows != o.rows || h.cols != o.cols {
		return fmt.Errorf("hydra: cannot merge a %dx%d grid into a %dx%d grid", o.rows, o.cols, h.rows, h.cols)
	}
	if reflect.TypeOf(h.proto) != reflect.TypeOf(o.proto) {
		return fmt.Errorf("hydra: cannot merge a %s grid into a %s grid", o.proto.CounterType(), h.proto.CounterType())
	}
	if !slices.Equal(h.schema.labels, o.schema.labels) {
		return fmt.Errorf("hydra: schema mismatch while merging: %q vs %q", h.schema.labels, o.schema.labels)
	}
	if len(h.cells) != len(o.cells) {
		return errors.New("hydra: storage length mismatch while merging")
	}
	for i, c := range h.cells {
		if err := c.Merge(o.cells[i]); err != nil {
			return err
		}
	}
	return nil
}
