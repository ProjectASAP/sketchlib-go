package hydrasketch

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"reflect"

	univmon "github.com/ProjectASAP/sketchlib-go/sketch_framework/UnivMon"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
	hll "github.com/ProjectASAP/sketchlib-go/sketches/HLL"
	kll "github.com/ProjectASAP/sketchlib-go/sketches/KLL"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// Wire constants: matrix counters are fast-mode i32, KLL items f64, HLL
// registers precision 14.
const (
	wireCounterType  = "i32"
	wireCounterMode  = "fast"
	wireItemType     = "f64"
	wirePrecision    = hll.HLLPrecision
	maxWireRows      = 20
	kllMaxLevels     = 61
	kllCapacityDecay = 2.0 / 3.0
)

// checkedCells returns rows*cols, failing for a zero dimension, more than
// maxWireRows rows, or a product that overflows int.
func checkedCells(what string, rows, cols uint64) (int, error) {
	if rows == 0 || cols == 0 {
		return 0, fmt.Errorf("hydra: %s dimensions must be non-zero: rows=%d, cols=%d", what, rows, cols)
	}
	if rows > maxWireRows {
		return 0, fmt.Errorf("hydra: %s rows %d exceeds %d", what, rows, maxWireRows)
	}
	return tiledLen(what, rows, cols)
}

// tiledLen returns n*per, failing when it overflows int.
func tiledLen(what string, n, per uint64) (int, error) {
	hi, lo := bits.Mul64(n, per)
	if hi != 0 || lo > math.MaxInt {
		return 0, fmt.Errorf("hydra: %d x %d %s overflows a length", n, per, what)
	}
	return int(lo), nil
}

// kllMaxItems is the most items a compact KLL of k and m retains.
func kllMaxItems(k, m int) int {
	total, scale := 0, 1.0
	for range kllMaxLevels {
		total += max(int(math.Ceil(float64(k)*scale)), m)
		scale *= kllCapacityDecay
	}
	return total
}

// gridMetadata writes the metadata keys every variant shares.
func (h *Hydra) gridMetadata(idx asapv1.SeedIndex) *asapv1.MetadataWriter {
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), idx)
	md.Uint("rows", uint64(h.rows))
	md.Uint("cols", uint64(h.cols))
	md.Strs("schema", h.schema.labels)
	return md
}

// checkCells fails unless the grid is wire-eligible and every cell has the
// prototype's concrete type.
func (h *Hydra) checkCells() error {
	if h.cols > math.MaxUint32 {
		return fmt.Errorf("hydra: cols %d exceeds u32", h.cols)
	}
	n, err := checkedCells("grid", uint64(h.rows), uint64(h.cols))
	if err != nil {
		return err
	}
	if len(h.cells) != n {
		return fmt.Errorf("hydra: storage holds %d cells != rows*cols %d", len(h.cells), n)
	}
	want := reflect.TypeOf(h.proto)
	for i, c := range h.cells {
		if reflect.TypeOf(c) != want {
			return fmt.Errorf("hydra: cell (%d, %d) is not the grid's %s counter", i/h.cols, i%h.cols, h.proto.CounterType())
		}
	}
	return nil
}

func freshPrototype(fresh bool, c HydraCounter) error {
	if fresh {
		return nil
	}
	return fmt.Errorf("hydra: the %s prototype holds data; it carries only its geometry", c.CounterType())
}

// MarshalASAPv1 encodes the grid as the ASAPv1 Hydra kind of its counter
// variant. It fails for more than 20 rows, a prototype holding data, a cell
// whose geometry differs from the prototype's, and a cell its codec refuses.
func (h *Hydra) MarshalASAPv1() ([]byte, error) {
	if err := h.checkCells(); err != nil {
		return nil, err
	}
	switch p := h.proto.(type) {
	case *countMinCounter:
		return h.marshalMatrix(asapv1.KindHydraCountMin, p.s.Rows, p.s.Cols, matrixCountsOf(p.s))
	case *countSketchCounter:
		return h.marshalMatrix(asapv1.KindHydraCountSketch, p.s.Rows, p.s.Cols, matrixCountsOf(p.s))
	case *hllCounter:
		return h.marshalHLL(p)
	case *kllCounter:
		return h.marshalKLL(p)
	case univMonCell:
		return h.marshalUnivMon(p)
	}
	return nil, fmt.Errorf("hydra: no ASAPv1 encoding for counter %T", h.proto)
}

