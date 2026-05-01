package budget_test

import (
	"testing"
	"time"

	"github.com/whiskeyjimbo/paras/internal/infra/semantic/budget"
)

func TestBudgetNoBreachWithinLimits(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	b := budget.NewWithClock("vault1", budget.Config{
		DailyVendorTokens: 100,
		DailyVendorCalls:  10,
		DailyLocalJobs:    50,
		DailyLocalCPUSecs: 3600,
	}, clk.Now)

	if breach := b.Record(budget.MetricVendorTokens, 50); breach != nil {
		t.Fatalf("unexpected breach: %+v", breach)
	}
	if b.IsTripped() {
		t.Fatal("circuit breaker should be closed when within limits")
	}
}

func TestBudgetBreachTripsCircuit(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	b := budget.NewWithClock("vault1", budget.Config{
		DailyVendorTokens: 10,
		DailyVendorCalls:  2,
		DailyLocalJobs:    5,
		DailyLocalCPUSecs: 1,
	}, clk.Now)

	b.Record(budget.MetricVendorTokens, 9)
	if b.IsTripped() {
		t.Fatal("should not be tripped yet")
	}

	breach := b.Record(budget.MetricVendorTokens, 2) // total 11 > 10
	if breach == nil {
		t.Fatal("expected breach")
	}
	if breach.Metric != budget.MetricVendorTokens {
		t.Fatalf("wrong metric: got %q", breach.Metric)
	}
	if !b.IsTripped() {
		t.Fatal("circuit breaker should be open after breach")
	}
}

func TestBudgetAutoRecoveryOnWindowRoll(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	b := budget.NewWithClock("vault1", budget.Config{
		DailyVendorTokens: 10,
		DailyVendorCalls:  100,
		DailyLocalJobs:    100,
		DailyLocalCPUSecs: 3600,
	}, clk.Now)

	b.Record(budget.MetricVendorTokens, 11) // breach
	if !b.IsTripped() {
		t.Fatal("should be tripped")
	}

	// Advance clock past 24h so window evicts the entry.
	clk.now = clk.now.Add(25 * time.Hour)
	b.Record(budget.MetricVendorTokens, 1) // re-evaluate after eviction

	if b.IsTripped() {
		t.Fatal("circuit breaker should auto-recover after window roll")
	}
}

func TestBudgetHeadroomResetsAt(t *testing.T) {
	now := time.Now()
	clk := &fakeClock{now: now}
	b := budget.NewWithClock("vault1", budget.DefaultConfig(), clk.Now)

	b.Record(budget.MetricVendorTokens, 100)
	h := b.Headroom()

	if h.ResetsAt.IsZero() {
		t.Fatal("ResetsAt should be set after a record")
	}
	if h.ResetsAt.Before(now) {
		t.Fatalf("ResetsAt %v should be in the future", h.ResetsAt)
	}
}

func TestBudgetDerivationPausedLexicalUnaffected(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	b := budget.NewWithClock("vault1", budget.Config{
		DailyVendorTokens: 1,
		DailyVendorCalls:  100,
		DailyLocalJobs:    100,
		DailyLocalCPUSecs: 3600,
	}, clk.Now)

	b.Record(budget.MetricVendorTokens, 2) // trip
	if !b.IsTripped() {
		t.Fatal("should be tripped")
	}

	// Derivation should be paused, but reads/lexical search are unaffected.
	// The budget does not touch the vault read path; callers check DerivationPaused().
	if !b.DerivationPaused() {
		t.Fatal("derivation should be paused when circuit is open")
	}
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }
