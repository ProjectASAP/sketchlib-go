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

// denseSparsityDivisor bounds how empty a dense bucket array is allowed to be
// before bucketWireForm switches to the sparse encoding: dense is kept only
// when span <= populated*denseSparsityDivisor (i.e. at least
// 1/denseSparsityDivisor of the span is actually populated).
//
// Calibration note: a dense zero entry costs ~1 byte on the wire (protobuf
// varint 0); a sparse (index, count) entry costs several bytes (an embedded
// message: tag + length + zigzag index + varint count), so the true
// byte-for-byte breakeven is around an 8x empty-to-populated ratio. This
// constant is deliberately set well above that breakeven — DDSketch's
// log-scale bucketing means even ordinary, non-pathological data (a handful
// of values spread across a couple of orders of magnitude) routinely produces
// occupancy ratios in the 1/20-1/30 range with a perfectly reasonable
// (hundreds of elements) absolute span; switching those to sparse would
// needlessly change the wire bytes for the common case. The bug this fixes
// is specifically the UNBOUNDED case — a genuine outlier (or a very fine
// alpha) driving the span into the thousands-to-millions — which this
// threshold still catches while leaving everyday spans on the legacy dense
// path. See bucketWireForm and DDSketchState.buckets (proto) for the full
// rationale (sketchlib-go#72).
const denseSparsityDivisor = 64

// bucketWireForm decides, and builds, the on-wire bucket encoding for a
// Buckets store: either the legacy DENSE positional array (store_counts +
// store_offset) or the SPARSE index+count list (the `buckets` field),
// mirroring the shape DDSketchDelta already uses for incremental deltas.
//
// The bug this fixes: SerializePortable/SerializeStateProtoBytes used to
// unconditionally copy d.store.counts.AsSlice() — a DENSE array spanning
// every position from the store's offset to its highest touched bucket —
// onto the wire. A single outlier value forces that array (and the O(span)
// allocation needed to copy it) to span an enormous, mostly-empty range
// even when only a handful of buckets are actually populated. bucketWireForm
// instead scans the EXISTING backing slice once (no copy) to count
// populated buckets, and only pays for a dense copy when the result is
// reasonably compact; otherwise it emits the sparse (index, count) pairs
// that are already the right shape for exactly this case.
//
// Choosing dense whenever it's compact keeps the wire bytes byte-for-byte
// identical to the pre-fix format for the common case, which matters
// because DDSketchState is also decoded by ASAPQuery-backend's Rust
// accumulator (sketch_core::dd_sketch::DdSketchAccumulator) — a consumer
// outside this repo that only understands the dense fields. That consumer
// needs a follow-up (tracked alongside sketchlib-go#72) to read the new
// `buckets` field; until then, a message that falls into the sparse case
// decodes there as an empty sketch rather than as a many-gigabyte dense
// array — a strict improvement over today's unbounded blowup, not a new
// regression, since the pathological span already forced that same
// unbounded copy on the Rust side beforehand.
func bucketWireForm(store *Buckets) (storeCounts []uint64, storeOffset int32, sparse []*ddpb.DDSketchBucketCount) {
	if store.IsEmpty() {
		return nil, 0, nil
	}
	counts := store.counts.AsSlice() // read-only view — no copy yet
	span := int64(len(counts))

	var populated int64
	for _, c := range counts {
		if c != 0 {
			populated++
		}
	}
	if populated == 0 {
		return nil, 0, nil
	}

	if span <= populated*denseSparsityDivisor {
		dense := make([]uint64, len(counts))
		copy(dense, counts)
		return dense, store.offset, nil
	}

	sparse = make([]*ddpb.DDSketchBucketCount, 0, populated)
	for i, c := range counts {
		if c == 0 {
			continue
		}
		sparse = append(sparse, &ddpb.DDSketchBucketCount{
			Index: store.offset + int32(i),
			Count: c,
		})
	}
	return nil, 0, sparse
}