// matrixCountsOf returns the counts of a matrix counter, read back from its
// own ASAPv1 encoding.
func matrixCountsOf(s asapv1.Marshaler) func() ([]int32, error) {
	return func() ([]int32, error) {
		b, err := s.MarshalASAPv1()
		if err != nil {
			return nil, err
		}
		kind, mdBytes, payload, err := asapv1.Split(b)
		if err != nil {
			return nil, err
		}
		md, err := asapv1.ReadMetadata(mdBytes)
		if err != nil {
			return nil, err
		}
		counterType := md.Str("counter_type")
		mode := md.Str("mode")
		if err := md.Err(); err != nil {
			return nil, err
		}
		if mode != wireCounterMode {
			return nil, fmt.Errorf("hydra: matrix counter mode %q is not %q", mode, wireCounterMode)
		}
		d := asapv1.NewDecoder(payload)
		d.ExpectArray(1)
		var counts []int32
		switch {
		case kind == asapv1.KindCountMin && counterType == "f64":
			for _, v := range asapv1.DecodeFloat64s(d) {
				if v != math.Trunc(v) || v < math.MinInt32 || v > math.MaxInt32 {
					return nil, fmt.Errorf("hydra: Count-Min counter %v is not an i32", v)
				}
				counts = append(counts, int32(v))
			}
		case kind == asapv1.KindCountSketch && counterType == wireCounterType:
			counts = asapv1.DecodeInts[int32](d)
		default:
			return nil, fmt.Errorf("hydra: matrix counter %s with counter_type %q has no Hydra encoding", kind, counterType)
		}
		if err := d.Finish(); err != nil {
			return nil, err
		}
		return counts, nil
	}
}

func (h *Hydra) marshalMatrix(kind asapv1.KindID, rows, cols int, protoCounts func() ([]int32, error)) ([]byte, error) {
	per, err := checkedCells("counter", uint64(rows), uint64(cols))
	if err != nil {
		return nil, err
	}
	total, err := tiledLen("counters", uint64(len(h.cells)), uint64(per))
	if err != nil {
		return nil, err
	}
	pc, err := protoCounts()
	if err != nil {
		return nil, err
	}
	fresh := true
	for _, v := range pc {
		fresh = fresh && v == 0
	}
	if err := freshPrototype(fresh, h.proto); err != nil {
		return nil, err
	}
	counts := make([]int32, 0, total)
	for i, c := range h.cells {
		var cellRows, cellCols int
		var cellCounts func() ([]int32, error)
		switch c := c.(type) {
		case *countMinCounter:
			cellRows, cellCols, cellCounts = c.s.Rows, c.s.Cols, matrixCountsOf(c.s)
		case *countSketchCounter:
			cellRows, cellCols, cellCounts = c.s.Rows, c.s.Cols, matrixCountsOf(c.s)
		}
		if cellRows != rows || cellCols != cols {
			return nil, fmt.Errorf("hydra: cell (%d, %d) is %dx%d against the prototype's %dx%d",
				i/h.cols, i%h.cols, cellRows, cellCols, rows, cols)
		}
		cc, err := cellCounts()
		if err != nil {
			return nil, fmt.Errorf("hydra: cell (%d, %d): %w", i/h.cols, i%h.cols, err)
		}
		if len(cc) != per {
			return nil, fmt.Errorf("hydra: cell (%d, %d) holds %d counters != %d", i/h.cols, i%h.cols, len(cc), per)
		}
		counts = append(counts, cc...)
	}
	md := h.gridMetadata(asapv1.SeedIndexMatrix)
	md.Uint("counter_rows", uint64(rows))
	md.Uint("counter_cols", uint64(cols))
	md.Str("counter_type", wireCounterType)
	md.Str("counter_mode", wireCounterMode)
	p := asapv1.NewEncoder()
	p.Array(1)
	asapv1.EncodeInts(p, counts)
	return asapv1.Marshal(kind, md, p)
}

