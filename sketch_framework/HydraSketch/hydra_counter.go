package hydrasketch

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/ProjectASAP/sketchlib-go/common"
	univmon "github.com/ProjectASAP/sketchlib-go/sketch_framework/UnivMon"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
	hll "github.com/ProjectASAP/sketchlib-go/sketches/HLL"
	kll "github.com/ProjectASAP/sketchlib-go/sketches/KLL"
)

// HydraCounterType mirrors sketchlib-rust Hydra counter variants.
type HydraCounterType string

const (
	HydraCounterCM        HydraCounterType = "cm"
	HydraCounterCS        HydraCounterType = "cs"
	HydraCounterHLL       HydraCounterType = "hll"
	HydraCounterKLL       HydraCounterType = "kll"
	HydraCounterUniversal HydraCounterType = "universal"
)

// HydraQueryKind mirrors sketchlib-rust Hydra query variants.
type HydraQueryKind int

const (
	HydraQueryFrequency HydraQueryKind = iota
	HydraQueryQuantile
	HydraQueryCDF
	HydraQueryCardinality
	HydraQueryL1Norm
	HydraQueryL2Norm
	HydraQueryEntropy
)

// HydraQuery describes a typed query for a HydraCounter.
type HydraQuery struct {
	Kind      HydraQueryKind
	Value     *common.SketchInput
	Threshold float64
}

func FrequencyQuery(v *common.SketchInput) HydraQuery {
	return HydraQuery{Kind: HydraQueryFrequency, Value: v}
}

func QuantileQuery(q float64) HydraQuery {
	return HydraQuery{Kind: HydraQueryQuantile, Threshold: q}
}

func CDFQuery(x float64) HydraQuery {
	return HydraQuery{Kind: HydraQueryCDF, Threshold: x}
}

func CardinalityQuery() HydraQuery {
	return HydraQuery{Kind: HydraQueryCardinality}
}

func L1NormQuery() HydraQuery {
	return HydraQuery{Kind: HydraQueryL1Norm}
}

func L2NormQuery() HydraQuery {
	return HydraQuery{Kind: HydraQueryL2Norm}
}

func EntropyQuery() HydraQuery {
	return HydraQuery{Kind: HydraQueryEntropy}
}

// HydraCounter is the counter abstraction per Hydra cell.
type HydraCounter interface {
	CounterType() HydraCounterType
	Clone() (HydraCounter, error)
	Insert(value *common.SketchInput, count int64)
	InsertWithHash(value *common.SketchInput, hash uint64, count int64)
	Query(q HydraQuery) (float64, error)
	Merge(other HydraCounter) error
	SerializeToBytes() ([]byte, error)
	SetTopKEnabled(enabled bool)
	TopK(k int) []Pair
}

func decodeCounter(counterType HydraCounterType, data []byte) (HydraCounter, error) {
	switch counterType {
	case HydraCounterCM:
		s, err := countminsketch.DeserializeCountMinSketchFromBytes(data)
		if err != nil {
			return nil, err
		}
		return &countMinCounter{s: s}, nil
	case HydraCounterCS:
		s, err := countsketch.DeserializeCountSketchFromBytes(data)
		if err != nil {
			return nil, err
		}
		return &countSketchCounter{s: s}, nil
	case HydraCounterHLL:
		s, err := hll.DeserializeHyperLogLogFromBytes(data)
		if err != nil {
			return nil, err
		}
		return &hllCounter{s: s}, nil
	case HydraCounterKLL:
		s, err := kll.DeserializeKLLSketchFromBytes(data)
		if err != nil {
			return nil, err
		}
		return &kllCounter{s: s}, nil
	case HydraCounterUniversal:
		s, err := univmon.DeserializeUnivSketchFromBytes(data)
		if err != nil {
			return nil, err
		}
		return &univCounter{s: s}, nil
	default:
		return nil, fmt.Errorf("unknown hydra counter type: %s", counterType)
	}
}

func cloneCounter(c HydraCounter) (HydraCounter, error) {
	b, err := c.SerializeToBytes()
	if err != nil {
		return nil, err
	}
	return decodeCounter(c.CounterType(), b)
}

func inputToFloat64(v *common.SketchInput) float64 {
	if v == nil {
		return 0
	}
	if len(v.Bytes) >= 8 {
		return math.Float64frombits(binary.NativeEndian.Uint64(v.Bytes[:8]))
	}
	return float64(v.Hash)
}

// CountMin counter

type countMinCounter struct {
	s *countminsketch.CountMinSketch
}

