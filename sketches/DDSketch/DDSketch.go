package ddsketch

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/ProjectASAP/sketchlib-go/common"
	"github.com/ProjectASAP/sketchlib-go/common/storage"
)

const GrowChunk = 128

// ---------------- Index Mapping ----------------

type IndexMapping struct {
	gamma       float64
	invLogGamma float64
}

func NewIndexMapping(alpha float64) IndexMapping {
	if !(alpha > 0 && alpha < 1) {
		panic("alpha must be in (0,1)")
	}

	gamma := (1 + alpha) / (1 - alpha)

	return IndexMapping{
		gamma:       gamma,
		invLogGamma: 1 / math.Log(gamma),
	}
}

func (m IndexMapping) Equals(other IndexMapping) bool {
	return math.Abs(m.gamma-other.gamma) < 1e-15
}

func (m IndexMapping) Index(v float64) int32 {
	return int32(math.Floor(math.Log(v) * m.invLogGamma))
}

func (m IndexMapping) Value(k int32) float64 {
	return math.Pow(m.gamma, float64(k)+0.5)
}

// ---------------- Buckets ----------------

type Buckets struct {
	counts *storage.Vector1D[uint64]
	offset int32
}

func (b *Buckets) IsEmpty() bool {
	return b.counts == nil || b.counts.Len() == 0
}

func (b *Buckets) Range() (int32, int32, bool) {
	if b.IsEmpty() {
		return 0, 0, false
	}
	counts := b.counts.AsSlice()
	left := b.offset
	right := b.offset + int32(len(counts)) - 1
	return left, right, true
}

func (b *Buckets) ensure(k int32) {
	if b.IsEmpty() {
		filled, _ := storage.FilledVector1D[uint64](GrowChunk, 0)
		b.counts = filled
		b.offset = k - int32(GrowChunk/2)
		return
	}

	counts := b.counts.AsSlice()
	left, right, _ := b.Range()

	if k < left {
		needed := int(left - k)
		grow := maxInt(needed, GrowChunk)

		newCounts := make([]uint64, grow+len(counts))
		copy(newCounts[grow:], counts)
		b.counts = storage.Vector1DFromVec(newCounts)
		b.offset -= int32(grow)

	} else if k > right {
		needed := int(k - right)
		grow := maxInt(needed, GrowChunk)
		b.counts.ExtendFromSlice(make([]uint64, grow))
	}
}

func (b *Buckets) addOne(k int32) {
	if b.IsEmpty() {
		b.ensure(k)
	}
	counts := b.counts.AsMutSlice()
	idx := k - b.offset
	if idx >= 0 {
		i := int(idx)
		if i < len(counts) {
			counts[i]++
			return
		}
	}
	b.ensure(k)
	b.counts.AsMutSlice()[int(k-b.offset)]++
}

func (b Buckets) Clone() Buckets {
	if b.IsEmpty() {
		return Buckets{offset: b.offset}
	}
	cp := append([]uint64(nil), b.counts.AsSlice()...)
	return Buckets{counts: storage.Vector1DFromVec(cp), offset: b.offset}
}

// ---------------- DDSketch ----------------

// High-performance latency quantile sketch (v > 0 only)
type DDSketch struct {
	mapping IndexMapping
	store   Buckets

	count uint64
	sum   float64
	min   float64
	max   float64
}

func NewDDSketch(alpha float64) *DDSketch {
	return &DDSketch{
		mapping: NewIndexMapping(alpha),
		min:     math.Inf(1),
		max:     math.Inf(-1),
	}
}

// New mirrors the Rust constructor naming.
func New(alpha float64) *DDSketch {
	return NewDDSketch(alpha)
}

// TypeName returns the name of the sketch.
func (d *DDSketch) TypeName() string {
	return "DDSketch"
}

// Add inserts a float64 value directly.
// Strictly positive values only.
func (d *DDSketch) Add(v float64) {
	if !(v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)) {
		return // ignore invalid or non-positive
	}

	d.count++
	d.sum += v

	if v < d.min {
		d.min = v
	}
	if v > d.max {
		d.max = v
	}

	k := d.mapping.Index(v)
	d.store.addOne(k)
}

// InsertWithHash implements common.Sketch.
// It interprets the hash as a numerical value (casting uint64 to float64).
// This allows tracking distributions of integer-like values (e.g., latencies in ns).
func (d *DDSketch) InsertWithHash(hash uint64) {
	d.Add(float64(hash))
}

func (d *DDSketch) GetCount() uint64 {
	return d.count
}

func (d *DDSketch) Count() uint64 {
	return d.count
}

func (d *DDSketch) Min() (float64, bool) {
	if d.count == 0 {
		return 0, false
	}
	return d.min, true
}

