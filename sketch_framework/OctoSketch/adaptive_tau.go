package octosketch

import (
	"math"
	"sync/atomic"
	"time"
)

// TauConfig holds all parameters for the adaptive threshold controller.
type TauConfig struct {
	Initial     float64       // starting τ value
	Min         float64       // floor: τ is never decreased below this
	Max         float64       // ceiling: τ is never increased above this
	Step        float64       // amount added/subtracted per adjustment tick
	TargetQueue int           // target queue length Q (must be > 0)
	Deadband    int           // tolerance around target queue before adjusting
	Interval    time.Duration // how often to re-evaluate queue depth

	// Legacy knobs kept for backward compatibility with existing call sites/tests.
	// If TargetQueue is unset (<= 0), NewAdaptiveTau derives TargetQueue/Deadband
	// from these bounds.
	UpperBound int // deprecated: queue depth above which τ is increased
	LowerBound int // deprecated: queue depth below which τ is decreased
}

// DefaultTauConfig returns a TauConfig with queue-relative defaults.
// By default:
//   - TargetQueue = deltaBufSize / 3
//   - Deadband    = deltaBufSize / 6
//   - Interval    = 1ms
//   - Step        = adaptive (larger buffers use larger steps)
func DefaultTauConfig(initial float64, deltaBufSize int) TauConfig {
	if deltaBufSize <= 0 {
		deltaBufSize = 1
	}
	target := deltaBufSize / 3
	if target < 1 {
		target = 1
	}
	deadband := deltaBufSize / 6
	if deadband < 1 {
		deadband = 1
	}
	step := float64(deltaBufSize) / 4096.0
	if step < 1 {
		step = 1
	}
	return TauConfig{
		Initial:     initial,
		Min:         1,
		Max:         initial * 10,
		Step:        step,
		TargetQueue: target,
		Deadband:    deadband,
		Interval:    1 * time.Millisecond,
	}
}

// FixedTauConfig returns a TauConfig where τ never changes.
// Use this for deterministic tests or when adaptive behaviour is not needed.
func FixedTauConfig(value float64) TauConfig {
	return TauConfig{
		Initial:     value,
		Min:         value,
		Max:         value,
		Step:        0,
		TargetQueue: 10,
		Deadband:    0,
		Interval:    time.Hour, // effectively never fires
	}
}

// AdaptiveTau is a concurrency-safe, dynamically-adjusting threshold τ.
//
// Workers call Current() on each insert through an atomic load.
// TauController calls Adjust() periodically based on observed queue depth.
//
// Adjustment rule (matches adaptive resource-allocation policy):
//
//	Q̂(t+1) = Q(t) + (Q(t) - Q(t-1))
//	if Q̂(t+1) > targetQ + deadband: τ = min(τ + Step, Max) // reduce send rate
//	if Q̂(t+1) < targetQ - deadband: τ = max(τ - Step, Min) // increase send rate
type AdaptiveTau struct {
	current atomic.Uint64
	cfg     TauConfig
	prevQ   atomic.Int64
	hasPrev atomic.Bool
}

// NewAdaptiveTau creates an AdaptiveTau starting at cfg.Initial.
func NewAdaptiveTau(cfg TauConfig) *AdaptiveTau {
	cfg = normalizeTauConfig(cfg)
	a := &AdaptiveTau{cfg: cfg}
	a.current.Store(math.Float64bits(cfg.Initial))
	return a
}

// Current returns the current τ value via a single atomic load.
func (a *AdaptiveTau) Current() float64 {
	return math.Float64frombits(a.current.Load())
}

// Adjust updates τ based on observed queue depth and its short-term trend.
// It uses:
//
//	Qhat(t+1) = Qt + (Qt - Q(t-1))
//
// and compares Qhat(t+1) against target queue Q.
func (a *AdaptiveTau) Adjust(queueLen int) {
	if a.cfg.Step == 0 {
		return // fixed tau — nothing to do
	}
	if queueLen < 0 {
		queueLen = 0
	}
	prev := queueLen
	if a.hasPrev.Load() {
		prev = int(a.prevQ.Load())
	}
	predicted := 2*queueLen - prev
	a.prevQ.Store(int64(queueLen))
	a.hasPrev.Store(true)

	upper := a.cfg.TargetQueue + a.cfg.Deadband
	lower := a.cfg.TargetQueue - a.cfg.Deadband
	dir := 0
	if predicted > upper {
		dir = 1
	} else if predicted < lower {
		dir = -1
	} else {
		return
	}

	for {
		curBits := a.current.Load()
		cur := math.Float64frombits(curBits)
		next := cur

		if dir > 0 {
			next += a.cfg.Step
			if next > a.cfg.Max {
				next = a.cfg.Max
			}
		} else {
			next -= a.cfg.Step
			if next < a.cfg.Min {
				next = a.cfg.Min
			}
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

func normalizeTauConfig(cfg TauConfig) TauConfig {
	if cfg.Min <= 0 {
		cfg.Min = 1
	}
	if cfg.Max < cfg.Min {
		cfg.Max = cfg.Min
	}
	if cfg.Initial < cfg.Min {
		cfg.Initial = cfg.Min
	}
	if cfg.Initial > cfg.Max {
		cfg.Initial = cfg.Max
	}
	if cfg.Step < 0 {
		cfg.Step = -cfg.Step
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 100 * time.Microsecond
	}
	if cfg.TargetQueue <= 0 {
		if cfg.UpperBound > cfg.LowerBound {
			cfg.TargetQueue = (cfg.UpperBound + cfg.LowerBound) / 2
			cfg.Deadband = (cfg.UpperBound - cfg.LowerBound) / 2
		} else {
			cfg.TargetQueue = 10
		}
	}
	if cfg.TargetQueue <= 0 {
		cfg.TargetQueue = 1
	}
	if cfg.Deadband < 0 {
		cfg.Deadband = 0
	}
	return cfg
}

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
