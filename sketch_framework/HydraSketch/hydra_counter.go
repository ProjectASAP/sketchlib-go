package hydrasketch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"

	"github.com/ProjectASAP/sketchlib-go/common"
	univmon "github.com/ProjectASAP/sketchlib-go/sketch_framework/UnivMon"
	countminsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountMinSketch"
	countsketch "github.com/ProjectASAP/sketchlib-go/sketches/CountSketch"
	hll "github.com/ProjectASAP/sketchlib-go/sketches/HLL"
	kll "github.com/ProjectASAP/sketchlib-go/sketches/KLL"
	"github.com/ProjectASAP/sketchlib-go/wire/asapv1"
)

// HydraCounterType names a Hydra counter variant.
type HydraCounterType string

const (
	HydraCounterCM        HydraCounterType = "cm"
	HydraCounterCS        HydraCounterType = "cs"
	HydraCounterHLL       HydraCounterType = "hll"
	HydraCounterKLL       HydraCounterType = "kll"
	HydraCounterUniversal HydraCounterType = "universal"
)

// HydraQueryKind is the statistic a HydraQuery asks for.
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

// HydraCounter is the sketch in each Hydra cell. The set of counters is
// closed: the constructors in this package return the only implementations.
type HydraCounter interface {
	CounterType() HydraCounterType
	Clone() (HydraCounter, error)
	Insert(value *common.SketchInput, count int64) error
	Query(q HydraQuery) (float64, error)
	Merge(other HydraCounter) error
	sealed()
}

var errNilValue = errors.New("hydra: nil value")

func unsupported(c HydraCounter, q HydraQuery) error {
	return fmt.Errorf("hydra: %s counter does not support query kind %d", c.CounterType(), q.Kind)
}

func mismatch(c, other HydraCounter) error {
	return fmt.Errorf("hydra: cannot merge a %s counter into a %s counter", other.CounterType(), c.CounterType())
}

// countMinCounter is a fast-mode Count-Min matrix.
type countMinCounter struct {
	s *countminsketch.CountMinSketch
}

// NewHydraCountMinCounter returns an empty rows x cols Count-Min counter.
func NewHydraCountMinCounter(rows, cols int) (HydraCounter, error) {
	s, err := countminsketch.NewCountMinSketch(rows, cols)
	if err != nil {
		return nil, err
	}
	return &countMinCounter{s: s}, nil
}

func (c *countMinCounter) sealed()                       {}
func (c *countMinCounter) CounterType() HydraCounterType { return HydraCounterCM }

func (c *countMinCounter) Clone() (HydraCounter, error) {
	b, err := c.s.MarshalASAPv1()
	if err != nil {
		return nil, err
	}
	s := new(countminsketch.CountMinSketch)
	if err := s.UnmarshalASAPv1(b); err != nil {
		return nil, err
	}
	return &countMinCounter{s: s}, nil
}

func (c *countMinCounter) Insert(value *common.SketchInput, count int64) error {
	if value == nil {
		return errNilValue
	}
	c.s.UpdateWeight(value, float64(count))
	return nil
}

func (c *countMinCounter) Query(q HydraQuery) (float64, error) {
	if q.Kind != HydraQueryFrequency {
		return 0, unsupported(c, q)
	}
	if q.Value == nil {
		return 0, errNilValue
	}
	return c.s.Estimate(q.Value), nil
}

func (c *countMinCounter) Merge(other HydraCounter) error {
	o, ok := other.(*countMinCounter)
	if !ok {
		return mismatch(c, other)
	}
	return c.s.Merge(o.s)
}

// countSketchCounter is a fast-mode Count Sketch matrix of int32 counters.
type countSketchCounter struct {
	s *countsketch.CountSketch
}

// NewHydraCountSketchCounter returns an empty rows x cols Count Sketch counter
// of int32 counters in fast mode; cols must be a power of two.
func NewHydraCountSketchCounter(rows, cols int) (HydraCounter, error) {
	s, err := countsketch.NewCountSketch(rows, cols)
	if err != nil {
		return nil, err
	}
	s.CounterType, s.Mode = countsketch.CounterInt32, countsketch.ModeFast
	return &countSketchCounter{s: s}, nil
}

func (c *countSketchCounter) sealed()                       {}
func (c *countSketchCounter) CounterType() HydraCounterType { return HydraCounterCS }