func (d *DDSketch) Max() (float64, bool) {
	if d.count == 0 {
		return 0, false
	}
	return d.max, true
}

// ---------------- Safe Merge ----------------

// Merge implements common.Sketch.
func (d *DDSketch) Merge(other common.Sketch) error {
	o, ok := other.(*DDSketch)
	if !ok {
		return errors.New("cannot merge different sketch types")
	}

	if !d.mapping.Equals(o.mapping) {
		return errors.New("cannot merge sketches with different index mappings")
	}

	if o.count == 0 {
		return nil
	}
	if d.count == 0 {
		// Clone fundamental fields
		d.count = o.count
		d.sum = o.sum
		d.min = o.min
		d.max = o.max
		d.store = o.store.Clone()
		// Mapping is already equal
		return nil
	}

	d.count += o.count
	d.sum += o.sum

	if o.min < d.min {
		d.min = o.min
	}
	if o.max > d.max {
		d.max = o.max
	}

	d.mergeBuckets(&d.store, &o.store)

	return nil
}

func (d *DDSketch) mergeBuckets(a *Buckets, b *Buckets) {

	if b.IsEmpty() {
		return
	}
	if a.IsEmpty() {
		*a = b.Clone()
		return
	}

	al, ar, _ := a.Range()
	bl, br, _ := b.Range()

	newL := minInt32(al, bl)
	newR := maxInt32(ar, br)
	newLen := int(newR-newL) + 1

	merged := make([]uint64, newLen)

	for i, c := range a.counts.AsSlice() {
		k := al + int32(i)
		merged[int(k-newL)] += c
	}
	for i, c := range b.counts.AsSlice() {
		k := bl + int32(i)
		merged[int(k-newL)] += c
	}

	a.counts = storage.Vector1DFromVec(merged)
	a.offset = newL
}

// ---------------- Quantile ----------------

// GetValueAtQuantile returns the value at quantile q (0 <= q <= 1).
func (d *DDSketch) GetValueAtQuantile(q float64) (float64, bool) {
	if d.count == 0 || q < 0 || q > 1 {
		return 0, false
	}

	if q == 0 {
		return d.min, true
	}
	if q == 1 {
		return d.max, true
	}

	rank := uint64(math.Ceil(q * float64(d.count)))

	var seen uint64

	if d.store.counts == nil {
		return d.max, true
	}
	for i, c := range d.store.counts.AsSlice() {
		if c == 0 {
			continue
		}
		seen += c
		if seen >= rank {
			k := d.store.offset + int32(i)
			v := d.mapping.Value(k)

			// clamp for safety
			if v < d.min {
				v = d.min
			}
			if v > d.max {
				v = d.max
			}
			return v, true
		}
	}

	return d.max, true
}

// QueryWithHash implements common.Sketch.
// For QueryQuantile: interprets 'hash' as the float64 bits of the quantile 'q' (0.0 to 1.0).
// For QueryFrequency: returns the total count in the sketch.
func (d *DDSketch) QueryWithHash(q common.QueryType, hash uint64) (float64, error) {
	switch q {
	case common.QueryQuantile:
		// Reinterpret bits as float64
		qVal := math.Float64frombits(hash)
		val, ok := d.GetValueAtQuantile(qVal)
		if !ok {
			return 0, errors.New("invalid quantile or empty sketch")
		}
		return val, nil

	case common.QueryFrequency:
		return float64(d.count), nil

	default:
		return 0, common.ErrUnsupportedQuery
	}
}

func (d *DDSketch) Clone() *DDSketch {
	return &DDSketch{
		mapping: d.mapping,
		store:   d.store.Clone(),
		count:   d.count,
		sum:     d.sum,
		min:     d.min,
		max:     d.max,
	}
}

// ---------------- OctoSketch bucket-level accessors ----------------
//
// These methods expose the internal log-mapping and per-bucket mutation so
// that the DDSketchOcto adapter (sketch_framework/OctoSketch) can delegate
// all mapping and estimation logic here instead of duplicating it.

// BucketIndex maps a positive float64 value to its DDSketch bucket index k.
// Equivalent to floor(log(v) * invLogGamma). Pure function, no state change.
func (d *DDSketch) BucketIndex(v float64) int32 {
	return d.mapping.Index(v)
}

// BucketValue returns the representative float64 for bucket index k: γ^(k+0.5).
// Pure function, no state change.
func (d *DDSketch) BucketValue(k int32) float64 {
	return d.mapping.Value(k)
}

// BucketCount returns the current count in bucket k, or 0 if k is outside range.
func (d *DDSketch) BucketCount(k int32) uint64 {
	if d.store.IsEmpty() {
		return 0
	}
	idx := int(k - d.store.offset)
	counts := d.store.counts.AsSlice()
	if idx < 0 || idx >= len(counts) {
		return 0
	}
	return counts[idx]
}

