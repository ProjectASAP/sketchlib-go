package nitrosketch

import (
	"fmt"
	"math"
	"math/rand"

	"github.com/ProjectASAP/sketchlib-go/common"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
)

const precomputedSampleSize = 0x10000

// float64Epsilon is the machine epsilon for float64, equivalent to f64::EPSILON in Rust.
const float64Epsilon = 2.220446049250313e-16

var precomputedSampleRate1Percent = buildPrecomputedSampleRate1Percent()

type nitroTarget[T any] interface {
	Rows() int
	UpdateRow(row int, hashed common.Hash128, delta uint64)
	Merge(other T) error
	EstimateMedian(input *common.SketchInput) float64
}

type NitroBatch[T nitroTarget[T]] struct {
	samplingRate   float64
	ToSkip         int
	invLnOneMinusP float64
	Delta          uint64
	generator      *rand.Rand
	idx            int
	mask           int
	target         T
}

func WithTarget[T nitroTarget[T]](rate float64, target T) *NitroBatch[T] {
	if math.IsNaN(rate) || rate <= 0.0 || rate > 1.0 {
		panic("sample_rate must be within (0.0, 1.0]")
	}
	invLn := 0.0
	if math.Abs(rate-1.0) > float64Epsilon {
		invLn = 1.0 / math.Log(1.0-rate)
	}
	n := &NitroBatch[T]{
		samplingRate:   rate,
		ToSkip:         0,
		invLnOneMinusP: invLn,
		Delta:          0,
		generator:      rand.New(rand.NewSource(rand.Int63())),
		idx:            0,
		mask:           precomputedSampleSize,
		target:         target,
	}
	n.Delta = n.ScaledIncrement(1)
	return n
}

func (n *NitroBatch[T]) Target() T {
	return n.target
}

func (n *NitroBatch[T]) SamplingRate() float64 {
	return n.samplingRate
}

func (n *NitroBatch[T]) DrawGeometric() {
	if n.isFullSampling() {
		n.ToSkip = 0
		return
	}
	var k float64
	for {
		k = n.generator.Float64()
		if k != 0.0 && k != 1.0 {
			break
		}
	}
	n.ToSkip = int(math.Ceil(math.Log(1.0-k) * n.invLnOneMinusP))
	n.idx = (n.idx + 1) & n.mask
}

func (n *NitroBatch[T]) ReduceToSkip() {
	n.ToSkip--
}

func (n *NitroBatch[T]) ReduceToSkipByCount(c int) {
	n.ToSkip -= c
}

func (n *NitroBatch[T]) ScaledIncrement(weight uint64) uint64 {
	if n.isFullSampling() {
		return weight
	}
	return uint64(math.Ceil(float64(weight) / n.samplingRate))
}

func (n *NitroBatch[T]) GetCtx() (idx int, invLnOneMinusP float64, toSkip int, mask int) {
	return n.idx, n.invLnOneMinusP, n.ToSkip, n.mask
}

func (n *NitroBatch[T]) CommitCtx(idx, toSkip int) {
	n.idx = idx
	n.ToSkip = toSkip
}

func (n *NitroBatch[T]) Insert(inputs []*common.SketchInput) {
	rows := n.target.Rows()
	n.DrawGeometric()
	position := n.ToSkip
	for position < len(inputs) {
		input := inputs[position]
		if input != nil {
			rowToUpdate := position % rows
			hashed := common.Hash128It(0, input.Bytes)
			n.target.UpdateRow(rowToUpdate, hashed, n.Delta)
		}
		n.DrawGeometric()
		position += n.ToSkip + 1
	}
}

func (n *NitroBatch[T]) InsertU64(values []uint64) {
	inputs := make([]*common.SketchInput, len(values))
	for i, value := range values {
		inputs[i] = common.FromU64(value)
	}
	n.Insert(inputs)
}

func (n *NitroBatch[T]) InsertCachedStep(inputs []*common.SketchInput) {
	rows := n.target.Rows()
	n.ToSkip = int(math.Ceil(precomputedSampleRate1Percent[n.idx]))
	n.idx = (n.idx + 1) & n.mask
	position := n.ToSkip
	for position < len(inputs) {
		input := inputs[position]
		if input != nil {
			rowToUpdate := position % rows
			hashed := common.Hash128It(0, input.Bytes)
			n.target.UpdateRow(rowToUpdate, hashed, n.Delta)
		}
		n.ToSkip = int(math.Ceil(precomputedSampleRate1Percent[n.idx]))
		n.idx = (n.idx + 1) & n.mask
		position += n.ToSkip + 1
	}
}

func (n *NitroBatch[T]) Merge(other *NitroBatch[T]) error {
	if math.Abs(n.samplingRate-other.samplingRate) > 0.0 {
		return fmt.Errorf("nitro merge requires matching sampling rates")
	}
	return n.target.Merge(other.target)
}

func (n *NitroBatch[T]) EstimateMedian(input *common.SketchInput) float64 {
	return n.target.EstimateMedian(input)
}

func (n *NitroBatch[T]) isFullSampling() bool {
	return math.Abs(n.samplingRate-1.0) <= float64Epsilon
}

type CountMinTarget struct {
	Sketch *countminsketch.CountMinSketch
}

