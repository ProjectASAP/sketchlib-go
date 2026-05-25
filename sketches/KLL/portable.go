package kll

import (
	"fmt"
	"math"

	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	kllpb "github.com/ProjectASAP/sketchlib-go/proto/kll"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
	"google.golang.org/protobuf/proto"
)

// kllScaleSweep is the set of decimal scale exponents the value-offset encoder
// tries, in priority order. value = residual * 10^scale + offset, so scale 0
// stores integers; negative scales carry fractional precision (e.g. -3 ==
// milli-resolution). The sweep is intentionally small and finite — the
// exactness guard rejects any scale that does not round-trip the retained set
// exactly, and the encoder falls back to raw f64 if none qualifies.
var kllScaleSweep = []int32{0, -1, -2, -3, -4, -5, -6}

// SerializePortable serializes the KLLSketch into a portable protobuf
// SketchEnvelope using the value-offset fixed-point representation when the
// retained samples are exactly representable at a candidate decimal scale,
// and falling back to raw f64 items otherwise. See SerializePortableRawF64 to
// force the legacy raw-f64 form.
func (s *KLLSketch) SerializePortable() (*envpb.SketchEnvelope, error) {
	return s.serializePortable(true)
}

// SerializePortableRawF64 serializes the sketch using the original raw-f64
// items[] representation, never emitting the value-offset fixed-point form.
// Retained mainly for migration/rollback and golden-fixture generation; the
// emitted bytes are identical to the pre-PR2 wire format.
func (s *KLLSketch) SerializePortableRawF64() (*envpb.SketchEnvelope, error) {
	return s.serializePortable(false)
}

func (s *KLLSketch) serializePortable(allowFixedPoint bool) (*envpb.SketchEnvelope, error) {
	// Convert []int levels to []uint32.
	levels := make([]uint32, len(s.levels))
	for i, v := range s.levels {
		levels[i] = uint32(v)
	}

	state := &kllpb.KLLState{
		K:         uint32(s.k),
		M:         uint32(s.m),
		NumLevels: uint32(s.numLevels),
		Levels:    levels,
		Coin: &kllpb.CoinState{
			State:         s.co.state,
			BitCache:      s.co.bitCache,
			RemainingBits: uint32(s.co.remainingBits),
		},
	}

	// Value-offset fixed-point encoding, gated on the exactness guard. When it
	// succeeds, items[] is left empty and the decoder reconstructs from
	// (offset, value_scale, residuals); otherwise we emit raw f64 items[].
	if allowFixedPoint {
		if offset, scale, residuals, ok := encodeValueOffset(s.items); ok {
			state.Offset = offset
			state.ValueScale = scale
			state.Residuals = residuals
			return wrapEnvelope(state), nil
		}
	}

	state.Items = append([]float64(nil), s.items...)
	return wrapEnvelope(state), nil
}

func wrapEnvelope(state *kllpb.KLLState) *envpb.SketchEnvelope {
	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &envpb.SketchEnvelope_Kll{
			Kll: state,
		},
	}
}

// encodeValueOffset attempts to represent items exactly as
// value = residual*10^scale + offset using a small sweep of decimal scales.
//
// The offset is the minimum value (so residuals are non-negative for sorted
// runs and small in magnitude). For each candidate scale it computes the
// integer residual round((value-offset)*10^-scale) and then RECONSTRUCTS the
// value, requiring exact bit-equality (the exactness guard, mirroring the cold
// path's decimal-exactness check). The first scale that round-trips every
// sample exactly wins. If none qualifies — e.g. an irrational or
// high-precision f64 that no reasonable decimal scale represents — ok is false
// and the caller falls back to raw f64.
//
// An empty item set is representable trivially (offset 0, scale 0, no
// residuals) but yields ok=false so the caller emits the (also empty) raw
// items[] path — keeping the empty-sketch wire bytes identical to v1.
func encodeValueOffset(items []float64) (offset float64, scale int32, residuals []int64, ok bool) {
	if len(items) == 0 {
		return 0, 0, nil, false
	}

	// offset = min value; reject non-finite values outright (cannot be
	// represented as residual*10^scale + offset).
	offset = items[0]
	for _, v := range items {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, 0, nil, false
		}
		if v < offset {
			offset = v
		}
	}

	for _, sc := range kllScaleSweep {
		mul := math.Pow(10, float64(-sc)) // (value-offset) * 10^-scale
		out := make([]int64, len(items))
		good := true
		for i, v := range items {
			scaled := (v - offset) * mul
			r := math.Round(scaled)
			// Residual must be a finite, in-range integer.
			if math.IsNaN(r) || math.IsInf(r, 0) ||
				r > math.MaxInt64 || r < math.MinInt64 {
				good = false
				break
			}
			ri := int64(r)
			// Exactness guard: reconstruct and require bit-identical recovery.
			recon := float64(ri)*math.Pow(10, float64(sc)) + offset
			if recon != v {
				good = false
				break
			}
			out[i] = ri
		}
		if good {
			return offset, sc, out, true
		}
	}
	return 0, 0, nil, false
}

