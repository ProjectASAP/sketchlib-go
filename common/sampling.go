package common

import (
	"math"
	"math/rand"
)

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
func (s *GeometricSampler) P() float64 { return s.p }

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
	if math.IsInf(g, 1) {
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
func (s *GeometricSampler) Admit() bool {
	if s.exact {
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
