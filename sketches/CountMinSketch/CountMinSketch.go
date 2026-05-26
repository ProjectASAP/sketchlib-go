package countminsketch

import (
	"errors"
	"math"
	"math/bits"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

// CountMinSketch with single-hash multi-row derivation.
// Hashing MUST be done externally.
type CountMinSketch struct {
	Rows int
	Cols int

	countStore *storage.FlatVector2D
	sumStore   *storage.FlatVector2D
	sum2Store  *storage.FlatVector2D

	Count [][]float64
	Sum   [][]float64
	Sum2  [][]float64

	L1 []float64
	L2 []float64

	bitsPerRow uint
	mask       uint64

	// sampler implements optional NitroSketch geometric skip-sampling. When nil
	// (the default) every update is admitted and the sketch is byte-identical to
	// an unsampled one. When set with p<1, updates are admitted with probability
	// p and the RAW sampled counts are stored; the consumer rescales ×1/p at
	// query. The probability rides on the SketchEnvelope (see SerializePortable),
	// never inside CountMinState, so downstream literal constructors are unaffected.
	sampler *common.GeometricSampler
}

func hashLayoutForCols(cols int) (uint, uint64) {
	width := 1
	for width < cols {
		width <<= 1
	}
	if width <= 1 {
		return 0, 0
	}
	return uint(bits.TrailingZeros(uint(width))), uint64(width - 1)
}

func (s *CountMinSketch) rehydrateStorage() error {
	if s.Rows <= 0 || s.Cols <= 0 {
		return errors.New("invalid snapshot dimensions")
	}
	if len(s.Count) != s.Rows || len(s.Sum) != s.Rows || len(s.Sum2) != s.Rows {
		return errors.New("invalid snapshot matrix row count")
	}
	for r := 0; r < s.Rows; r++ {
		if len(s.Count[r]) != s.Cols || len(s.Sum[r]) != s.Cols || len(s.Sum2[r]) != s.Cols {
			return errors.New("invalid snapshot matrix col count")
		}
	}
	if len(s.L1) != s.Rows || len(s.L2) != s.Rows {
		return errors.New("invalid snapshot l1/l2 size")
	}

	countStore, err := storage.NewFlatVector2DFrom2D(s.Count)
	if err != nil {
		return err
	}
	sumStore, err := storage.NewFlatVector2DFrom2D(s.Sum)
	if err != nil {
		return err
	}
	sum2Store, err := storage.NewFlatVector2DFrom2D(s.Sum2)
	if err != nil {
		return err
	}

	s.countStore = countStore
	s.sumStore = sumStore
	s.sum2Store = sum2Store
	s.Count = countStore.As2D()
	s.Sum = sumStore.As2D()
	s.Sum2 = sum2Store.As2D()
	s.bitsPerRow, s.mask = hashLayoutForCols(s.Cols)
	return nil
}

/* sketch configurations */
const (
	DefaultRowNum    = 3
	DefaultColNum    = 4096
	QuickStartRowNum = 5
	QuickStartColNum = 2048

	CM_ROW_NO = 5
	CM_COL_NO = 2048
)

// NewCountMinSketch creates the float64-counter Count-Min sketch.
func NewCountMinSketch(row, col int) (*CountMinSketch, error) {
	if row <= 0 || col <= 0 {
		return nil, errors.New("row and col must be positive")
	}

	countStore, err := storage.NewFlatVector2D(row, col)
	if err != nil {
		return nil, err
	}
	sumStore, err := storage.NewFlatVector2D(row, col)
	if err != nil {
		return nil, err
	}
	sum2Store, err := storage.NewFlatVector2D(row, col)
	if err != nil {
		return nil, err
	}

	bitsPerRow, mask := hashLayoutForCols(col)

	return &CountMinSketch{
		Rows:       row,
		Cols:       col,
		countStore: countStore,
		sumStore:   sumStore,
		sum2Store:  sum2Store,
		Count:      countStore.As2D(),
		Sum:        sumStore.As2D(),
		Sum2:       sum2Store.As2D(),
		L1:         make([]float64, row),
		L2:         make([]float64, row),
		bitsPerRow: bitsPerRow,
		mask:       mask,
	}, nil
}

// New returns a float64-counter Count-Min sketch with Rust default dimensions.
func New() (*CountMinSketch, error) {
	return NewCountMinSketch(DefaultRowNum, DefaultColNum)
}

// WithDimensions mirrors Rust constructor naming for the float64 variant.
func WithDimensions(rows, cols int) (*CountMinSketch, error) {
	return NewCountMinSketch(rows, cols)
}

// WithSampleP enables NitroSketch geometric skip-sampling at probability p in
// (0,1]. With p>=1 sampling is disabled (exact, the default) and the sketch is
// byte-identical to an unsampled one. The seed makes the admitted subset
// reproducible. Counter writes are cut to ~p× per item; the RAW sampled counts
// are stored and the probability is stamped on the SketchEnvelope so the
// consumer rescales frequency estimates ×1/p at query time.
//
// Returns the receiver for fluent construction:
//
//	cm, _ := NewCountMinSketch(3, 512)
//	cm.WithSampleP(0.1, seed)
func (s *CountMinSketch) WithSampleP(p float64, seed int64) *CountMinSketch {
	if p >= 1.0 {
		s.sampler = nil
		return s
	}
	s.sampler = common.NewGeometricSampler(p, seed)
	return s
}

// SampleP returns the configured sampling probability (1.0 when sampling is
// disabled).
func (s *CountMinSketch) SampleP() float64 {
	if s.sampler == nil {
		return 1.0
	}
	return s.sampler.P()
}

// wireSampleP returns the value to stamp on the SketchEnvelope.sample_p field.
// When sampling is disabled it returns 0.0 (the proto3 default) so the encoded
// envelope is BYTE-IDENTICAL to the pre-sampling format — proto3 omits
// default-valued scalars, and the consumer's dual-read treats an unset/0.0
// sample_p as 1.0. A sampled sketch (p<1) emits its actual probability.
func (s *CountMinSketch) wireSampleP() float64 {
	if s.sampler == nil {
		return 0.0
	}
	return s.sampler.P()
}

// admit reports whether the next update should be applied. Always true when no
// sampler is configured (exact, no RNG cost).
func (s *CountMinSketch) admit() bool {
	if s.sampler == nil {
		return true
	}
	return s.sampler.Admit()
}

func (s *CountMinSketch) RowCount() int { return s.Rows }
func (s *CountMinSketch) ColCount() int { return s.Cols }

func (s *CountMinSketch) AsStorage() *storage.FlatVector2D {
	return s.countStore
}

func (s *CountMinSketch) AsStorageMut() *storage.FlatVector2D {
	return s.countStore
}

// deriveIndex computes column index from base hash (NO hashing, NO modulo)
func (s *CountMinSketch) deriveIndex(hash uint64, row int) int {
	shift := uint(row) * s.bitsPerRow
	idx := int((hash >> shift) & s.mask)
	if idx >= s.Cols {
		return idx % s.Cols
	}
	return idx
}

// ================= INSERT =================

func (s *CountMinSketch) InsertWithHash(hash uint64) {
	if !s.admit() {
		return
	}
	shift := uint(0)
	for r := 0; r < s.Rows; r++ {
		c := int((hash >> shift) & s.mask)
		if c >= s.Cols {
			c %= s.Cols
		}
		shift += s.bitsPerRow

		countRow := s.countStore.RowSlice(r)
		sumRow := s.sumStore.RowSlice(r)
		sum2Row := s.sum2Store.RowSlice(r)

		prev := countRow[c]
		curr := prev + 1.0
		countRow[c] = curr
		sumRow[c] += 1.0
		sum2Row[c] += 1.0

		s.L1[r] += 1.0
		s.L2[r] += curr*curr - prev*prev
	}
}

// Update is the canonical high-level update method, mirroring Rust's
// unified API (`update`). It populates the sketch from a SketchInput.
func (s *CountMinSketch) Update(input *common.SketchInput) {
	if input == nil {
		return
	}
	if !s.admit() {
		return
	}
	s.insertMatrixHash(storage.BuildMatrixHashFromInput(input, s.Rows, s.Cols), 1)
}

// OctoUpdate is an alias for Update kept for the OctoSketch framework
// (which wires sketches through a uniform "Octo" naming convention).
func (s *CountMinSketch) OctoUpdate(input *common.SketchInput) { s.Update(input) }

func (s *CountMinSketch) UpdateWeight(input *common.SketchInput, many float64) {
	if input == nil || many == 0 {
		return
	}
	if !s.admit() {
		return
	}
	s.insertMatrixHash(storage.BuildMatrixHashFromInput(input, s.Rows, s.Cols), many)
}

func (s *CountMinSketch) BulkUpdate(inputs []*common.SketchInput) {
	for _, input := range inputs {
		s.Update(input)
	}
}

func (s *CountMinSketch) BulkUpdateWeight(values []struct {
	Input *common.SketchInput
	Many  float64
}) {
	for _, value := range values {
		s.UpdateWeight(value.Input, value.Many)
	}
}

// InsertWithHashFast is an optimized alias for benchmark fast-path usage.
func (s *CountMinSketch) InsertWithHashFast(hash uint64) {
	s.InsertWithHash(hash)
}

func (s *CountMinSketch) FastInsertWithHashValue(hash uint64) {
	s.InsertWithHash(hash)
}

func (s *CountMinSketch) FastInsertWeightWithHashValue(hash uint64, many float64) {
	if many == 0 {
		return
	}
	if !s.admit() {
		return
	}
	shift := uint(0)
	for r := 0; r < s.Rows; r++ {
		c := int((hash >> shift) & s.mask)
		if c >= s.Cols {
			c %= s.Cols
		}
		shift += s.bitsPerRow

		countRow := s.countStore.RowSlice(r)
		sumRow := s.sumStore.RowSlice(r)
		sum2Row := s.sum2Store.RowSlice(r)

		prev := countRow[c]
		curr := prev + many
		countRow[c] = curr
		sumRow[c] += many
		sum2Row[c] += many

		s.L1[r] += many
		s.L2[r] += curr*curr - prev*prev
	}
}

func (s *CountMinSketch) insertMatrixHash(hashed storage.MatrixHashType, many float64) {
	for r := 0; r < s.Rows; r++ {
		c := int(hashed.RowHash(r, s.bitsPerRow, s.mask))
		if c >= s.Cols {
			c %= s.Cols
		}

		countRow := s.countStore.RowSlice(r)
		sumRow := s.sumStore.RowSlice(r)
		sum2Row := s.sum2Store.RowSlice(r)

		prev := countRow[c]
		curr := prev + many
		countRow[c] = curr
		sumRow[c] += many
		sum2Row[c] += many

		s.L1[r] += many
		s.L2[r] += curr*curr - prev*prev
	}
}

func (s *CountMinSketch) queryFrequencyFast(hash uint64) float64 {
	res := math.MaxFloat64
	shift := uint(0)
	for r := 0; r < s.Rows; r++ {
		c := int((hash >> shift) & s.mask)
		if c >= s.Cols {
			c %= s.Cols
		}
		shift += s.bitsPerRow
		v := s.countStore.RowSlice(r)[c]
		if v < res {
			res = v
		}
	}
	return res
}

func (s *CountMinSketch) Estimate(input *common.SketchInput) float64 {
	if input == nil {
		return 0
	}
	return s.estimateMatrixHash(storage.BuildMatrixHashFromInput(input, s.Rows, s.Cols))
}

// OctoEstimate satisfies the octosketch.OctoSketch interface.
func (s *CountMinSketch) OctoEstimate(input *common.SketchInput) float64 { return s.Estimate(input) }

func (s *CountMinSketch) FastEstimateWithHash(hash uint64) float64 {
	return s.queryFrequencyFast(hash)
}

func (s *CountMinSketch) estimateMatrixHash(hashed storage.MatrixHashType) float64 {
	res := math.MaxFloat64
	for r := 0; r < s.Rows; r++ {
		c := int(hashed.RowHash(r, s.bitsPerRow, s.mask))
		if c >= s.Cols {
			c %= s.Cols
		}
		v := s.countStore.RowSlice(r)[c]
		if v < res {
			res = v
		}
	}
	if res == math.MaxFloat64 {
		return 0
	}
	return res
}

// ================= QUERY =================

func (s *CountMinSketch) QueryWithHash(
	q common.QueryType,
	hash uint64,
) (float64, error) {

	switch q {

	case common.QueryFrequency:
		return s.queryFrequencyFast(hash), nil

	case common.QuerySum:
		res := math.MaxFloat64
		shift := uint(0)
		for r := 0; r < s.Rows; r++ {
			c := int((hash >> shift) & s.mask)
			if c >= s.Cols {
				c %= s.Cols
			}
			shift += s.bitsPerRow
			v := math.Abs(s.sumStore.RowSlice(r)[c])
			if v < res {
				res = v
			}
		}
		return res, nil

	case common.QuerySum2:
		res := math.MaxFloat64
		shift := uint(0)
		for r := 0; r < s.Rows; r++ {
			c := int((hash >> shift) & s.mask)
			if c >= s.Cols {
				c %= s.Cols
			}
			shift += s.bitsPerRow
			v := s.sum2Store.RowSlice(r)[c]
			if v < res {
				res = v
			}
		}
		return res, nil

	default:
		return 0, common.ErrUnsupportedQuery
	}
}

// ================= METRICS =================

func (s *CountMinSketch) CM_L1() float64 {
	res := math.MaxFloat64
	for i := 0; i < s.Rows; i++ {
		if s.L1[i] < res {
			res = s.L1[i]
		}
	}
	if res == math.MaxFloat64 {
		return 0
	}
	return res
}

func (s *CountMinSketch) CM_L2() float64 {
	res := math.MaxFloat64
	for i := 0; i < s.Rows; i++ {
		if s.L2[i] < res {
			res = s.L2[i]
		}
	}
	if res == math.MaxFloat64 {
		return 0
	}
	return math.Sqrt(res)
}

// Reset clears all counters and norms, returning the sketch to its zero state.
func (s *CountMinSketch) Reset() {
	for i := range s.Count {
		clear(s.Count[i])
		clear(s.Sum[i])
		clear(s.Sum2[i])
	}
	clear(s.L1)
	clear(s.L2)
}

func (s *CountMinSketch) TypeName() string {
	return "countmin"
}

// ================= MERGE =================

func (s *CountMinSketch) Merge(other common.Sketch) error {
	o, ok := other.(*CountMinSketch)
	if !ok {
		return errors.New("cannot merge: incompatible sketch type")
	}
	if s.Rows != o.Rows || s.Cols != o.Cols {
		return errors.New("cannot merge: dimension mismatch")
	}

	for r := 0; r < s.Rows; r++ {
		s.L1[r] += o.L1[r]
		s.L2[r] += o.L2[r]
		sCountRow := s.countStore.RowSlice(r)
		sSumRow := s.sumStore.RowSlice(r)
		sSum2Row := s.sum2Store.RowSlice(r)
		oCountRow := o.countStore.RowSlice(r)
		oSumRow := o.sumStore.RowSlice(r)
		oSum2Row := o.sum2Store.RowSlice(r)
		for c := 0; c < s.Cols; c++ {
			sCountRow[c] += oCountRow[c]
			sSumRow[c] += oSumRow[c]
			sSum2Row[c] += oSum2Row[c]
		}
	}
	return nil
}

// ── OctoSketch cell-level accessors ──────────────────────────────────────────
//
// These thin methods expose per-cell operations on the internal countStore so
// that the CountMinOcto adapter (sketch_framework/OctoSketch) can delegate all
// storage and hash logic here instead of duplicating it.
//
// They operate only on countStore; L1/L2 norms and sumStore/sum2Store are
// whole-stream statistics that are irrelevant to the per-cell OctoSketch loop.

// ColForRow derives the column index for row r from input's pre-computed hash,
// using the same bit-slicing as InsertWithHash. Pure: same input → same col.
func (s *CountMinSketch) ColForRow(input *common.SketchInput, row int) int {
	return s.deriveIndex(input.Hash, row)
}

// GetCell returns the current count at countStore[row][col].
func (s *CountMinSketch) GetCell(row, col int) float64 {
	return s.countStore.RowSlice(row)[col]
}

// IncrCell increments countStore[row][col] by delta and returns the new value.
// L1/L2 norms are NOT updated; use Insert for whole-stream accounting.
func (s *CountMinSketch) IncrCell(row, col int, delta float64) float64 {
	row_ := s.countStore.RowSlice(row)
	row_[col] += delta
	return row_[col]
}

// SetCell writes countStore[row][col] = val directly.
func (s *CountMinSketch) SetCell(row, col int, val float64) {
	s.countStore.RowSlice(row)[col] = val
}

// ForEachNonZeroCell calls fn(row, col, val) for every cell where val > 0.
// Iteration order is unspecified. Used by CountMinOcto.Flush.
func (s *CountMinSketch) ForEachNonZeroCell(fn func(row, col int, val float64)) {
	for r := range s.Rows {
		rowSlice := s.countStore.RowSlice(r)
		for c := range s.Cols {
			if v := rowSlice[c]; v > 0 {
				fn(r, c, v)
			}
		}
	}
}

// ── CellSketch interface ───────────────────────────────────────────────────────

// NumRows returns the depth of the sketch matrix.
func (s *CountMinSketch) NumRows() int { return s.Rows }

// UpdateCell increments count[row][col] by 1. Always returns changed=true.
func (s *CountMinSketch) UpdateCell(row, col int, _ *common.SketchInput) (float64, bool) {
	return s.IncrCell(row, col, 1), true
}

// ProcessInput is an optimized OctoSketch worker fast path that slices all row
// indices from the precomputed hash once and emits deltas without interface churn.
func (s *CountMinSketch) ProcessInput(input *common.SketchInput, tau float64, emit func(common.DeltaUpdate)) {
	if input == nil {
		return
	}
	hash := input.Hash
	shift := uint(0)
	for row := 0; row < s.Rows; row++ {
		col := int((hash >> shift) & s.mask)
		if col >= s.Cols {
			col %= s.Cols
		}
		shift += s.bitsPerRow

		newVal := s.IncrCell(row, col, 1)
		if newVal >= tau {
			emit(common.DeltaUpdate{Row: row, Col: col, Value: newVal})
			s.SetCell(row, col, 0)
		}
	}
}

// ShouldEmit returns true when newVal >= τ (CMS no-underestimate threshold).
func (s *CountMinSketch) ShouldEmit(newVal, tau float64) bool { return newVal >= tau }

// BuildDelta constructs the DeltaUpdate to emit for (row, col).
func (s *CountMinSketch) BuildDelta(row, col int, input *common.SketchInput) common.DeltaUpdate {
	return common.DeltaUpdate{
		Row:   row,
		Col:   col,
		Value: s.GetCell(row, col),
		Key:   input.Bytes,
	}
}

// ResetCell zeros count[row][col] after a delta has been emitted.
func (s *CountMinSketch) ResetCell(row, col int) { s.SetCell(row, col, 0) }

// MergeDelta adds delta.Value to the global counter at (delta.Row, delta.Col).
// Out-of-bounds indices are silently dropped.
func (s *CountMinSketch) MergeDelta(delta common.DeltaUpdate) {
	if delta.Row < 0 || delta.Row >= s.Rows ||
		delta.Col < 0 || delta.Col >= s.Cols {
		return
	}
	s.IncrCell(delta.Row, delta.Col, delta.Value)
}

// Flush emits every non-zero cell and resets it to zero.
func (s *CountMinSketch) Flush(emit func(common.DeltaUpdate)) {
	s.ForEachNonZeroCell(func(row, col int, val float64) {
		emit(common.DeltaUpdate{Row: row, Col: col, Value: val})
		s.SetCell(row, col, 0)
	})
}

// SerializeToBytes serializes CountMinSketch into bytes.
func (s *CountMinSketch) SerializeToBytes() ([]byte, error) {
	return common.EncodeToBytes(s)
}

// DeserializeCountMinSketchFromBytes restores CountMinSketch from serialized bytes.
func DeserializeCountMinSketchFromBytes(data []byte) (*CountMinSketch, error) {
	var s CountMinSketch
	if err := common.DecodeFromBytes(data, &s); err != nil {
		return nil, err
	}
	if err := s.rehydrateStorage(); err != nil {
		return nil, err
	}
	return &s, nil
}