// decodeValueOffset reconstructs the retained samples from the value-offset
// fixed-point fields. The caller must only invoke this when residuals is
// non-empty.
func decodeValueOffset(offset float64, scale int32, residuals []int64) []float64 {
	mul := math.Pow(10, float64(scale))
	out := make([]float64, len(residuals))
	for i, r := range residuals {
		out[i] = float64(r)*mul + offset
	}
	return out
}

// SerializeProtoBytes serializes the KLLSketch as a proto-encoded SketchEnvelope.
func (s *KLLSketch) SerializeProtoBytes() ([]byte, error) {
	env, err := s.SerializePortable()
	if err != nil {
		return nil, err
	}
	return proto.Marshal(env)
}

// DeserializeKLLSketchFromProtoBytes restores a KLLSketch from a proto-encoded SketchEnvelope.
func DeserializeKLLSketchFromProtoBytes(data []byte) (*KLLSketch, error) {
	var env envpb.SketchEnvelope
	if err := proto.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	st := env.GetKll()
	if st == nil {
		return nil, fmt.Errorf("kll: proto envelope does not contain KLLState")
	}

	k := int(st.GetK())
	m := int(st.GetM())
	if k <= 0 || m <= 0 {
		return nil, fmt.Errorf("kll: invalid parameters k=%d m=%d", k, m)
	}

	pbLevels := st.GetLevels()
	levels := make([]int, len(pbLevels))
	for i, v := range pbLevels {
		levels[i] = int(v)
	}
	if len(levels) < 2 {
		return nil, fmt.Errorf("kll: invalid levels length %d", len(levels))
	}

	// Dual-read: prefer the value-offset fixed-point representation when
	// residuals are present, else fall back to raw f64 items[] (v1 behavior).
	var items []float64
	if residuals := st.GetResiduals(); len(residuals) > 0 {
		if len(st.GetItems()) != 0 {
			return nil, fmt.Errorf("kll: both residuals and raw items[] set")
		}
		items = decodeValueOffset(st.GetOffset(), st.GetValueScale(), residuals)
	} else {
		items = append([]float64(nil), st.GetItems()...)
	}
	if levels[len(levels)-1] != len(items) {
		return nil, fmt.Errorf("kll: invalid item layout")
	}

	var co coin
	if pbCoin := st.GetCoin(); pbCoin != nil {
		co = coin{
			state:         normalizeSeed(pbCoin.GetState()),
			bitCache:      pbCoin.GetBitCache(),
			remainingBits: uint8(pbCoin.GetRemainingBits()),
		}
	} else {
		co = newCoin()
	}

	sketch := &KLLSketch{
		items:     items,
		levels:    levels,
		k:         k,
		m:         m,
		numLevels: len(levels) - 1,
		co:        co,
	}
	sketch.bindStoresFromSlices()
	sketch.rebuildCapacityCache()
	return sketch, nil
}

// portableHashSpec returns the standard HashSpec for sketchlib-go.
func portableHashSpec() *commonpb.HashSpec {
	return &commonpb.HashSpec{
		Algorithm:          commonpb.HashAlgorithm_HASH_ALGORITHM_XXH3_64,
		CanonicalSeedIndex: 5,
		SeedList: []uint64{
			0xcafe3553,
			0xade3415118,
			0x8cc70208,
			0x2f024b2b,
			0x451a3df5,
			0x6a09e667,
			0xbb67ae85,
			0x3c6ef372,
			0xa54ff53a,
			0x510e527f,
			0x9b05688c,
			0x1f83d9ab,
			0x5be0cd19,
			0xcbbb9d5d,
			0x629a292a,
			0x9159015a,
			0x152fecd8,
			0x67332667,
			0x8eb44a87,
			0xdb0c2e0d,
		},
		SeedDerivation: commonpb.SeedDerivation_SEED_DERIVATION_PACKED,
	}
}