func NewHydraCountMinCounter(rows, cols int) (HydraCounter, error) {
	s, err := countminsketch.WithDimensions(rows, cols)
	if err != nil {
		return nil, err
	}
	return &countMinCounter{s: s}, nil
}

func (c *countMinCounter) CounterType() HydraCounterType { return HydraCounterCM }
func (c *countMinCounter) Clone() (HydraCounter, error)  { return cloneCounter(c) }
func (c *countMinCounter) Insert(value *common.SketchInput, count int64) {
	if value == nil || count == 0 {
		return
	}
	c.s.InsertWeight(value, float64(count))
}
func (c *countMinCounter) InsertWithHash(_ *common.SketchInput, hash uint64, count int64) {
	if count == 0 {
		return
	}
	c.s.FastInsertWeightWithHashValue(hash, float64(count))
}
func (c *countMinCounter) Query(q HydraQuery) (float64, error) {
	if q.Kind != HydraQueryFrequency || q.Value == nil {
		return 0, errors.New("count-min only supports frequency query")
	}
	return c.s.QueryWithHash(common.QueryFrequency, q.Value.Hash)
}
func (c *countMinCounter) Merge(other HydraCounter) error {
	o, ok := other.(*countMinCounter)
	if !ok {
		return errors.New("counter type mismatch")
	}
	return c.s.Merge(o.s)
}
func (c *countMinCounter) SerializeToBytes() ([]byte, error) { return c.s.SerializeToBytes() }
func (c *countMinCounter) SetTopKEnabled(bool)               {}
func (c *countMinCounter) TopK(int) []Pair                   { return nil }

// CountSketch counter

type countSketchCounter struct {
	s *countsketch.CountSketch
}

func NewHydraCountSketchCounter(rows, cols int) (HydraCounter, error) {
	s, err := countsketch.WithDimensions(rows, cols)
	if err != nil {
		return nil, err
	}
	return &countSketchCounter{s: s}, nil
}

func (c *countSketchCounter) CounterType() HydraCounterType { return HydraCounterCS }
func (c *countSketchCounter) Clone() (HydraCounter, error)  { return cloneCounter(c) }
func (c *countSketchCounter) Insert(value *common.SketchInput, count int64) {
	if value == nil || count == 0 {
		return
	}
	c.s.InsertWeight(value, float64(count))
}
func (c *countSketchCounter) InsertWithHash(_ *common.SketchInput, hash uint64, count int64) {
	if count == 0 {
		return
	}
	c.s.FastInsertWeightWithHashValue(hash, float64(count))
}
func (c *countSketchCounter) Query(q HydraQuery) (float64, error) {
	if q.Kind != HydraQueryFrequency || q.Value == nil {
		return 0, errors.New("count-sketch only supports frequency query")
	}
	return c.s.QueryWithHash(common.QueryFrequency, q.Value.Hash)
}
func (c *countSketchCounter) Merge(other HydraCounter) error {
	o, ok := other.(*countSketchCounter)
	if !ok {
		return errors.New("counter type mismatch")
	}
	return c.s.Merge(o.s)
}
func (c *countSketchCounter) SerializeToBytes() ([]byte, error) { return c.s.SerializeToBytes() }
func (c *countSketchCounter) SetTopKEnabled(bool)               {}
func (c *countSketchCounter) TopK(int) []Pair                   { return nil }

// HLL counter

type hllCounter struct {
	s *hll.HyperLogLog
}

func NewHydraHLLCounter() HydraCounter {
	return &hllCounter{s: hll.New()}
}

func (c *hllCounter) CounterType() HydraCounterType { return HydraCounterHLL }
func (c *hllCounter) Clone() (HydraCounter, error)  { return cloneCounter(c) }
func (c *hllCounter) Insert(value *common.SketchInput, count int64) {
	if value == nil {
		return
	}
	c.s.InsertInput(value)
}
func (c *hllCounter) InsertWithHash(_ *common.SketchInput, hash uint64, count int64) {
	if count == 0 {
		return
	}
	c.s.InsertWithHash(hash)
}
func (c *hllCounter) Query(q HydraQuery) (float64, error) {
	if q.Kind != HydraQueryCardinality {
		return 0, errors.New("hll only supports cardinality query")
	}
	return float64(c.s.EstimateCardinality()), nil
}
func (c *hllCounter) Merge(other HydraCounter) error {
	o, ok := other.(*hllCounter)
	if !ok {
		return errors.New("counter type mismatch")
	}
	return c.s.Merge(o.s)
}
func (c *hllCounter) SerializeToBytes() ([]byte, error) { return c.s.SerializeToBytes() }
func (c *hllCounter) SetTopKEnabled(bool)               {}
func (c *hllCounter) TopK(int) []Pair                   { return nil }