// hllRegisters returns an HLL's registers, read back from its own ASAPv1
// encoding.
func hllRegisters(s *hll.HyperLogLog) ([]byte, error) {
	b, err := s.MarshalASAPv1()
	if err != nil {
		return nil, err
	}
	_, payload, err := asapv1.SplitKind(b, asapv1.KindHLLErtlMLE)
	if err != nil {
		return nil, err
	}
	d := asapv1.NewDecoder(payload)
	d.ExpectArray(1)
	regs := d.Bin()
	if err := d.Finish(); err != nil {
		return nil, err
	}
	return regs, nil
}

func (h *Hydra) marshalHLL(p *hllCounter) ([]byte, error) {
	const per = 1 << wirePrecision
	total, err := tiledLen("registers", uint64(len(h.cells)), per)
	if err != nil {
		return nil, err
	}
	pr, err := hllRegisters(p.s)
	if err != nil {
		return nil, err
	}
	fresh := true
	for _, r := range pr {
		fresh = fresh && r == 0
	}
	if err := freshPrototype(fresh, p); err != nil {
		return nil, err
	}
	regs := make([]byte, 0, total)
	for i, c := range h.cells {
		cr, err := hllRegisters(c.(*hllCounter).s)
		if err != nil {
			return nil, fmt.Errorf("hydra: cell (%d, %d): %w", i/h.cols, i%h.cols, err)
		}
		regs = append(regs, cr...)
	}
	md := h.gridMetadata(asapv1.SeedIndexCanonical)
	md.Uint("counter_precision", wirePrecision)
	e := asapv1.NewEncoder()
	e.Array(1)
	e.Bin(regs)
	return asapv1.Marshal(asapv1.KindHydraHLL, md, e)
}

// kllEmpty reports whether a KLL holds one empty level, as a fresh one does.
func kllEmpty(s *kll.KLLSketch) (bool, error) {
	e := asapv1.NewEncoder()
	if err := s.EncodeASAPv1Payload(e); err != nil {
		return false, err
	}
	d := asapv1.NewDecoder(e.Bytes())
	d.ExpectArray(3)
	levels := asapv1.DecodeUints[uint32](d)
	return len(levels) == 2 && s.GetRetainedItems() == 0, d.Err()
}

func (h *Hydra) marshalKLL(p *kllCounter) ([]byte, error) {
	k, m := p.s.K(), p.s.M()
	fresh, err := kllEmpty(p.s)
	if err != nil {
		return nil, err
	}
	if err := freshPrototype(fresh, p); err != nil {
		return nil, err
	}
	e := asapv1.NewEncoder()
	e.Array(1)
	e.Array(len(h.cells))
	for i, c := range h.cells {
		s := c.(*kllCounter).s
		if s.K() != k || s.M() != m {
			return nil, fmt.Errorf("hydra: cell (%d, %d) has k=%d, m=%d against the prototype's k=%d, m=%d",
				i/h.cols, i%h.cols, s.K(), s.M(), k, m)
		}
		if n, limit := s.GetRetainedItems(), kllMaxItems(k, m); n > limit {
			return nil, fmt.Errorf("hydra: cell (%d, %d) retains %d items, above the compact KLL's %d", i/h.cols, i%h.cols, n, limit)
		}
		if err := s.EncodeASAPv1Payload(e); err != nil {
			return nil, fmt.Errorf("hydra: cell (%d, %d): %w", i/h.cols, i%h.cols, err)
		}
	}
	md := h.gridMetadata(asapv1.SeedIndexNone)
	md.Uint("counter_k", uint64(k))
	md.Uint("counter_m", uint64(m))
	md.Str("counter_item_type", wireItemType)
	return asapv1.Marshal(asapv1.KindHydraKLL, md, e)
}