// bucketsFromWire reconstructs the in-memory Buckets store from either wire
// shape bucketWireForm can produce. Sparse entries win when present (a
// producer never populates both); the resulting in-memory representation is
// identical either way — this only affects what crosses the wire, not how a
// DDSketch is held in memory (the separate, structural sketchlib-go#72
// concern is intentionally not addressed here — see bucketWireForm's doc).
func bucketsFromWire(storeCounts []uint64, storeOffset int32, sparse []*ddpb.DDSketchBucketCount) Buckets {
	if len(sparse) > 0 {
		minIdx, maxIdx := sparse[0].Index, sparse[0].Index
		for _, e := range sparse {
			if e.Index < minIdx {
				minIdx = e.Index
			}
			if e.Index > maxIdx {
				maxIdx = e.Index
			}
		}
		dense := make([]uint64, int(maxIdx-minIdx)+1)
		for _, e := range sparse {
			dense[int(e.Index-minIdx)] = e.Count
		}
		return Buckets{counts: storage.Vector1DFromVec(dense), offset: minIdx}
	}
	if len(storeCounts) > 0 {
		cp := append([]uint64(nil), storeCounts...)
		return Buckets{counts: storage.Vector1DFromVec(cp), offset: storeOffset}
	}
	return Buckets{}
}

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

	storeCounts, storeOffset, sparse := bucketWireForm(&d.store)

	state := &ddpb.DDSketchState{
		Alpha:       alpha,
		StoreCounts: storeCounts,
		StoreOffset: storeOffset,
		Buckets:     sparse,
	}

	return &envpb.SketchEnvelope{
		FormatVersion: 1,
		Producer: &commonpb.ProducerInfo{
			Library: "sketchlib-go",
			Version: "0.1.0",
		},
		HashSpec: portableHashSpec(),
		SampleP:  d.wireSampleP(),
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

	storeCounts, storeOffset, sparse := bucketWireForm(&d.store)

	state := &ddpb.DDSketchState{
		Alpha:       alpha,
		StoreCounts: storeCounts,
		StoreOffset: storeOffset,
		Buckets:     sparse,
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
// The DataPoint-level metric scalars (count/sum/min/max) are no longer
// carried on the wire: count is recovered by summing the bucket counts,
// and min/max are derived from the lowest/highest non-empty buckets
// (relative-accuracy bounded). sum is unrecoverable and is left at zero
// (callers must not rely on the reconstructed sketch's sum). Empty
// sketches round-trip via the +Inf/-Inf min/max sentinels.
func NewFromState(state *ddpb.DDSketchState) (*DDSketch, error) {
	if state == nil {
		return nil, errors.New("NewFromState: state is nil")
	}
	if !(state.Alpha > 0 && state.Alpha < 1) {
		return nil, errors.New("NewFromState: alpha must be in (0, 1)")
	}
	mapping := NewIndexMapping(state.Alpha)

	buckets := bucketsFromWire(state.StoreCounts, state.StoreOffset, state.Buckets)

	count, minVal, maxVal := deriveScalarsFromBuckets(&buckets, mapping)

	return &DDSketch{
		mapping: mapping,
		store:   buckets,
		count:   count,
		// sum is not carried on the wire and is unrecoverable from the
		// bucket distribution; leave it at the zero value.
		sum: 0,
		min: minVal,
		max: maxVal,
		// gosPopulated: this constructor is used by the BACKEND's decode path
		// and by cross-peer merge, not by an edge-side GOS-enabled series, but
		// initialise it correctly anyway (one more O(span) pass over a
		// reconstruction that is already O(span)) so a sketch built this way
		// behaves correctly if it is ever subsequently driven through
		// UpdateGOS (e.g. in a test).
		gosPopulated: populatedBucketCount(&buckets),
	}, nil
}

// deriveScalarsFromBuckets recomputes the count and min/max scalars from a
// bucket store now that they are no longer carried on the wire. count is the
// exact sum of bucket counts. min/max are the representative values of the
// lowest/highest non-empty buckets (relative-accuracy bounded). An empty store
// yields count 0 and the +Inf/-Inf sentinels matching NewDDSketch, so a
// reconstructed empty sketch behaves identically to a freshly constructed one.
func deriveScalarsFromBuckets(b *Buckets, mapping IndexMapping) (count uint64, min, max float64) {
	min = math.Inf(1)
	max = math.Inf(-1)
	if b.IsEmpty() {
		return 0, min, max
	}
	counts := b.counts.AsSlice()
	for i, c := range counts {
		if c == 0 {
			continue
		}
		count += c
		k := b.offset + int32(i)
		rep := mapping.Value(k)
		if rep < min {
			min = rep
		}
		if rep > max {
			max = rep
		}
	}
	if count == 0 {
		// Store had only zero-count buckets: treat as empty.
		min = math.Inf(1)
		max = math.Inf(-1)
	}
	return count, min, max
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