func (c *countSketchCounter) Clone() (HydraCounter, error) {
	b, err := c.s.MarshalASAPv1()
	if err != nil {
		return nil, err
	}
	s := new(countsketch.CountSketch)
	if err := s.UnmarshalASAPv1(b); err != nil {
		return nil, err
	}
	return &countSketchCounter{s: s}, nil
}

func (c *countSketchCounter) Insert(value *common.SketchInput, count int64) error {
	if value == nil {
		return errNilValue
	}
	c.s.UpdateWeight(value, float64(count))
	return nil
}

func (c *countSketchCounter) Query(q HydraQuery) (float64, error) {
	if q.Kind != HydraQueryFrequency {
		return 0, unsupported(c, q)
	}
	if q.Value == nil {
		return 0, errNilValue
	}
	return c.s.Estimate(q.Value), nil
}

func (c *countSketchCounter) Merge(other HydraCounter) error {
	o, ok := other.(*countSketchCounter)
	if !ok {
		return mismatch(c, other)
	}
	return c.s.Merge(o.s)
}

// hllCounter is a precision-14 Ertl-MLE HyperLogLog.
type hllCounter struct {
	s *hll.HyperLogLog
}

// NewHydraHLLCounter returns an empty HyperLogLog counter.
func NewHydraHLLCounter() HydraCounter {
	return &hllCounter{s: hll.NewHyperLogLog()}
}

func (c *hllCounter) sealed()                       {}
func (c *hllCounter) CounterType() HydraCounterType { return HydraCounterHLL }

func (c *hllCounter) Clone() (HydraCounter, error) {
	b, err := c.s.MarshalASAPv1()
	if err != nil {
		return nil, err
	}
	s := new(hll.HyperLogLog)
	if err := s.UnmarshalASAPv1(b); err != nil {
		return nil, err
	}
	return &hllCounter{s: s}, nil
}

// Insert records value once, whatever count is.
func (c *hllCounter) Insert(value *common.SketchInput, _ int64) error {
	if value == nil {
		return errNilValue
	}
	c.s.Update(value)
	return nil
}

func (c *hllCounter) Query(q HydraQuery) (float64, error) {
	if q.Kind != HydraQueryCardinality {
		return 0, unsupported(c, q)
	}
	return float64(c.s.Estimate()), nil
}

func (c *hllCounter) Merge(other HydraCounter) error {
	o, ok := other.(*hllCounter)
	if !ok {
		return mismatch(c, other)
	}
	return c.s.Merge(o.s)
}

// kllCounter is a KLL sketch of float64 values.
type kllCounter struct {
	s *kll.KLLSketch
}

// NewHydraKLLCounter returns an empty KLL counter with accuracy k and minimum
// level capacity m, clamped as kll.Init clamps them.
func NewHydraKLLCounter(k, m int) HydraCounter {
	return &kllCounter{s: kll.Init(k, m)}
}

func (c *kllCounter) sealed()                       {}
func (c *kllCounter) CounterType() HydraCounterType { return HydraCounterKLL }

func (c *kllCounter) Clone() (HydraCounter, error) {
	e := asapv1.NewEncoder()
	if err := c.s.EncodeASAPv1Payload(e); err != nil {
		return nil, err
	}
	return decodeKLLCell(uint32(c.s.K()), uint32(c.s.M()), asapv1.NewDecoder(e.Bytes()))
}

// Insert adds value's float64 count times; value must carry a float64.
func (c *kllCounter) Insert(value *common.SketchInput, count int64) error {
	if value == nil {
		return errNilValue
	}
	if !value.HasFloat64 {
		return errors.New("hydra: a KLL counter takes float64 values")
	}
	if count < 0 {
		return fmt.Errorf("hydra: a KLL counter takes no negative count, got %d", count)
	}
	for range count {
		c.s.Update(value.Float64)
	}
	return nil
}

func (c *kllCounter) Query(q HydraQuery) (float64, error) {
	switch q.Kind {
	case HydraQueryQuantile:
		return c.s.Quantile(q.Threshold), nil
	case HydraQueryCDF:
		return c.s.CDF().Quantile(q.Threshold), nil
	default:
		return 0, unsupported(c, q)
	}
}