// univMonCell is a univMonCounter of any key type.
type univMonCell interface {
	HydraCounter
	shape() [4]int
	holdsKeys() bool
	keyType() string
	fresh() (bool, error)
	encodePayload(e *asapv1.Encoder) error
}

// shape returns layer_size, sketch_row, sketch_col and heap_size.
func (c *univMonCounter[K]) shape() [4]int {
	return [4]int{c.s.LayerSize(), c.s.SketchRow(), c.s.SketchCol(), c.s.HeapSize()}
}

func (c *univMonCounter[K]) holdsKeys() bool {
	for l := range c.s.LayerSize() {
		if len(c.s.HeapEntries(l)) > 0 {
			return true
		}
	}
	return false
}

func (c *univMonCounter[K]) keyType() string { return asapv1.HeapKeyType[K]() }

func (c *univMonCounter[K]) fresh() (bool, error) {
	empty, err := univmon.NewUnivMon[K](c.s.HeapSize(), c.s.SketchRow(), c.s.SketchCol(), c.s.LayerSize())
	if err != nil {
		return false, err
	}
	want, err := empty.MarshalASAPv1()
	if err != nil {
		return false, err
	}
	got, err := c.s.MarshalASAPv1()
	if err != nil {
		return false, err
	}
	return string(got) == string(want), nil
}

func (c *univMonCounter[K]) encodePayload(e *asapv1.Encoder) error { return c.s.EncodeASAPv1Payload(e) }

func (h *Hydra) marshalUnivMon(p univMonCell) ([]byte, error) {
	shape := p.shape()
	fresh, err := p.fresh()
	if err != nil {
		return nil, err
	}
	if err := freshPrototype(fresh, p); err != nil {
		return nil, err
	}
	keyType := asapv1.EmptyHeapKeyType
	e := asapv1.NewEncoder()
	e.Array(1)
	e.Array(len(h.cells))
	for i, c := range h.cells {
		u := c.(univMonCell)
		if u.shape() != shape {
			return nil, fmt.Errorf("hydra: cell (%d, %d) is a %v pyramid against the prototype's %v", i/h.cols, i%h.cols, u.shape(), shape)
		}
		if u.holdsKeys() {
			keyType = u.keyType()
		}
		if err := u.encodePayload(e); err != nil {
			return nil, fmt.Errorf("hydra: cell (%d, %d): %w", i/h.cols, i%h.cols, err)
		}
	}
	md := h.gridMetadata(asapv1.SeedIndexNone)
	md.Uint("counter_layer_size", uint64(shape[0]))
	md.Uint("counter_sketch_row", uint64(shape[1]))
	md.Uint("counter_sketch_col", uint64(shape[2]))
	md.Uint("counter_heap_size", uint64(shape[3]))
	md.Str("counter_key_type", keyType)
	return asapv1.Marshal(asapv1.KindHydraUnivMon, md, e)
}

