package common

import (
	"math"
	"math/rand"
)

// RowSampler is the admission interface consumed by the per-row sampled sketch
// updates (CountSketch.UpdateStringSampledPerRow, CountMinSketch.
// InsertWithHashSampledPerRow). The caller invokes BeginItem() exactly once per
// stream item, then Admit() exactly once per row in row order (0..d-1).
//
// The sole implementation is *GeometricSampler — NitroSketch skip-sampling
// over the flattened (item,row) candidate stream. Stateful (RNG skip
// counter); cheapest RNG. Admission is decided exactly once, upstream of
// serialization (SDK-side), so no other pipeline stage re-derives it.
type RowSampler interface {
	BeginItem()
	Admit() bool
	P() float64
}

// RowMaskSampler consumes a whole item-sized block from the flattened
// (item,row) candidate stream at once. Implementations return a bit mask whose
// bit r is set exactly when row r is admitted. This is the direct-jump form of
// NitroSketch Algorithm 1: a gap that spans one or more complete items is
// consumed with one subtraction instead of d Admit calls per skipped item.
type RowMaskSampler interface {
	AdmitRows(rows int) uint64
}

// AdmitRows uses a sampler's direct block-jump implementation when available.
// The fallback preserves compatibility with other RowSampler implementations.
func AdmitRows(s RowSampler, rows int) uint64 {
	if rows <= 0 {
		return 0
	}
	if rows > 64 {
		rows = 64
	}
	// A nil sampler is the library-wide spelling of exact admission. Keep the
	// helper consistent with (*GeometricSampler)(nil).AdmitRows and with the
	// sampled sketch entry points, which all treat nil as unsampled/exact.
	if s == nil {
		if rows == 64 {
			return ^uint64(0)
		}
		return (uint64(1) << uint(rows)) - 1
	}
	if fast, ok := s.(RowMaskSampler); ok {
		return fast.AdmitRows(rows)
	}
	s.BeginItem()
	var mask uint64
	for r := 0; r < rows; r++ {
		if s.Admit() {
			mask |= uint64(1) << uint(r)
		}
	}
	return mask
}

// GeometricSampler implements NitroSketch-style geometric skip-sampling.
//
// Instead of flipping an independent Bernoulli(p) coin on every stream update
// (one RNG draw per item), the sampler draws a Geometric(p)-distributed number
// of updates to SKIP after each admitted update and jumps ahead. The sequence
// of admitted positions is statistically identical to per-update Bernoulli(p)
// admission — the gaps between successive kept items are i.i.d. Geometric(p) iff
// each item is kept independently with probability p — so every error bound and
// the "store raw sampled state + p, rescale ×1/p at query" composition rule are
// unchanged. The win is amortizing the sampling RNG to ~O(p) draws per item and
// dropping the per-item branch.
//
// With p == 1.0 the sampler is an exact NO-OP: Admit always returns true and
// never touches the RNG, so a sketch built with p == 1.0 is byte-identical to
// an unsampled sketch. This is the default and the safe rollout state.
//
// A GeometricSampler is NOT safe for concurrent use; per-series warm sketches
// keep their own sampler (see the design note on per-series counters).
type GeometricSampler struct {
	p       float64
	skip    int64 // remaining updates to skip before the next admitted one
	rng     *rand.Rand
	exact   bool // p >= 1.0 fast path
	lnOneMP float64
}

// NewGeometricSampler returns a sampler that admits each update with probability
// p in (0, 1]. Values <= 0 are clamped to a tiny positive probability and values
// > 1 are clamped to 1.0 (exact). The sampler seeds its own RNG from the
// supplied seed so producers are reproducible.
func NewGeometricSampler(p float64, seed int64) *GeometricSampler {
	s := &GeometricSampler{}
	s.Reset(p, seed)
	return s
}

// Reset reconfigures the sampler with a new probability and reseeds the RNG.
func (s *GeometricSampler) Reset(p float64, seed int64) {
	if p >= 1.0 || math.IsNaN(p) {
		s.p = 1.0
		s.exact = true
		s.skip = 0
		s.rng = nil
		s.lnOneMP = 0
		return
	}
	if p <= 0 {
		// Degenerate: clamp to a tiny positive probability rather than dropping
		// the entire stream (which would make the sketch unusable).
		p = math.SmallestNonzeroFloat64
	}
	s.p = p
	s.exact = false
	s.rng = rand.New(rand.NewSource(seed))
	s.lnOneMP = math.Log1p(-p) // ln(1 - p), always negative for 0 < p < 1
	s.skip = s.nextGap()
}

// P returns the configured sampling probability (1.0 when exact).
// Nil-safe: a nil sampler reports 1.0, so interface callers holding a typed-nil
// *GeometricSampler degrade to the exact (unsampled) path.
func (s *GeometricSampler) P() float64 {
	if s == nil {
		return 1.0
	}
	return s.p
}