// AddToBucket increments bucket k's count by delta and adjusts d.count.
// Grows the bucket store if k is outside the current range.
// Also updates d.min and d.max from the bucket representative so that
// GetValueAtQuantile's safety-clamp remains meaningful.
func (d *DDSketch) AddToBucket(k int32, delta uint64) {
	if delta == 0 {
		return
	}
	d.store.ensure(k)
	d.store.counts.AsMutSlice()[int(k-d.store.offset)] += delta
	d.count += delta
	rep := d.mapping.Value(k)
	if rep < d.min {
		d.min = rep
	}
	if rep > d.max {
		d.max = rep
	}
}

// ResetBucket zeroes bucket k and decrements d.count by the bucket's current count.
// Used by DDSketchOcto.ResetCell to drain a worker-local bucket after emitting a delta.
func (d *DDSketch) ResetBucket(k int32) {
	if d.store.IsEmpty() {
		return
	}
	idx := int(k - d.store.offset)
	counts := d.store.counts.AsMutSlice()
	if idx < 0 || idx >= len(counts) {
		return
	}
	n := counts[idx]
	if n == 0 {
		return
	}
	counts[idx] = 0
	if d.count >= n {
		d.count -= n
	} else {
		d.count = 0
	}
}

// DrainBuckets calls f(k, count) for every non-zero bucket and then zeroes the
// entire store (setting d.count = 0). Used by DDSketchOcto.Flush to ship all
// worker-local sub-τ residuals to the aggregator at end-of-window.
func (d *DDSketch) DrainBuckets(f func(k int32, count uint64)) {
	if d.store.IsEmpty() {
		return
	}
	counts := d.store.counts.AsMutSlice()
	for i, c := range counts {
		if c > 0 {
			f(d.store.offset+int32(i), c)
			counts[i] = 0
		}
	}
	d.count = 0
}

// EachBucket calls f(k, count) for every non-zero bucket without modifying
// the sketch. Used by DDSketchOcto.LocalBuckets for test inspection.
func (d *DDSketch) EachBucket(f func(k int32, count uint64)) {
	if d.store.IsEmpty() {
		return
	}
	for i, c := range d.store.counts.AsSlice() {
		if c > 0 {
			f(d.store.offset+int32(i), c)
		}
	}
}

// ── Input helpers ──────────────────────────────────────────────────────────────