// UnmarshalASAPv1 replaces h with the grid in b, of any Hydra kind, rebuilding
// each cell through its counter's own decoder. UnivMon cells take the Go key
// type counter_key_type names; "isize" and "usize" have none.
func (h *Hydra) UnmarshalASAPv1(b []byte) error {
	kind, mdBytes, payload, err := asapv1.Split(b)
	if err != nil {
		return err
	}
	md, err := asapv1.ReadMetadata(mdBytes)
	if err != nil {
		return err
	}
	var idx asapv1.SeedIndex
	switch kind {
	case asapv1.KindHydraCountMin, asapv1.KindHydraCountSketch:
		idx = asapv1.SeedIndexMatrix
	case asapv1.KindHydraHLL:
		idx = asapv1.SeedIndexCanonical
	case asapv1.KindHydraKLL, asapv1.KindHydraUnivMon:
		idx = asapv1.SeedIndexNone
	default:
		return fmt.Errorf("hydra: kind_id %s is not a Hydra kind", kind)
	}
	md.ExpectVersion(1)
	md.HashSpec(asapv1.StandardProfile(), idx)
	rows := md.Uint32("rows")
	cols := md.Uint32("cols")
	labels := md.Strs("schema")
	d := asapv1.NewDecoder(payload)
	var out *Hydra
	switch kind {
	case asapv1.KindHydraCountMin, asapv1.KindHydraCountSketch:
		out, err = unmarshalMatrix(kind, md, d, rows, cols, labels)
	case asapv1.KindHydraHLL:
		out, err = unmarshalHLL(md, d, rows, cols, labels)
	case asapv1.KindHydraKLL:
		out, err = unmarshalKLL(md, d, rows, cols, labels)
	case asapv1.KindHydraUnivMon:
		out, err = unmarshalUnivMon(md, d, rows, cols, labels)
	}
	if err != nil {
		return err
	}
	if err := d.Finish(); err != nil {
		return err
	}
	*h = *out
	return nil
}

// grid validates the metadata's grid and schema after md.Finish.
func grid(md *asapv1.MetadataReader, rows, cols uint32, labels []string) (int, keySchema, error) {
	if err := md.Finish(); err != nil {
		return 0, keySchema{}, err
	}
	n, err := checkedCells("grid", uint64(rows), uint64(cols))
	if err != nil {
		return 0, keySchema{}, err
	}
	ks, err := newKeySchema(labels)
	return n, ks, err
}

func unmarshalMatrix(kind asapv1.KindID, md *asapv1.MetadataReader, d *asapv1.Decoder, rows, cols uint32, labels []string) (*Hydra, error) {
	counterRows := md.Uint32("counter_rows")
	counterCols := md.Uint32("counter_cols")
	md.ExpectStr("counter_type", wireCounterType)
	md.ExpectStr("counter_mode", wireCounterMode)
	n, ks, err := grid(md, rows, cols, labels)
	if err != nil {
		return nil, err
	}
	per, err := checkedCells("counter", uint64(counterRows), uint64(counterCols))
	if err != nil {
		return nil, err
	}
	total, err := tiledLen("counters", uint64(n), uint64(per))
	if err != nil {
		return nil, err
	}
	d.ExpectArray(1)
	counts := asapv1.DecodeInts[int32](d)
	if err := d.Err(); err != nil {
		return nil, err
	}
	if len(counts) != total {
		return nil, fmt.Errorf("hydra: counts length %d != rows*cols*counter_rows*counter_cols %d", len(counts), total)
	}
	build := func(run []int32) (HydraCounter, error) {
		cm := asapv1.NewMetadataWriter(1)
		cm.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
		cm.Uint("rows", uint64(counterRows))
		cm.Uint("cols", uint64(counterCols))
		p := asapv1.NewEncoder()
		p.Array(1)
		if kind == asapv1.KindHydraCountMin {
			cm.Str("counter_type", "f64")
			cm.Str("mode", wireCounterMode)
			p.Array(len(run))
			for _, v := range run {
				p.Float64(float64(v))
			}
			b, err := asapv1.Marshal(asapv1.KindCountMin, cm, p)
			if err != nil {
				return nil, err
			}
			s := new(countminsketch.CountMinSketch)
			if err := s.UnmarshalASAPv1(b); err != nil {
				return nil, err
			}
			return &countMinCounter{s: s}, nil
		}
		cm.Str("counter_type", wireCounterType)
		cm.Str("mode", wireCounterMode)
		asapv1.EncodeInts(p, run)
		b, err := asapv1.Marshal(asapv1.KindCountSketch, cm, p)
		if err != nil {
			return nil, err
		}
		s := new(countsketch.CountSketch)
		if err := s.UnmarshalASAPv1(b); err != nil {
			return nil, err
		}
		return &countSketchCounter{s: s}, nil
	}
	cells := make([]HydraCounter, n)
	for i := range cells {
		if cells[i], err = build(counts[i*per : (i+1)*per]); err != nil {
			return nil, err
		}
	}
	proto, err := build(make([]int32, per))
	if err != nil {
		return nil, err
	}
	return newGrid(int(rows), int(cols), ks, cells, proto), nil
}

