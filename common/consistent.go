package common

// RowSampler is the admission interface consumed by the per-row sampled sketch
// updates (CountSketch.UpdateStringSampledPerRow, CountMinSketch.
// InsertWithHashSampledPerRow). The caller invokes BeginItem() exactly once per
// stream item, then Admit() exactly once per row in row order (0..d-1).
//
// Two implementations:
//   - *GeometricSampler — NitroSketch skip-sampling over the flattened
//     (item,row) candidate stream. Stateful (RNG skip counter); cheapest RNG.
//   - *ConsistentSampler — stateless hash decision per (occurrence, row).
//     Deterministic given (seed, occurrence id): ANY pipeline stage that knows
//     the same (seed, occ) recomputes the identical admitted-row set, so the
//     sampling decision has ONE owner regardless of where it is evaluated and
//     double application is idempotent (see design §3.1, single-location
//     sampling).
type RowSampler interface {
	BeginItem()
	Admit() bool
	P() float64
}

// consistentSampleSalt decorrelates row-admission decisions from every other
// splitmix64 use of the same seed material (e.g. HLL's hllSampleSalt keyed
// threshold sampling).
const consistentSampleSalt uint64 = 0xC2B2AE3D27D4EB4F

// consistentRowStride spreads the row index across the 64-bit state so
// (occ, row) and (occ+1, row-d) never collide. Any large odd constant works.
const consistentRowStride uint64 = 0xD6E8FEB86659FD93

// ConsistentAdmit is the STATELESS per-row admission decision:
//
//	admit(seed, occ, row) ⇔ U(seed, occ, row) < p
//
// where U is a splitmix64-derived uniform in [0,1). It is a pure function —
// the same (seed, occ, row, p) always yields the same answer — which is what
// makes the sampling decision location-independent: the SDK aggregator, the
// wire-level otlpfilter, and the collector wrapper can each evaluate it and
// agree exactly, so at most ONE stage ever drops/weights an update and
// re-evaluation elsewhere is a no-op rather than a second dilution.
//
// occ is the per-series occurrence id of the item. It must vary per
// OCCURRENCE (a monotonic counter, or a wire-visible identity such as the
// datapoint's TimeUnixNano) — never per key alone, or a key's admissions
// become all-or-nothing and the per-row decorrelation of design §3.2 is lost.
func ConsistentAdmit(seed, occ uint64, row int, p float64) bool {
	if p >= 1.0 {
		return true
	}
	if p <= 0 {
		return false
	}
	mixed := splitmix64Finalize((seed ^ consistentSampleSalt) + occ*0x9E3779B97F4A7C15 + uint64(row)*consistentRowStride)
	u := float64(mixed>>11) * (1.0 / 9007199254740992.0) // 2^53 → [0,1)
	return u < p
}

// ConsistentSampler is the per-series cursor over (occurrence, row) candidates
// implementing RowSampler on top of ConsistentAdmit. Unlike GeometricSampler it
// holds NO RNG state — only the position cursor — so decisions are reproducible
// from (seed, occurrence id) alone and survive process restarts, replays, and
// recomputation at other pipeline stages.
//
// Seed the sampler per series (e.g. FNV-1a of the series key) and salt the seed
// with the window start so admission patterns decorrelate across windows.
// Not safe for concurrent use; keep one per series like GeometricSampler.
type ConsistentSampler struct {
	p    float64
	seed uint64
	occ  uint64
	row  int
}

// NewConsistentSampler returns a sampler admitting each (item,row) candidate
// with probability p. p >= 1 admits everything (exact); p <= 0 is clamped to
// a tiny positive probability (mirroring GeometricSampler.Reset).
func NewConsistentSampler(p float64, seed uint64) *ConsistentSampler {
	s := &ConsistentSampler{}
	s.Reset(p, seed)
	return s
}

// Reset reconfigures probability and seed and rewinds the occurrence cursor.
func (s *ConsistentSampler) Reset(p float64, seed uint64) {
	if p >= 1.0 {
		p = 1.0
	} else if p <= 0 {
		p = 5e-324 // smallest positive float64, mirrors GeometricSampler
	}
	s.p = p
	s.seed = seed
	s.occ = 0
	s.row = 0
}

// BeginItem advances to the next occurrence and rewinds the row cursor.
// Call exactly once per stream item, before the per-row Admit() calls.
func (s *ConsistentSampler) BeginItem() {
	if s == nil {
		return
	}
	s.occ++
	s.row = 0
}

// SetOccurrence pins the occurrence id explicitly (wire-derived identity such
// as a datapoint's TimeUnixNano) instead of the internal counter, and rewinds
// the row cursor. Use this at stages that recompute another stage's decision.
func (s *ConsistentSampler) SetOccurrence(occ uint64) {
	s.occ = occ
	s.row = 0
}

// Admit reports the decision for the current (occurrence, row) candidate and
// advances the row cursor. Nil-safe: a nil sampler admits everything.
func (s *ConsistentSampler) Admit() bool {
	if s == nil {
		return true
	}
	r := s.row
	s.row++
	return ConsistentAdmit(s.seed, s.occ, r, s.p)
}

// P returns the configured admission probability. Nil-safe: 1.0 (exact).
func (s *ConsistentSampler) P() float64 {
	if s == nil {
		return 1.0
	}
	return s.p
}