func (c *kllCounter) Merge(other HydraCounter) error {
	o, ok := other.(*kllCounter)
	if !ok {
		return mismatch(c, other)
	}
	return c.s.Merge(o.s)
}

// univMonCounter is a UnivMon pyramid whose heaps hold keys of type K.
type univMonCounter[K asapv1.HeapKey] struct {
	s *univmon.UnivMon[K]
}

// NewHydraUnivMonCounter returns an empty UnivMon counter of layerSize layers,
// each a sketchRow x sketchCol CountL2HH and a heap of heapSize keys of type K.
func NewHydraUnivMonCounter[K asapv1.HeapKey](heapSize, sketchRow, sketchCol, layerSize int) (HydraCounter, error) {
	s, err := univmon.NewUnivMon[K](heapSize, sketchRow, sketchCol, layerSize)
	if err != nil {
		return nil, err
	}
	return &univMonCounter[K]{s: s}, nil
}

func (c *univMonCounter[K]) sealed()                       {}
func (c *univMonCounter[K]) CounterType() HydraCounterType { return HydraCounterUniversal }

func (c *univMonCounter[K]) Clone() (HydraCounter, error) {
	b, err := c.s.MarshalASAPv1()
	if err != nil {
		return nil, err
	}
	s := new(univmon.UnivMon[K])
	if err := s.UnmarshalASAPv1(b); err != nil {
		return nil, err
	}
	return &univMonCounter[K]{s: s}, nil
}

// Insert adds count for value's key, converted as inputKey converts it.
func (c *univMonCounter[K]) Insert(value *common.SketchInput, count int64) error {
	if value == nil {
		return errNilValue
	}
	key, err := inputKey[K](value)
	if err != nil {
		return err
	}
	return c.s.Insert(key, count)
}

func (c *univMonCounter[K]) Query(q HydraQuery) (float64, error) {
	switch q.Kind {
	case HydraQueryCardinality:
		return c.s.CalcCard(), nil
	case HydraQueryL1Norm:
		return c.s.CalcL1(), nil
	case HydraQueryL2Norm:
		return c.s.CalcL2(), nil
	case HydraQueryEntropy:
		return c.s.CalcEntropy(), nil
	default:
		return 0, unsupported(c, q)
	}
}

func (c *univMonCounter[K]) Merge(other HydraCounter) error {
	o, ok := other.(*univMonCounter[K])
	if !ok {
		return mismatch(c, other)
	}
	return c.s.Merge(o.s)
}

// inputKey converts v to a key of type K: a string from its bytes (its Hash in
// hex when it has none), a []byte from its bytes, a float from its float64, an
// integer from its 8 native-endian bytes when the value fits K.
func inputKey[K asapv1.HeapKey](v *common.SketchInput) (K, error) {
	var key K
	rv := reflect.ValueOf(&key).Elem()
	switch rv.Kind() {
	case reflect.String:
		if v.Bytes == nil {
			rv.SetString(strconv.FormatUint(v.Hash, 16))
		} else {
			rv.SetString(string(v.Bytes))
		}
		return key, nil
	case reflect.Slice:
		rv.SetBytes(bytes.Clone(v.Bytes))
		return key, nil
	case reflect.Float32, reflect.Float64:
		if !v.HasFloat64 {
			return key, errors.New("hydra: a float-keyed UnivMon counter takes float64 values")
		}
		if rv.Kind() == reflect.Float32 && float64(float32(v.Float64)) != v.Float64 && !math.IsNaN(v.Float64) {
			return key, fmt.Errorf("hydra: %v is not exact as a float32 key", v.Float64)
		}
		rv.SetFloat(v.Float64)
		return key, nil
	}
	if len(v.Bytes) != 8 {
		return key, fmt.Errorf("hydra: an integer key takes 8 value bytes, got %d", len(v.Bytes))
	}
	u := binary.NativeEndian.Uint64(v.Bytes)
	switch rv.Kind() {
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if rv.OverflowInt(int64(u)) {
			return key, fmt.Errorf("hydra: %d overflows a %s key", int64(u), rv.Type())
		}
		rv.SetInt(int64(u))
	default:
		if rv.OverflowUint(u) {
			return key, fmt.Errorf("hydra: %d overflows a %s key", u, rv.Type())
		}
		rv.SetUint(u)
	}
	return key, nil
}