func unmarshalHLL(md *asapv1.MetadataReader, d *asapv1.Decoder, rows, cols uint32, labels []string) (*Hydra, error) {
	md.ExpectUint("counter_precision", wirePrecision)
	n, ks, err := grid(md, rows, cols, labels)
	if err != nil {
		return nil, err
	}
	const per = 1 << wirePrecision
	total, err := tiledLen("registers", uint64(n), per)
	if err != nil {
		return nil, err
	}
	d.ExpectArray(1)
	regs := d.Bin()
	if err := d.Err(); err != nil {
		return nil, err
	}
	if len(regs) != total {
		return nil, fmt.Errorf("hydra: registers length %d != rows*cols*2^precision %d", len(regs), total)
	}
	cells := make([]HydraCounter, n)
	for i := range cells {
		cm := asapv1.NewMetadataWriter(1)
		cm.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexCanonical)
		cm.Uint("precision", wirePrecision)
		p := asapv1.NewEncoder()
		p.Array(1)
		p.Bin(regs[i*per : (i+1)*per])
		b, err := asapv1.Marshal(asapv1.KindHLLErtlMLE, cm, p)
		if err != nil {
			return nil, err
		}
		s := new(hll.HyperLogLog)
		if err := s.UnmarshalASAPv1(b); err != nil {
			return nil, err
		}
		cells[i] = &hllCounter{s: s}
	}
	return newGrid(int(rows), int(cols), ks, cells, NewHydraHLLCounter()), nil
}

// decodeKLLCell reads one KLL payload from d through KLL's own decoder, and
// rejects more items than a compact KLL of k and m retains.
func decodeKLLCell(k, m uint32, d *asapv1.Decoder) (HydraCounter, error) {
	cm := asapv1.NewMetadataWriter(1)
	cm.Uint("k", uint64(k))
	cm.Uint("m", uint64(m))
	cm.Str("item_type", wireItemType)
	md, err := asapv1.ReadMetadata(cm.Bytes())
	if err != nil {
		return nil, err
	}
	s := new(kll.KLLSketch)
	if err := s.DecodeASAPv1Payload(md, d); err != nil {
		return nil, err
	}
	if n, limit := s.GetRetainedItems(), kllMaxItems(int(k), int(m)); n > limit {
		return nil, fmt.Errorf("hydra: KLL cell retains %d items, above the compact KLL's %d", n, limit)
	}
	return &kllCounter{s: s}, nil
}

func unmarshalKLL(md *asapv1.MetadataReader, d *asapv1.Decoder, rows, cols uint32, labels []string) (*Hydra, error) {
	k := md.Uint32("counter_k")
	m := md.Uint32("counter_m")
	md.ExpectStr("counter_item_type", wireItemType)
	n, ks, err := grid(md, rows, cols, labels)
	if err != nil {
		return nil, err
	}
	d.ExpectArray(1)
	if got := d.Array(); d.Err() == nil && got != n {
		return nil, fmt.Errorf("hydra: %d KLL cells != rows*cols %d", got, n)
	}
	if err := d.Err(); err != nil {
		return nil, err
	}
	cells := make([]HydraCounter, n)
	for i := range cells {
		if cells[i], err = decodeKLLCell(k, m, d); err != nil {
			return nil, err
		}
	}
	return newGrid(int(rows), int(cols), ks, cells, NewHydraKLLCounter(int(k), int(m))), nil
}

