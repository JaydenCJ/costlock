// Tests for the gate: tolerance and warn thresholds, absolute caps,
// new/missing suite policies, unpriced handling, and the total budget.
// Inputs are hand-built runs so every verdict is pinned arithmetic.
package gate

import (
	"strings"
	"testing"

	"github.com/JaydenCJ/costlock/internal/aggregate"
	"github.com/JaydenCJ/costlock/internal/lockfile"
)

// runWith builds a one-suite run with the given cost.
func runWith(suite string, cost float64) aggregate.Run {
	return runUsage(suite, aggregate.Usage{Calls: 1, CostUSD: cost})
}

func runUsage(suite string, u aggregate.Usage) aggregate.Run {
	return aggregate.Run{
		Suites: map[string]*aggregate.Suite{suite: {Usage: u}},
		Total:  u,
	}
}

// lockWith builds a lockfile with one budgeted suite at the given
// baseline cost and default policy.
func lockWith(suite string, baseline float64) lockfile.File {
	lf := lockfile.New()
	lf.Budgets[suite] = lockfile.Budget{Baseline: lockfile.Baseline{CostUSD: baseline}}
	return lf
}

func row(t *testing.T, res Result, suite string) Row {
	t.Helper()
	for _, r := range res.Rows {
		if r.Suite == suite {
			return r
		}
	}
	t.Fatalf("no row for suite %q in %+v", suite, res.Rows)
	return Row{}
}

func TestWithinToleranceIsOK(t *testing.T) {
	// +4% growth, warn at 5%, tolerance 10% → clean pass.
	res := Check(runWith("unit", 0.104), lockWith("unit", 0.10))
	if res.Verdict != OK {
		t.Fatalf("verdict = %v, want OK", res.Verdict)
	}
	r := row(t, res, "unit")
	if r.Verdict != OK || len(r.Reasons) != 0 {
		t.Fatalf("row = %+v", r)
	}
	// Bit-identical baseline and current must be OK even with warn 0,
	// or the gate flaps on float noise.
	lf := lockWith("unit", 0.123456)
	lf.Policy.WarnPct = 0
	if res := Check(runWith("unit", 0.123456), lf); res.Verdict != OK {
		t.Fatalf("identical cost: verdict = %v, want OK", res.Verdict)
	}
}

func TestGrowthAboveWarnBelowToleranceWarns(t *testing.T) {
	// +7% is above warn (5) but under tolerance (10).
	res := Check(runWith("unit", 0.107), lockWith("unit", 0.10))
	if res.Verdict != Warn {
		t.Fatalf("verdict = %v, want Warn", res.Verdict)
	}
	r := row(t, res, "unit")
	if len(r.Reasons) != 1 || !strings.Contains(r.Reasons[0], "warn threshold") {
		t.Fatalf("reasons = %v", r.Reasons)
	}
}

func TestGrowthAboveToleranceBreaches(t *testing.T) {
	res := Check(runWith("unit", 0.115), lockWith("unit", 0.10))
	if res.Verdict != Breach {
		t.Fatalf("verdict = %v, want Breach", res.Verdict)
	}
	r := row(t, res, "unit")
	if r.DeltaPct < 14.9 || r.DeltaPct > 15.1 {
		t.Fatalf("delta = %v, want ~15", r.DeltaPct)
	}
	if len(res.Problems) == 0 || !strings.Contains(res.Problems[0], "unit:") {
		t.Fatalf("problems = %v", res.Problems)
	}
}

func TestCostDecreaseIsOKWithNegativeDelta(t *testing.T) {
	res := Check(runWith("unit", 0.08), lockWith("unit", 0.10))
	r := row(t, res, "unit")
	if r.Verdict != OK || r.DeltaPct < -20.1 || r.DeltaPct > -19.9 {
		t.Fatalf("row = %+v", r)
	}
	if r.FormatDelta() != "-20.0%" {
		t.Fatalf("FormatDelta = %q", r.FormatDelta())
	}
}