func NewCountMinBatch(rate float64, rows, cols int) (*NitroBatch[*CountMinTarget], error) {
	sketch, err := countminsketch.WithDimensions(rows, cols)
	if err != nil {
		return nil, err
	}
	return WithTarget(rate, &CountMinTarget{Sketch: sketch}), nil
}

func (t *CountMinTarget) Rows() int {
	return t.Sketch.Rows
}

func (t *CountMinTarget) UpdateRow(row int, hashed common.Hash128, delta uint64) {
	col := colForHash128(hashed, row, t.Sketch.AsStorage().MaskBits(), t.Sketch.AsStorage().Mask(), t.Sketch.Cols)
	countRow := t.Sketch.Count[row]
	sumRow := t.Sketch.Sum[row]
	sum2Row := t.Sketch.Sum2[row]
	prev := countRow[col]
	curr := prev + float64(delta)
	countRow[col] = curr
	sumRow[col] += float64(delta)
	sum2Row[col] += float64(delta)
	t.Sketch.L1[row] += float64(delta)
}

func (t *CountMinTarget) Merge(other *CountMinTarget) error {
	return t.Sketch.Merge(other.Sketch)
}

func (t *CountMinTarget) EstimateMedian(input *common.SketchInput) float64 {
	return nitroCountMinEstimate(t.Sketch, input)
}

type CountSketchTarget struct {
	Sketch *countsketch.CountSketch
}

func NewCountSketchBatch(rate float64, rows, cols int) (*NitroBatch[*CountSketchTarget], error) {
	sketch, err := countsketch.WithDimensions(rows, cols)
	if err != nil {
		return nil, err
	}
	return WithTarget(rate, &CountSketchTarget{Sketch: sketch}), nil
}

func (t *CountSketchTarget) Rows() int {
	return t.Sketch.Rows
}

func (t *CountSketchTarget) UpdateRow(row int, hashed common.Hash128, delta uint64) {
	col := colForHash128(hashed, row, t.Sketch.AsStorage().MaskBits(), t.Sketch.AsStorage().Mask(), t.Sketch.Cols)
	sign := signForHash128(hashed, row)
	rowData := t.Sketch.Count[row]
	prev := rowData[col]
	curr := prev + sign*float64(delta)
	rowData[col] = curr
	t.Sketch.L2[row] += curr*curr - prev*prev
}

func (t *CountSketchTarget) Merge(other *CountSketchTarget) error {
	return t.Sketch.Merge(other.Sketch)
}

func (t *CountSketchTarget) EstimateMedian(input *common.SketchInput) float64 {
	return nitroCountSketchEstimate(t.Sketch, input)
}

func nitroCountMinEstimate(sketch *countminsketch.CountMinSketch, input *common.SketchInput) float64 {
	if input == nil {
		return 0
	}
	hashed := common.Hash128It(0, input.Bytes)
	maskBits := sketch.AsStorage().MaskBits()
	mask := sketch.AsStorage().Mask()
	minVal := math.MaxFloat64
	for row := 0; row < sketch.Rows; row++ {
		col := colForHash128(hashed, row, maskBits, mask, sketch.Cols)
		val := sketch.Count[row][col]
		if val < minVal {
			minVal = val
		}
	}
	if minVal == math.MaxFloat64 {
		return 0
	}
	return minVal
}

func nitroCountSketchEstimate(sketch *countsketch.CountSketch, input *common.SketchInput) float64 {
	if input == nil {
		return 0
	}
	hashed := common.Hash128It(0, input.Bytes)
	maskBits := sketch.AsStorage().MaskBits()
	mask := sketch.AsStorage().Mask()
	estimates := make([]float64, sketch.Rows)
	for row := 0; row < sketch.Rows; row++ {
		col := colForHash128(hashed, row, maskBits, mask, sketch.Cols)
		estimates[row] = sketch.Count[row][col] * signForHash128(hashed, row)
	}
	return common.ComputeMedianInlineF64(estimates)
}

func colForHash128(hashed common.Hash128, row int, maskBits uint, mask uint64, cols int) int {
	col := int(shiftRight128(hashed.Hi, hashed.Lo, maskBits*uint(row)) & mask)
	if col >= cols {
		col %= cols
	}
	return col
}

func signForHash128(hashed common.Hash128, row int) float64 {
	if bitAt128(hashed.Hi, hashed.Lo, uint(127-row)) == 0 {
		return -1.0
	}
	return 1.0
}

func shiftRight128(hi, lo uint64, shift uint) uint64 {
	if shift == 0 {
		return lo
	}
	if shift < 64 {
		return (lo >> shift) | (hi << (64 - shift))
	}
	if shift < 128 {
		return hi >> (shift - 64)
	}
	return 0
}

func bitAt128(hi, lo uint64, shift uint) uint64 {
	if shift < 64 {
		return (lo >> shift) & 1
	}
	if shift < 128 {
		return (hi >> (shift - 64)) & 1
	}
	return 0
}

func buildPrecomputedSampleRate1Percent() []float64 {
	rng := rand.New(rand.NewSource(1))
	samples := make([]float64, precomputedSampleSize)
	denom := math.Log(0.99)
	for i := range samples {
		var k float64
		for {
			k = rng.Float64()
			if k != 0.0 && k != 1.0 {
				break
			}
		}
		samples[i] = math.Log(1.0-k) / denom
	}
	return samples
}
