package octosketch

import (
	"math"
	"sync/atomic"
	"time"
)

// TauConfig holds all parameters for the adaptive threshold controller.
type TauConfig struct {
	Initial    float64       // starting τ value
	Min        float64       // floor: τ is never decreased below this
	Max        float64       // ceiling: τ is never increased above this
	Step       float64       // amount added/subtracted per adjustment tick
	UpperBound int           // queue depth above which τ is increased (backpressure)
	LowerBound int           // queue depth below which τ is decreased (latency)
	Interval   time.Duration // how often to re-evaluate queue depth
}

// DefaultTauConfig returns a TauConfig with sensible defaults for a delta channel
// whose buffer is deltaBufSize. The bounds are set to 10% and 60% of the buffer.
func DefaultTauConfig(initial float64, deltaBufSize int) TauConfig {
	return TauConfig{
		Initial:    initial,
		Min:        1,
		Max:        initial * 10,
		Step:       1,
		UpperBound: deltaBufSize * 6 / 10, // 60% full → back off
		LowerBound: deltaBufSize * 1 / 10, // 10% full → be more eager
		Interval:   50 * time.Millisecond,
	}
}

// FixedTauConfig returns a TauConfig where τ never changes.
// Use this for deterministic tests or when adaptive behaviour is not needed.
func FixedTauConfig(value float64) TauConfig {
	return TauConfig{
		Initial:    value,
		Min:        value,
		Max:        value,
		Step:       0,
		UpperBound: 0,
		LowerBound: 0,
		Interval:   time.Hour, // effectively never fires
	}
}

// AdaptiveTau is a concurrency-safe, dynamically-adjusting threshold τ.
//
// Workers call Current() on each insert through an atomic load.
// TauController calls Adjust() periodically based on observed queue depth.
//
// Adjustment rule (matches spec):
//
//	if queueLen > UpperBound  →  τ = min(τ + Step, Max)   // slow down emission
//	if queueLen < LowerBound  →  τ = max(τ - Step, Min)   // speed up emission
type AdaptiveTau struct {
	current atomic.Uint64
	cfg     TauConfig
}

// NewAdaptiveTau creates an AdaptiveTau starting at cfg.Initial.
func NewAdaptiveTau(cfg TauConfig) *AdaptiveTau {
	a := &AdaptiveTau{cfg: cfg}
	a.current.Store(math.Float64bits(cfg.Initial))
	return a
}

// Current returns the current τ value via a single atomic load.
func (a *AdaptiveTau) Current() float64 {
	return math.Float64frombits(a.current.Load())
}

// Adjust updates τ based on observed queue depth.
// Implements the spec's if/else rule with clamping to [Min, Max].
func (a *AdaptiveTau) Adjust(queueLen int) {
	if a.cfg.Step == 0 {
		return // fixed tau — nothing to do
	}

	for {
		curBits := a.current.Load()
		cur := math.Float64frombits(curBits)
		next := cur

		if queueLen > a.cfg.UpperBound {
			next += a.cfg.Step
			if next > a.cfg.Max {
				next = a.cfg.Max
			}
		} else if queueLen < a.cfg.LowerBound {
			next -= a.cfg.Step
			if next < a.cfg.Min {
				next = a.cfg.Min
			}
		} else {
			return
		}

		if next == cur {
			return
		}
		if a.current.CompareAndSwap(curBits, math.Float64bits(next)) {
			return
		}
	}
}

// Config returns the configuration used at creation time.
func (a *AdaptiveTau) Config() TauConfig { return a.cfg }

// ─────────────────────────────────────────────────────────────────────────────
// TauController
// ─────────────────────────────────────────────────────────────────────────────

// TauController monitors the shared delta channel depth and adjusts τ on each tick.
// One controller is typically shared across all workers in a system.
type TauController struct {
	tau      *AdaptiveTau
	queueLen func() int
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// NewTauController creates a controller that observes an aggregate queue depth.
func NewTauController(tau *AdaptiveTau, queueLen func() int) *TauController {
	return &TauController{
		tau:      tau,
		queueLen: queueLen,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Run starts the background goroutine that calls tau.Adjust on each ticker tick.
func (c *TauController) Run() {
	go func() {
		defer close(c.doneCh)
		ticker := time.NewTicker(c.tau.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.tau.Adjust(c.queueLen())
			case <-c.stopCh:
				return
			}
		}
	}()
}

// Stop signals the controller to stop and blocks until it exits.
func (c *TauController) Stop() {
	close(c.stopCh)
	<-c.doneCh
}

// Done returns a channel that closes when the controller goroutine exits.
func (c *TauController) Done() <-chan struct{} { return c.doneCh }

// Tau returns the underlying AdaptiveTau so callers can read Current().
func (c *TauController) Tau() *AdaptiveTau { return c.tau }