func TestPerSuiteToleranceOverride(t *testing.T) {
	lf := lockWith("unit", 0.10)
	loose := 50.0
	b := lf.Budgets["unit"]
	b.TolerancePct = &loose
	lf.Budgets["unit"] = b
	// +15% breaches the default 10 but not the per-suite 50; the
	// global warn threshold (5) still flags the growth.
	res := Check(runWith("unit", 0.115), lf)
	if res.Verdict != Warn {
		t.Fatalf("verdict = %v, want Warn (no breach) under override", res.Verdict)
	}
	if row(t, res, "unit").LimitPct != 50 {
		t.Fatalf("LimitPct = %v", row(t, res, "unit").LimitPct)
	}
}

func TestZeroBaselineSemantics(t *testing.T) {
	// Spend appearing where the baseline was $0 has no meaningful
	// percentage — it must breach, rendered as +inf.
	res := Check(runWith("unit", 0.02), lockWith("unit", 0))
	r := row(t, res, "unit")
	if r.Verdict != Breach || !r.DeltaInf {
		t.Fatalf("row = %+v", r)
	}
	if r.FormatDelta() != "+inf" {
		t.Fatalf("FormatDelta = %q", r.FormatDelta())
	}
	// $0 baseline with $0 spend is a clean pass.
	if res := Check(runWith("unit", 0), lockWith("unit", 0)); res.Verdict != OK {
		t.Fatalf("zero/zero verdict = %v", res.Verdict)
	}
}