// KLL counter

type kllCounter struct {
	s *kll.KLLSketch
}

func NewHydraKLLCounter(k int) (HydraCounter, error) {
	s, err := kll.NewKLLSketch(k)
	if err != nil {
		return nil, err
	}
	return &kllCounter{s: s}, nil
}

func (c *kllCounter) CounterType() HydraCounterType { return HydraCounterKLL }
func (c *kllCounter) Clone() (HydraCounter, error)  { return cloneCounter(c) }
func (c *kllCounter) Insert(value *common.SketchInput, count int64) {
	if value == nil || count <= 0 {
		return
	}
	v := inputToFloat64(value)
	for i := int64(0); i < count; i++ {
		c.s.Insert(v)
	}
}
func (c *kllCounter) InsertWithHash(value *common.SketchInput, _ uint64, count int64) {
	// KLL requires value semantics; follow Rust behavior and fallback to normal insert.
	c.Insert(value, count)
}
func (c *kllCounter) Query(q HydraQuery) (float64, error) {
	switch q.Kind {
	case HydraQueryQuantile:
		return c.s.Quantile(q.Threshold), nil
	case HydraQueryCDF:
		return c.s.CDF().Quantile(q.Threshold), nil
	default:
		return 0, errors.New("kll only supports quantile/cdf query")
	}
}
func (c *kllCounter) Merge(other HydraCounter) error {
	o, ok := other.(*kllCounter)
	if !ok {
		return errors.New("counter type mismatch")
	}
	return c.s.Merge(o.s)
}
func (c *kllCounter) SerializeToBytes() ([]byte, error) { return c.s.SerializeToBytes() }
func (c *kllCounter) SetTopKEnabled(bool)               {}
func (c *kllCounter) TopK(int) []Pair                   { return nil }

// UnivMon counter

type univCounter struct {
	s *univmon.UnivSketch
}

func NewHydraUnivMonCounter(topK, row, col, layer int) (HydraCounter, error) {
	s, err := univmon.NewUnivSketchPyramid(topK, row, col, layer)
	if err != nil {
		return nil, err
	}
	return &univCounter{s: s}, nil
}

func (c *univCounter) CounterType() HydraCounterType { return HydraCounterUniversal }
func (c *univCounter) Clone() (HydraCounter, error)  { return cloneCounter(c) }
func (c *univCounter) Insert(value *common.SketchInput, count int64) {
	if value == nil || count == 0 {
		return
	}
	c.s.Update(value, count)
}
func (c *univCounter) InsertWithHash(_ *common.SketchInput, hash uint64, count int64) {
	if count == 0 {
		return
	}
	c.s.UpdateWithHashOnly(hash, count)
}
func (c *univCounter) Query(q HydraQuery) (float64, error) {
	switch q.Kind {
	case HydraQueryFrequency:
		if q.Value == nil {
			return 0, errors.New("frequency query needs value")
		}
		v, err := c.s.QueryWithHash(common.QueryFrequency, q.Value.Hash)
		if err != nil {
			return 0, err
		}
		return v, nil
	case HydraQueryCardinality:
		return c.s.GetCardinality(), nil
	case HydraQueryL1Norm:
		return c.s.GetL1(), nil
	case HydraQueryL2Norm:
		v, err := c.s.QueryWithHash(common.QuerySum2, 0)
		if err != nil {
			return 0, err
		}
		if v < 0 {
			return 0, nil
		}
		return math.Sqrt(v), nil
	case HydraQueryEntropy:
		return c.s.GetEntropy(), nil
	default:
		return 0, errors.New("univmon query kind not supported")
	}
}
func (c *univCounter) Merge(other HydraCounter) error {
	o, ok := other.(*univCounter)
	if !ok {
		return errors.New("counter type mismatch")
	}
	return c.s.Merge(o.s)
}
func (c *univCounter) SerializeToBytes() ([]byte, error) { return c.s.SerializeToBytes() }
func (c *univCounter) SetTopKEnabled(enabled bool)       { c.s.SetTopKEnabled(enabled) }
func (c *univCounter) TopK(k int) []Pair {
	hh := c.s.QueryTopK(k)
	out := make([]Pair, 0, len(hh.Heap))
	for _, item := range hh.Heap {
		out = append(out, Pair{Key: item.Key, Value: item.Count})
	}
	return out
}