// valueFromInput recovers the float64 inserted via common.FromF64(v).
// Returns (0, false) for non-positive, NaN, Inf, or too-short Bytes.
func (d *DDSketch) valueFromInput(input *common.SketchInput) (float64, bool) {
	if input == nil {
		return 0, false
	}
	if input.HasFloat64 {
		v := input.Float64
		return v, v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
	}
	if len(input.Bytes) < 8 {
		return 0, false
	}
	v := math.Float64frombits(binary.NativeEndian.Uint64(input.Bytes[:8]))
	return v, v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// phiFromInput recovers a quantile phi ∈ [0,1] from input.Bytes.
func (d *DDSketch) phiFromInput(input *common.SketchInput) (float64, bool) {
	if input == nil {
		return 0, false
	}
	if input.HasFloat64 {
		phi := input.Float64
		return phi, phi >= 0 && phi <= 1 && !math.IsNaN(phi)
	}
	if len(input.Bytes) < 8 {
		return 0, false
	}
	phi := math.Float64frombits(binary.NativeEndian.Uint64(input.Bytes[:8]))
	return phi, phi >= 0 && phi <= 1 && !math.IsNaN(phi)
}

// ── CellSketch interface ───────────────────────────────────────────────────────

// NumRows returns 1. DDSketch is 1-dimensional.
func (d *DDSketch) NumRows() int { return 1 }

// ColForRow returns the DDSketch bucket index for the value encoded in input.
// Non-positive, NaN, or Inf values return math.MinInt32 (sentinel).
func (d *DDSketch) ColForRow(input *common.SketchInput, _ int) int {
	if input == nil {
		return math.MinInt32
	}
	v, ok := d.valueFromInput(input)
	if !ok {
		return math.MinInt32
	}
	return int(d.BucketIndex(v))
}

// UpdateCell increments the local count for bucket col by 1.
// Returns (0, false) for the sentinel bucket; (newVal, true) otherwise.
func (d *DDSketch) UpdateCell(_, col int, _ *common.SketchInput) (float64, bool) {
	if col == math.MinInt32 {
		return 0, false
	}
	d.AddToBucket(int32(col), 1)
	return float64(d.BucketCount(int32(col))), true
}

// ProcessInput is an optimized OctoSketch worker fast path that decodes the
// float payload once, derives the bucket index once, and emits directly.
func (d *DDSketch) ProcessInput(input *common.SketchInput, tau float64, emit func(common.DeltaUpdate)) {
	if input == nil {
		return
	}
	v, ok := d.valueFromInput(input)
	if !ok {
		return
	}
	col := int32(d.BucketIndex(v))
	d.AddToBucket(col, 1)
	count := d.BucketCount(col)
	if float64(count) >= tau {
		emit(common.DeltaUpdate{Row: 0, Col: int(col), Value: float64(count)})
		d.ResetBucket(col)
	}
}

// ShouldEmit returns true when the local bucket count newVal reaches threshold τ.
func (d *DDSketch) ShouldEmit(newVal, tau float64) bool { return newVal >= tau }

// BuildDelta constructs the DeltaUpdate for bucket col.
func (d *DDSketch) BuildDelta(_, col int, _ *common.SketchInput) common.DeltaUpdate {
	return common.DeltaUpdate{
		Row:   0,
		Col:   col,
		Value: float64(d.BucketCount(int32(col))),
	}
}

// ResetCell zeroes the local count for bucket col.
func (d *DDSketch) ResetCell(_, col int) { d.ResetBucket(int32(col)) }

// MergeDelta adds delta.Value counts to the aggregator's bucket delta.Col.
func (d *DDSketch) MergeDelta(delta common.DeltaUpdate) {
	if delta.Col == math.MinInt32 || delta.Value <= 0 {
		return
	}
	d.AddToBucket(int32(delta.Col), uint64(delta.Value))
}

// Estimate returns the quantile estimate for the phi encoded in input.
// Returns 0 when the sketch is empty or input is nil.
func (d *DDSketch) Estimate(input *common.SketchInput) float64 {
	if input == nil || d.count == 0 {
		return 0
	}
	phi, ok := d.phiFromInput(input)
	if !ok {
		return 0
	}
	val, ok := d.GetValueAtQuantile(phi)
	if !ok {
		return 0
	}
	return val
}

// Flush drains all non-zero local buckets to the delta channel.
func (d *DDSketch) Flush(emit func(common.DeltaUpdate)) {
	d.DrainBuckets(func(k int32, count uint64) {
		emit(common.DeltaUpdate{Row: 0, Col: int(k), Value: float64(count)})
	})
}

// Insert populates the underlying sketch from a SketchInput.
func (d *DDSketch) Insert(input *common.SketchInput) {
	if input == nil {
		return
	}
	v, ok := d.valueFromInput(input)
	if !ok {
		return
	}
	d.Add(v)
}

// LocalBuckets returns a snapshot of all non-zero bucket counts.
// Used by tests to verify the per-bucket delay bound.
func (d *DDSketch) LocalBuckets() map[int]float64 {
	snap := make(map[int]float64)
	d.EachBucket(func(k int32, count uint64) {
		snap[int(k)] = float64(count)
	})
	return snap
}

// TotalCount returns the total number of insertions reflected in this instance.
func (d *DDSketch) TotalCount() float64 { return float64(d.count) }

// ---------------- Utils ----------------

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

type ddSketchSnapshot struct {
	MappingGamma       float64
	MappingInvLogGamma float64
	StoreCounts        []uint64
	StoreOffset        int32
	Count              uint64
	Sum                float64
	Min                float64
	Max                float64
}

// SerializeToBytes serializes DDSketch into bytes.
func (d *DDSketch) SerializeToBytes() ([]byte, error) {
	storeCounts := []uint64(nil)
	if d.store.counts != nil {
		storeCounts = append([]uint64(nil), d.store.counts.AsSlice()...)
	}
	return common.EncodeToBytes(ddSketchSnapshot{
		MappingGamma:       d.mapping.gamma,
		MappingInvLogGamma: d.mapping.invLogGamma,
		StoreCounts:        storeCounts,
		StoreOffset:        d.store.offset,
		Count:              d.count,
		Sum:                d.sum,
		Min:                d.min,
		Max:                d.max,
	})
}

// DeserializeDDSketchFromBytes restores DDSketch from serialized bytes.
func DeserializeDDSketchFromBytes(data []byte) (*DDSketch, error) {
	var snap ddSketchSnapshot
	if err := common.DecodeFromBytes(data, &snap); err != nil {
		return nil, err
	}
	if snap.MappingGamma <= 1 || snap.MappingInvLogGamma <= 0 {
		return nil, errors.New("invalid snapshot mapping")
	}

	return &DDSketch{
		mapping: IndexMapping{
			gamma:       snap.MappingGamma,
			invLogGamma: snap.MappingInvLogGamma,
		},
		store: Buckets{
			counts: storage.Vector1DFromVec(snap.StoreCounts),
			offset: snap.StoreOffset,
		},
		count: snap.Count,
		sum:   snap.Sum,
		min:   snap.Min,
		max:   snap.Max,
	}, nil
}
