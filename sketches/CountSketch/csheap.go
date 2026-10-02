package countsketch

import (
	"fmt"
	"math"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// CSHeap is a Count Sketch matrix paired with a top-k heap of string keys, in
// the form ASAPv1 kind CSHeap carries. ToCSHeap and ToCountSketch convert it
// from and to a CountSketch.
type CSHeap struct {
	Rows, Cols int
	// CounterType is "i32" or "i64"; Mode is "fast" or "regular".
	CounterType string
	Mode        string
	// Counts holds Rows*Cols signed cells, row-major.
	Counts []int64
	// K is the heap capacity; Heap holds at most K distinct keys.
	K    int
	Heap []common.Item
}

const (
	counterTypeI32 = "i32"
	counterTypeI64 = "i64"
	modeFast       = "fast"
	modeRegular    = "regular"
)

func checkCounterType(counterType string) error {
	if counterType != counterTypeI32 && counterType != counterTypeI64 {
		return fmt.Errorf("countsketch: CSHeap counter_type %q is not i32 or i64", counterType)
	}
	return nil
}

func checkMode(mode string) error {
	if mode != modeFast && mode != modeRegular {
		return fmt.Errorf("countsketch: CSHeap mode %q is not fast or regular", mode)
	}
	return nil
}

func checkDims(rows, cols uint64) error {
	if rows == 0 || cols == 0 {
		return fmt.Errorf("countsketch: CSHeap dimensions must be non-zero: rows=%d, cols=%d", rows, cols)
	}
	if maxRows := uint64(len(common.SeedList())); rows > maxRows {
		return fmt.Errorf("countsketch: CSHeap rows %d exceeds the %d seeds", rows, maxRows)
	}
	if cols > math.MaxUint32 {
		return fmt.Errorf("countsketch: CSHeap cols %d exceeds the u32 metadata field", cols)
	}
	return nil
}

func (h *CSHeap) validate() error {
	if h.Rows < 0 || h.Cols < 0 {
		return fmt.Errorf("countsketch: CSHeap dimensions must be positive: rows=%d, cols=%d", h.Rows, h.Cols)
	}
	if err := checkDims(uint64(h.Rows), uint64(h.Cols)); err != nil {
		return err
	}
	if err := checkCounterType(h.CounterType); err != nil {
		return err
	}
	if err := checkMode(h.Mode); err != nil {
		return err
	}
	if len(h.Counts) != h.Rows*h.Cols {
		return fmt.Errorf("countsketch: CSHeap counts length %d != rows*cols %d", len(h.Counts), h.Rows*h.Cols)
	}
	if h.CounterType == counterTypeI32 {
		for i, v := range h.Counts {
			if v < math.MinInt32 || v > math.MaxInt32 {
				return fmt.Errorf("countsketch: CSHeap cell %d value %d overflows i32", i, v)
			}
		}
	}
	if h.K < 0 || uint64(h.K) > math.MaxUint32 {
		return fmt.Errorf("countsketch: CSHeap k %d is outside the u32 metadata field", h.K)
	}
	if len(h.Heap) > h.K {
		return fmt.Errorf("countsketch: CSHeap heap holds %d entries, more than k=%d", len(h.Heap), h.K)
	}
	return nil
}

// checkFastGeometry fails unless rows x cols uses the 64-bit packed hash, the
// one geometry where CountSketch places keys as ASAPv1 fast mode does.
func checkFastGeometry(rows, cols int) error {
	if storage.HashModeForMatrix(rows, cols) != storage.MatrixHashPacked64 {
		return fmt.Errorf("countsketch: a %dx%d CountSketch does not hash in ASAPv1 fast mode: "+
			"rows*(log2(cols)+1) must be at most 64", rows, cols)
	}
	return nil
}

func heapEntries(items []common.Item) []asapv1.HeapEntry[string] {
	es := make([]asapv1.HeapEntry[string], len(items))
	for i, it := range items {
		es[i] = asapv1.HeapEntry[string]{Key: it.Key, Count: it.Count}
	}
	return es
}

// MarshalASAPv1 encodes h as an ASAPv1 CSHeap envelope, with the heap in
// descending count, then key order.
func (h *CSHeap) MarshalASAPv1() ([]byte, error) {
	if err := h.validate(); err != nil {
		return nil, err
	}
	es := heapEntries(h.Heap)
	md := asapv1.NewMetadataWriter(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	md.Uint("rows", uint64(h.Rows))
	md.Uint("cols", uint64(h.Cols))
	md.Str("counter_type", h.CounterType)
	md.Str("mode", h.Mode)
	md.Uint("k", uint64(h.K))
	md.Str("key_type", asapv1.HeapKeyTypeOf(es))
	p := asapv1.NewEncoder()
	p.Array(3)
	asapv1.EncodeInts(p, h.Counts)
	asapv1.EncodeHeapEntries(p, es)
	return asapv1.Marshal(asapv1.KindCSHeap, md, p)
}

// UnmarshalASAPv1 replaces h with the CSHeap in b. A heap with entries must
// have key_type "string".
func (h *CSHeap) UnmarshalASAPv1(b []byte) error {
	md, p, err := asapv1.Open(b, asapv1.KindCSHeap)
	if err != nil {
		return err
	}
	md.ExpectVersion(1)
	md.HashSpec(asapv1.StandardProfile(), asapv1.SeedIndexMatrix)
	rows := md.Uint32("rows")
	cols := md.Uint32("cols")
	counterType := md.Str("counter_type")
	mode := md.Str("mode")
	k := md.Uint32("k")
	keyType := md.Str("key_type")
	if err := md.Finish(); err != nil {
		return err
	}
	if err := checkDims(uint64(rows), uint64(cols)); err != nil {
		return err
	}
	if err := checkCounterType(counterType); err != nil {
		return err
	}
	if err := checkMode(mode); err != nil {
		return err
	}
	if !asapv1.IsHeapKeyType(keyType) {
		return fmt.Errorf("countsketch: CSHeap key_type %q is not a heap key type", keyType)
	}
	p.ExpectArray(3)
	n := p.Array()
	if p.Err() == nil && uint64(n) != uint64(rows)*uint64(cols) {
		return fmt.Errorf("countsketch: CSHeap counts length %d != rows*cols %d", n, uint64(rows)*uint64(cols))
	}
	counts := make([]int64, n)
	for i := range counts {
		if counterType == counterTypeI32 {
			counts[i] = int64(p.Int32())
		} else {
			counts[i] = p.Int()
		}
	}
	es := asapv1.DecodeHeapEntries[string](p, keyType)
	if err := p.Finish(); err != nil {
		return err
	}
	if uint64(len(es)) > uint64(k) {
		return fmt.Errorf("countsketch: CSHeap heap holds %d entries, more than k=%d", len(es), k)
	}
	heap := make([]common.Item, len(es))
	for i, e := range es {
		heap[i] = common.Item{Key: e.Key, Count: e.Count}
	}
	*h = CSHeap{
		Rows: int(rows), Cols: int(cols),
		CounterType: counterType, Mode: mode,
		Counts: counts, K: int(k), Heap: heap,
	}
	return nil
}

// ToCSHeap returns s's matrix as i64 cells in fast mode, with a heap of capacity
// k holding the k Space-Saving candidates of highest positive estimate. It fails
// when k < 0, a cell is not an i64, or s's geometry is not fast mode's.
func (s *CountSketch) ToCSHeap(k int) (*CSHeap, error) {
	if k < 0 {
		return nil, fmt.Errorf("countsketch: ToCSHeap: k %d is negative", k)
	}
	if err := checkFastGeometry(s.Rows, s.Cols); err != nil {
		return nil, err
	}
	counts := make([]int64, 0, s.Rows*s.Cols)
	for r := 0; r < s.Rows; r++ {
		for c, v := range s.Count[r] {
			iv, ok := integralCellDelta(v)
			if !ok {
				return nil, fmt.Errorf("countsketch: ToCSHeap: cell (%d,%d) value %v is not an i64", r, c, v)
			}
			counts = append(counts, iv)
		}
	}
	var es []asapv1.HeapEntry[string]
	if s.SS != nil {
		for _, key := range s.SS.Candidates() {
			if est := s.EstimateStringCount(key); est > 0 {
				es = append(es, asapv1.HeapEntry[string]{Key: key, Count: est})
			}
		}
	}
	asapv1.SortHeapEntries(es)
	es = es[:min(len(es), k)]
	heap := make([]common.Item, len(es))
	for i, e := range es {
		heap[i] = common.Item{Key: e.Key, Count: e.Count}
	}
	return &CSHeap{
		Rows: s.Rows, Cols: s.Cols,
		CounterType: counterTypeI64, Mode: modeFast,
		Counts: counts, K: k, Heap: heap,
	}, nil
}

// ToCountSketch returns a CountSketch holding h's cells, h's heap as its TopK,
// L2 as each row's sum of squares and an empty Space-Saving tracker. It fails
// unless h is valid, in fast mode and geometry, and float64 holds every cell.
func (h *CSHeap) ToCountSketch() (*CountSketch, error) {
	if err := h.validate(); err != nil {
		return nil, err
	}
	if h.Mode != modeFast {
		return nil, fmt.Errorf("countsketch: ToCountSketch: mode %q, CountSketch hashes in fast mode", h.Mode)
	}
	if err := checkFastGeometry(h.Rows, h.Cols); err != nil {
		return nil, err
	}
	keys := make([]string, len(h.Heap))
	for i, it := range h.Heap {
		keys[i] = it.Key
	}
	if err := asapv1.CheckDistinctHeapKeys(keys); err != nil {
		return nil, err
	}
	s, err := NewCountSketch(h.Rows, h.Cols)
	if err != nil {
		return nil, err
	}
	for r := 0; r < h.Rows; r++ {
		for c := 0; c < h.Cols; c++ {
			v := h.Counts[r*h.Cols+c]
			f := float64(v)
			if f >= 0x1p63 || int64(f) != v {
				return nil, fmt.Errorf("countsketch: ToCountSketch: cell (%d,%d) value %d is not exact in float64", r, c, v)
			}
			s.Count[r][c] = f
			s.L2[r] += f * f
		}
	}
	s.TopK = &common.TopKHeap{Heap: make([]common.Item, 0, len(h.Heap)), K: h.K}
	for _, it := range h.Heap {
		s.TopK.Insert(it.Key, it.Count)
	}
	return s, nil
}