// BeginItem marks a stream-item boundary (RowSampler). The geometric
// skip-sampler runs over the FLATTENED (item,row) candidate stream, so item
// boundaries carry no state here — this is a no-op kept for RowSampler
// interface parity.
func (s *GeometricSampler) BeginItem() {}

// AdmitRows consumes rows consecutive candidates from the flattened
// (item,row) stream and returns their admission mask. It is distributionally
// and seed-for-seed identical to calling Admit rows times, but skips a whole
// item in O(1) when the current geometric gap is at least rows. Work for an
// item is O(1 + number of admitted rows), matching NitroSketch's direct cursor
// jump rather than scanning every row.
func (s *GeometricSampler) AdmitRows(rows int) uint64 {
	if rows <= 0 {
		return 0
	}
	if rows > 64 {
		rows = 64
	}
	if s == nil || s.exact {
		if rows == 64 {
			return ^uint64(0)
		}
		return (uint64(1) << uint(rows)) - 1
	}

	block := int64(rows)
	if s.skip >= block {
		s.skip -= block
		return 0
	}

	position := uint64(s.skip)
	var mask uint64
	for {
		mask |= uint64(1) << position
		gap := uint64(s.nextGap())
		next := position + 1 + gap
		if next >= uint64(rows) {
			remaining := next - uint64(rows)
			if remaining > uint64(math.MaxInt64) {
				s.skip = math.MaxInt64
			} else {
				s.skip = int64(remaining)
			}
			return mask
		}
		position = next
	}
}

// IsExact reports whether the sampler admits every update (p == 1.0).
func (s *GeometricSampler) IsExact() bool { return s.exact }

// nextGap draws a Geometric(p) number of updates to skip before the next
// admitted update. P(G = k) = (1-p)^k * p for k >= 0, sampled via the inverse
// CDF G = floor(ln(U) / ln(1-p)) with U ~ Uniform(0,1].
func (s *GeometricSampler) nextGap() int64 {
	u := s.rng.Float64()
	if u <= 0 {
		u = math.SmallestNonzeroFloat64
	}
	g := math.Floor(math.Log(u) / s.lnOneMP)
	if g < 0 {
		g = 0
	}
	if math.IsInf(g, 1) || g >= float64(math.MaxInt64) {
		// p so small the gap overflows; cap to keep the counter finite.
		return math.MaxInt64
	}
	return int64(g)
}

// hllSampleSalt decorrelates the hash-threshold decision from any sketch's
// canonical register / cell hash. HLL samples by a value-determined threshold
// on the DISTINCT key (keep iff u(h(x)) < p) rather than a stream-position coin,
// so it must NOT reuse the register hash directly: using the same hash
// correlates the kept set with the register layout and biases the estimate.
// We mix the incoming canonical hash with this salt via a splitmix64-style
// finalizer to obtain an effectively independent uniform.
const hllSampleSalt uint64 = 0x9E3779B97F4A7C15

// splitmix64Finalize applies the splitmix64 avalanche finalizer, producing a
// well-distributed 64-bit value from x. Used to derive an independent uniform
// for hash-threshold sampling.
func splitmix64Finalize(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return x
}

// KeepKeyByThreshold reports whether a DISTINCT key whose canonical hash is
// `canonicalHash` is retained under hash-threshold sampling at probability p.
// A key is kept iff u(h(x)) < p where u is an independent uniform in [0,1)
// derived by re-mixing the canonical hash with an internal salt — so every
// distinct key gets the same Pr[kept] = p and the decision is stable (the same
// key is always kept or always dropped), which is required for unbiased
// cardinality estimation. This is value-determined sampling and is NOT geometric.
//
// With p >= 1.0 every key is kept (exact). p <= 0 keeps nothing.
func KeepKeyByThreshold(canonicalHash uint64, p float64) bool {
	if p >= 1.0 {
		return true
	}
	if p <= 0 {
		return false
	}
	mixed := splitmix64Finalize(canonicalHash ^ hllSampleSalt)
	// Map to [0,1): divide by 2^64. (mixed >> 11) gives 53 bits of mantissa.
	u := float64(mixed>>11) * (1.0 / 9007199254740992.0) // 2^53
	return u < p
}

// Admit reports whether the current update should be applied to the sketch.
// Call it exactly once per stream update. When it returns true the caller
// updates the sketch with the RAW (unscaled) weight — the ×1/p rescale is
// applied by the consumer at query time, never here.
//
// With p == 1.0 this is a single comparison and never draws from the RNG.
// Nil-safe: a nil sampler admits everything (exact).
func (s *GeometricSampler) Admit() bool {
	if s == nil || s.exact {
		return true
	}
	if s.skip > 0 {
		s.skip--
		return false
	}
	// This update is admitted; pre-draw the gap to the next admitted update.
	s.skip = s.nextGap()
	return true
}
