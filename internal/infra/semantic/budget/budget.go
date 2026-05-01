// Package budget implements a rolling 24h token-bucket per vault with a
// circuit breaker that pauses derivation on breach.
package budget

import (
	"sync"
	"time"
)

// Metric names a tracked counter in the rolling budget window.
type Metric string

const (
	MetricVendorTokens Metric = "vendor_tokens"
	MetricVendorCalls  Metric = "vendor_calls"
	MetricLocalJobs    Metric = "local_jobs"
	MetricLocalCPUSecs Metric = "local_cpu_secs"
)

// Config holds per-vault daily limits.
type Config struct {
	DailyVendorTokens int64
	DailyVendorCalls  int64
	DailyLocalJobs    int64
	DailyLocalCPUSecs float64
}

// DefaultConfig returns production-safe defaults.
func DefaultConfig() Config {
	return Config{
		DailyVendorTokens: 1_000_000,
		DailyVendorCalls:  2_000,
		DailyLocalJobs:    5_000,
		DailyLocalCPUSecs: 3_600,
	}
}

// Breach describes the metric that tripped the circuit breaker.
type Breach struct {
	Metric Metric
	Used   int64
	Limit  int64
}

// Headroom reports current remaining capacity and when the oldest window entry expires.
type Headroom struct {
	VendorTokensRemaining int64
	VendorCallsRemaining  int64
	LocalJobsRemaining    int64
	LocalCPUSecsRemaining float64
	ResetsAt              time.Time // when the oldest 24h entry expires
}

type entry struct {
	at     time.Time
	metric Metric
	amount int64
}

// Budget tracks a rolling 24h usage window and trips a circuit breaker on breach.
// All methods are safe for concurrent use.
type Budget struct {
	mu      sync.Mutex
	cfg     Config
	clock   func() time.Time
	window  []entry
	tripped bool
}

// New creates a Budget with the given config using wall-clock time.
func New(vaultID string, cfg Config) *Budget {
	return NewWithClock(vaultID, cfg, time.Now)
}

// NewWithClock creates a Budget with an injectable clock (for testing).
func NewWithClock(_ string, cfg Config, clock func() time.Time) *Budget {
	return &Budget{cfg: cfg, clock: clock}
}

// Record adds amount of metric to the rolling window, evicts stale entries, and
// checks limits. Returns a non-nil *Breach if any limit is exceeded; also trips
// the circuit breaker in that case. If limits are satisfied after eviction, the
// circuit breaker is automatically closed (recovery on window roll).
func (b *Budget) Record(m Metric, amount int64) *Breach {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clock()
	b.window = append(b.window, entry{at: now, metric: m, amount: amount})
	b.evict(now)

	totals := b.totals()
	breach := b.checkLimits(totals)
	b.tripped = breach != nil
	return breach
}

// IsTripped reports whether the circuit breaker is currently open.
func (b *Budget) IsTripped() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tripped
}

// DerivationPaused reports whether the pipeline should skip new derivation work.
// It is true whenever the circuit breaker is open.
func (b *Budget) DerivationPaused() bool { return b.IsTripped() }

// Headroom returns current remaining capacity across all metrics.
func (b *Budget) Headroom() Headroom {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clock()
	b.evict(now)
	t := b.totals()

	var resetsAt time.Time
	if len(b.window) > 0 {
		resetsAt = b.window[0].at.Add(24 * time.Hour)
	}

	return Headroom{
		VendorTokensRemaining: max(int64(0), b.cfg.DailyVendorTokens-t.vendorTokens),
		VendorCallsRemaining:  max(int64(0), b.cfg.DailyVendorCalls-t.vendorCalls),
		LocalJobsRemaining:    max(int64(0), b.cfg.DailyLocalJobs-t.localJobs),
		LocalCPUSecsRemaining: max(float64(0), b.cfg.DailyLocalCPUSecs-t.localCPUSecs),
		ResetsAt:              resetsAt,
	}
}

type windowTotals struct {
	vendorTokens int64
	vendorCalls  int64
	localJobs    int64
	localCPUSecs float64
}

// evict removes entries older than 24h. Requires entries are in non-decreasing
// time order (guaranteed because clock() is always called under mu).
func (b *Budget) evict(now time.Time) {
	cutoff := now.Add(-24 * time.Hour)
	i := 0
	for i < len(b.window) && b.window[i].at.Before(cutoff) {
		i++
	}
	b.window = b.window[i:]
}

func (b *Budget) totals() windowTotals {
	var t windowTotals
	for _, e := range b.window {
		switch e.metric {
		case MetricVendorTokens:
			t.vendorTokens += e.amount
		case MetricVendorCalls:
			t.vendorCalls += e.amount
		case MetricLocalJobs:
			t.localJobs += e.amount
		case MetricLocalCPUSecs:
			t.localCPUSecs += float64(e.amount)
		}
	}
	return t
}

func (b *Budget) checkLimits(t windowTotals) *Breach {
	if b.cfg.DailyVendorTokens > 0 && t.vendorTokens > b.cfg.DailyVendorTokens {
		return &Breach{Metric: MetricVendorTokens, Used: t.vendorTokens, Limit: b.cfg.DailyVendorTokens}
	}
	if b.cfg.DailyVendorCalls > 0 && t.vendorCalls > b.cfg.DailyVendorCalls {
		return &Breach{Metric: MetricVendorCalls, Used: t.vendorCalls, Limit: b.cfg.DailyVendorCalls}
	}
	if b.cfg.DailyLocalJobs > 0 && t.localJobs > b.cfg.DailyLocalJobs {
		return &Breach{Metric: MetricLocalJobs, Used: t.localJobs, Limit: b.cfg.DailyLocalJobs}
	}
	if b.cfg.DailyLocalCPUSecs > 0 && t.localCPUSecs > b.cfg.DailyLocalCPUSecs {
		used := int64(t.localCPUSecs)
		return &Breach{Metric: MetricLocalCPUSecs, Used: used, Limit: int64(b.cfg.DailyLocalCPUSecs)}
	}
	return nil
}