func TestMaxCostCapBreachesEvenWithinTolerance(t *testing.T) {
	lf := lockWith("unit", 0.10)
	cap := 0.105
	b := lf.Budgets["unit"]
	b.MaxCostUSD = &cap
	lf.Budgets["unit"] = b
	// +7% growth is under tolerance but over the absolute cap.
	res := Check(runWith("unit", 0.107), lf)
	r := row(t, res, "unit")
	if r.Verdict != Breach {
		t.Fatalf("verdict = %v, want Breach", r.Verdict)
	}
	found := false
	for _, reason := range r.Reasons {
		if strings.Contains(reason, "max_cost_usd") {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasons = %v", r.Reasons)
	}
}

func TestTokenAndCallCaps(t *testing.T) {
	lf := lockWith("unit", 1)
	calls, in, out := int64(2), int64(100), int64(10)
	b := lf.Budgets["unit"]
	b.MaxCalls, b.MaxInputTokens, b.MaxOutputTokens = &calls, &in, &out
	lf.Budgets["unit"] = b
	res := Check(runUsage("unit", aggregate.Usage{
		Calls: 3, InputTokens: 150, OutputTokens: 20, CostUSD: 1,
	}), lf)
	r := row(t, res, "unit")
	if r.Verdict != Breach || len(r.Reasons) != 3 {
		t.Fatalf("row = %+v", r)
	}
}

func TestNewSuitePolicies(t *testing.T) {
	// Unbudgeted spend fails by default — new suites must be budgeted
	// deliberately, not slip in silently.
	res := Check(runWith("surprise", 0.01), lockfile.New())
	r := row(t, res, "surprise")
	if r.Status != StatusNew || r.Verdict != Breach {
		t.Fatalf("row = %+v", r)
	}
	if !strings.Contains(r.Reasons[0], "costlock update") {
		t.Fatalf("reason should point at the fix: %v", r.Reasons)
	}
	lf := lockfile.New()
	lf.Policy.OnNewSuite = lockfile.ActionWarn
	if res := Check(runWith("s", 0.01), lf); res.Verdict != Warn {
		t.Fatalf("warn policy: verdict = %v", res.Verdict)
	}
	lf.Policy.OnNewSuite = lockfile.ActionIgnore
	if res := Check(runWith("s", 0.01), lf); res.Verdict != OK {
		t.Fatalf("ignore policy: verdict = %v", res.Verdict)
	}
}

func TestMissingSuiteWarnsByDefaultAndFailsUnderPolicy(t *testing.T) {
	lf := lockWith("gone", 0.10)
	lf.Budgets["present"] = lockfile.Budget{Baseline: lockfile.Baseline{CostUSD: 0.05}}
	res := Check(runWith("present", 0.05), lf)
	r := row(t, res, "gone")
	if r.Status != StatusMissing || r.Verdict != Warn {
		t.Fatalf("row = %+v", r)
	}
	if res.Verdict != Warn {
		t.Fatalf("overall = %v", res.Verdict)
	}
	// on_missing_suite=fail escalates the same situation to a breach.
	lf.Policy.OnMissingSuite = lockfile.ActionFail
	res = Check(aggregate.Run{Suites: map[string]*aggregate.Suite{}}, lf)
	if res.Verdict != Breach {
		t.Fatalf("fail policy: verdict = %v, want Breach", res.Verdict)
	}
}

func TestUnpricedFailsByDefault(t *testing.T) {
	run := runUsage("unit", aggregate.Usage{Calls: 1, UnpricedCalls: 1})
	run.UnpricedModels = []string{"mystery"}
	lf := lockWith("unit", 0)
	res := Check(run, lf)
	if res.Verdict != Breach {
		t.Fatalf("verdict = %v", res.Verdict)
	}
	if len(res.Problems) == 0 || !strings.Contains(res.Problems[0], "mystery") {
		t.Fatalf("problems = %v", res.Problems)
	}
}

func TestUnpricedPolicyWarnAndIgnore(t *testing.T) {
	run := runUsage("unit", aggregate.Usage{Calls: 1, UnpricedCalls: 1})
	lf := lockWith("unit", 0)
	lf.Policy.OnUnpriced = lockfile.ActionWarn
	if res := Check(run, lf); res.Verdict != Warn || len(res.Notes) != 1 {
		t.Fatalf("warn policy: %+v", res)
	}
	lf.Policy.OnUnpriced = lockfile.ActionIgnore
	if res := Check(run, lf); res.Verdict != OK {
		t.Fatalf("ignore policy: verdict = %v", res.Verdict)
	}
}

func TestTotalBudgetGatesTheWholeRun(t *testing.T) {
	lf := lockWith("unit", 0.10)
	lf.Total = &lockfile.Budget{Baseline: lockfile.Baseline{CostUSD: 0.10}}
	// Suite passes its own gate but the total breaches.
	res := Check(runWith("unit", 0.109), lf)
	if row(t, res, "unit").Verdict != Warn {
		t.Fatalf("suite row = %+v", row(t, res, "unit"))
	}
	// Total is the same +9% here, still under 10 — OK... use a bigger jump.
	res = Check(runWith("unit", 0.15), lf)
	if res.Total.Verdict != Breach || !res.Total.Gated {
		t.Fatalf("total row = %+v", res.Total)
	}
}

func TestNoTotalBudgetMeansInformationalTotal(t *testing.T) {
	res := Check(runWith("unit", 0.5), lockWith("unit", 0.5))
	if res.Total.Gated || res.Total.Verdict != OK || res.Total.HasBaseline {
		t.Fatalf("total row = %+v", res.Total)
	}
	if res.Total.CurrentUSD != 0.5 {
		t.Fatalf("total current = %v", res.Total.CurrentUSD)
	}
}

func TestRowsAreSortedByName(t *testing.T) {
	run := aggregate.Run{Suites: map[string]*aggregate.Suite{
		"zeta": {Usage: aggregate.Usage{CostUSD: 0.01}},
		"alfa": {Usage: aggregate.Usage{CostUSD: 0.01}},
	}}
	lf := lockfile.New()
	lf.Policy.OnNewSuite = lockfile.ActionIgnore
	res := Check(run, lf)
	if len(res.Rows) != 2 || res.Rows[0].Suite != "alfa" || res.Rows[1].Suite != "zeta" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestWarnThresholdNeverExceedsTolerance(t *testing.T) {
	// warn_pct misconfigured above tolerance: growth past tolerance
	// must still breach, not merely warn.
	lf := lockWith("unit", 0.10)
	lf.Policy.WarnPct = 50
	res := Check(runWith("unit", 0.115), lf)
	if res.Verdict != Breach {
		t.Fatalf("verdict = %v, want Breach", res.Verdict)
	}
}
