package ddsketch

import (
	"errors"
	"math"

	"google.golang.org/protobuf/proto"

	"github.com/ProjectASAP/sketchlib-go/common/storage"
	commonpb "github.com/ProjectASAP/sketchlib-go/proto/common"
	ddpb "github.com/ProjectASAP/sketchlib-go/proto/ddsketch"
	envpb "github.com/ProjectASAP/sketchlib-go/proto/sketch_envelope"
)

// SerializePortable serializes the DDSketch into a portable protobuf SketchEnvelope.
//
// Only alpha is transmitted; the consumer recomputes gamma and invLogGamma:
//
//	alpha = (gamma - 1) / (gamma + 1)
//	gamma = (1 + alpha) / (1 - alpha)
func (d *DDSketch) SerializePortable() (*envpb.SketchEnvelope, error) {
	// Derive alpha from the stored gamma.
	gamma := d.mapping.gamma
	alpha := (gamma - 1.0) / (gamma + 1.0)

	var storeCounts []uint64
	if d.store.counts != nil {
		storeCounts = append([]uint64(nil), d.store.counts.AsSlice()...)
	}

	state := &ddpb.DDSketchState{
		Alpha:       alpha,
		StoreCounts: storeCounts,
		StoreOffset: d.store.offset,
		Count:       d.count,
		Sum:         d.sum,
		Min:         d.min,
		Max:         d.max,
	}

	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SketchState: &envpb.SketchEnvelope_Ddsketch{
			Ddsketch: state,
		},
	}, nil
}

// SerializeStateProtoBytes returns the raw prost-compatible bytes of the
// inner `DDSketchState` message (without the `SketchEnvelope` wrapper).
// This is the wire format that ASAPQuery-backend's
// `sketch_core::dd_sketch::DdSketchAccumulator::from_sketchlib_proto_bytes`
// consumes when the modified-OTLP `DDSketchDataPoint.encoding` is
// `DDSKETCH_ENCODING_PROTO = 1`.
//
// `SerializePortable` wraps the state in an `envpb.SketchEnvelope` for
// cross-sketch dispatch. When the caller already knows the sketch type
// (e.g. a `DDSketchDataPoint` already identifies the variant via the
// OTLP message type), the envelope overhead is wasted — this helper
// emits the inner state directly.
func (d *DDSketch) SerializeStateProtoBytes() ([]byte, error) {
	gamma := d.mapping.gamma
	alpha := (gamma - 1.0) / (gamma + 1.0)

	var storeCounts []uint64
	if d.store.counts != nil {
		storeCounts = append([]uint64(nil), d.store.counts.AsSlice()...)
	}

	state := &ddpb.DDSketchState{
		Alpha:       alpha,
		StoreCounts: storeCounts,
		StoreOffset: d.store.offset,
		Count:       d.count,
		Sum:         d.sum,
		Min:         d.min,
		Max:         d.max,
	}
	return proto.Marshal(state)
}

// NewFromState rebuilds a DDSketch from a decoded `DDSketchState` proto.
// The mapping is recomputed from `state.Alpha` (gamma = (1+α)/(1-α)),
// and bucket counts are restored into the internal `Buckets` store at
// the given `StoreOffset`. This is the constructor producers should
// use when consuming a `DDSketchDataPoint` with
// `DDSKETCH_ENCODING_PROTO` bytes that were emitted by another
// sketchlib-go peer (e.g. a DataCollector sketch processor).
//
// Empty sketches (count == 0) round-trip via the +Inf/-Inf min/max
// sentinels the constructor already seeds.
func NewFromState(state *ddpb.DDSketchState) (*DDSketch, error) {
	if state == nil {
		return nil, errors.New("NewFromState: state is nil")
	}
	if !(state.Alpha > 0 && state.Alpha < 1) {
		return nil, errors.New("NewFromState: alpha must be in (0, 1)")
	}
	mapping := NewIndexMapping(state.Alpha)

	var buckets Buckets
	if len(state.StoreCounts) > 0 {
		cp := append([]uint64(nil), state.StoreCounts...)
		buckets = Buckets{
			counts: storage.Vector1DFromVec(cp),
			offset: state.StoreOffset,
		}
	}

	// Empty-sketch sentinels. If state.Count == 0 and min/max weren't
	// explicitly set (i.e. they are the zero-value float), reset them
	// to +Inf / -Inf so subsequent Add() calls start from the standard
	// NewDDSketch state.
	minVal := state.Min
	maxVal := state.Max
	if state.Count == 0 && !math.IsInf(minVal, 1) {
		minVal = math.Inf(1)
	}
	if state.Count == 0 && !math.IsInf(maxVal, -1) {
		maxVal = math.Inf(-1)
	}

	return &DDSketch{
		mapping: mapping,
		store:   buckets,
		count:   state.Count,
		sum:     state.Sum,
		min:     minVal,
		max:     maxVal,
	}, nil
}

// NewFromStateProtoBytes is a convenience wrapper: unmarshal the raw
// proto bytes emitted by `SerializeStateProtoBytes` into a fresh
// DDSketch.
func NewFromStateProtoBytes(data []byte) (*DDSketch, error) {
	var state ddpb.DDSketchState
	if err := proto.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return NewFromState(&state)
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