func unmarshalUnivMon(md *asapv1.MetadataReader, d *asapv1.Decoder, rows, cols uint32, labels []string) (*Hydra, error) {
	var shape [4]uint32
	for i, key := range [4]string{"counter_layer_size", "counter_sketch_row", "counter_sketch_col", "counter_heap_size"} {
		shape[i] = md.Uint32(key)
	}
	keyType := md.Str("counter_key_type")
	n, ks, err := grid(md, rows, cols, labels)
	if err != nil {
		return nil, err
	}
	if shape[0] == 0 || shape[1] == 0 || shape[2] == 0 || shape[3] == 0 {
		return nil, fmt.Errorf("hydra: UnivMon counter dimensions must be non-zero: layers=%d, %dx%d, heap=%d",
			shape[0], shape[1], shape[2], shape[3])
	}
	var cells []HydraCounter
	var proto HydraCounter
	switch keyType {
	case "i8":
		cells, proto, err = decodeUnivMonCells[int8](d, n, shape, keyType)
	case "i16":
		cells, proto, err = decodeUnivMonCells[int16](d, n, shape, keyType)
	case "i32":
		cells, proto, err = decodeUnivMonCells[int32](d, n, shape, keyType)
	case "i64":
		cells, proto, err = decodeUnivMonCells[int64](d, n, shape, keyType)
	case "u8":
		cells, proto, err = decodeUnivMonCells[uint8](d, n, shape, keyType)
	case "u16":
		cells, proto, err = decodeUnivMonCells[uint16](d, n, shape, keyType)
	case "u32":
		cells, proto, err = decodeUnivMonCells[uint32](d, n, shape, keyType)
	case "u64":
		cells, proto, err = decodeUnivMonCells[uint64](d, n, shape, keyType)
	case "f32":
		cells, proto, err = decodeUnivMonCells[float32](d, n, shape, keyType)
	case "f64":
		cells, proto, err = decodeUnivMonCells[float64](d, n, shape, keyType)
	case "string":
		cells, proto, err = decodeUnivMonCells[string](d, n, shape, keyType)
	case "bytes":
		cells, proto, err = decodeUnivMonCells[[]byte](d, n, shape, keyType)
	default:
		return nil, fmt.Errorf("hydra: counter_key_type %q has no Go heap key type", keyType)
	}
	if err != nil {
		return nil, err
	}
	return newGrid(int(rows), int(cols), ks, cells, proto), nil
}

// decodeUnivMonCells reads n UnivMon payloads from d through UnivMon's own
// decoder, keyed by K.
func decodeUnivMonCells[K asapv1.HeapKey](d *asapv1.Decoder, n int, shape [4]uint32, keyType string) ([]HydraCounter, HydraCounter, error) {
	d.ExpectArray(1)
	if got := d.Array(); d.Err() == nil && got != n {
		return nil, nil, fmt.Errorf("hydra: %d UnivMon cells != rows*cols %d", got, n)
	}
	if err := d.Err(); err != nil {
		return nil, nil, err
	}
	cells := make([]HydraCounter, n)
	for i := range cells {
		cm := asapv1.NewMetadataWriter(1)
		cm.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexNone)
		cm.Uint("layer_size", uint64(shape[0]))
		cm.Uint("sketch_row", uint64(shape[1]))
		cm.Uint("sketch_col", uint64(shape[2]))
		cm.Uint("heap_size", uint64(shape[3]))
		cm.Str("key_type", keyType)
		md, err := asapv1.ReadMetadata(cm.Bytes())
		if err != nil {
			return nil, nil, err
		}
		s := new(univmon.UnivMon[K])
		if err := s.DecodeASAPv1Payload(md, d); err != nil {
			return nil, nil, err
		}
		cells[i] = &univMonCounter[K]{s: s}
	}
	proto, err := NewHydraUnivMonCounter[K](int(shape[3]), int(shape[1]), int(shape[2]), int(shape[0]))
	if err != nil {
		return nil, nil, errors.Join(errors.New("hydra: UnivMon counter shape"), err)
	}
	return cells, proto, nil
}
