// Package gate compares an aggregated run against the committed
// lockfile and produces the verdict a CI job turns into an exit code.
// All checks are pure arithmetic over the two inputs; every breach and
// warning carries a human-readable reason.
package gate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JaydenCJ/costlock/internal/aggregate"
	"github.com/JaydenCJ/costlock/internal/lockfile"
)

// Verdict orders outcomes by severity so combining is a max().
type Verdict int

const (
	OK Verdict = iota
	Warn
	Breach
)

// String renders the verdict the way reports print it.
func (v Verdict) String() string {
	switch v {
	case Warn:
		return "warn"
	case Breach:
		return "BREACH"
	default:
		return "ok"
	}
}

// Row statuses beyond plain gating.
const (
	StatusGated   = "gated"   // budgeted suite present in the run
	StatusNew     = "new"     // suite in the run but not in the lockfile
	StatusMissing = "missing" // suite in the lockfile but not in the run
)

// Row is the check result for one suite (or the run total).
type Row struct {
	Suite   string
	Status  string
	Verdict Verdict
	// BaselineUSD / CurrentUSD are the compared costs. BaselineUSD is
	// meaningless for StatusNew rows (HasBaseline is false).
	HasBaseline bool
	BaselineUSD float64
	CurrentUSD  float64
	// DeltaPct is cost growth over baseline in percent; DeltaInf marks
	// spend appearing where the baseline was $0.
	DeltaPct float64
	DeltaInf bool
	// LimitPct is the effective tolerance applied; Gated is false when
	// no relative gate applied (e.g. an informational total row).
	LimitPct float64
	Gated    bool
	// Reasons explains every warning or breach on this row.
	Reasons []string
}

// Result is the full check outcome.
type Result struct {
	Rows []Row // per-suite, sorted by name
	// Total is the run-wide row. It is gated only when the lockfile
	// has a "total" budget; otherwise it is informational.
	Total Row
	// Notes are run-level warnings (e.g. unpriced models under
	// on_unpriced=warn).
	Notes []string
	// Problems flattens every breach reason, for one-line summaries.
	Problems []string
	Verdict  Verdict
}

// epsilon absorbs float noise in percent comparisons so a suite whose
// cost is bit-for-bit identical to baseline never flaps.
const epsilon = 1e-9

// Check gates run against lf.
func Check(run aggregate.Run, lf lockfile.File) Result {
	var res Result

	names := map[string]bool{}
	for n := range run.Suites {
		names[n] = true
	}
	for n := range lf.Budgets {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	for _, name := range sorted {
		var u *aggregate.Usage
		if s, ok := run.Suites[name]; ok {
			u = &s.Usage
		}
		var b *lockfile.Budget
		if bb, ok := lf.Budgets[name]; ok {
			b = &bb
		}
		row := evaluate(name, b, u, lf.Policy)
		res.Rows = append(res.Rows, row)
	}

	res.Total = evaluate("total", lf.Total, &run.Total, lf.Policy)
	if lf.Total == nil {
		// No total budget: the row is informational, never a verdict.
		res.Total.Gated = false
		res.Total.Verdict = OK
		res.Total.Reasons = nil
		res.Total.Status = StatusGated
		res.Total.HasBaseline = false
	}

	if run.Total.UnpricedCalls > 0 {
		msg := fmt.Sprintf("%d record(s) could not be priced (models: %s); add prices or record cost_usd",
			run.Total.UnpricedCalls, joinModels(run.UnpricedModels))
		switch lf.Policy.OnUnpriced {
		case lockfile.ActionFail:
			res.Problems = append(res.Problems, msg)
			res.Verdict = Breach
		case lockfile.ActionWarn:
			res.Notes = append(res.Notes, msg)
			if res.Verdict < Warn {
				res.Verdict = Warn
			}
		}
	}

	for _, row := range append(res.Rows, res.Total) {
		if row.Verdict > res.Verdict {
			res.Verdict = row.Verdict
		}
		switch row.Verdict {
		case Breach:
			for _, r := range row.Reasons {
				res.Problems = append(res.Problems, row.Suite+": "+r)
			}
		case Warn:
			for _, r := range row.Reasons {
				res.Notes = append(res.Notes, row.Suite+": "+r)
			}
		}
	}
	return res
}

// evaluate gates one suite. b == nil means the suite is unbudgeted;
// u == nil means the budgeted suite produced no records.
func evaluate(name string, b *lockfile.Budget, u *aggregate.Usage, pol lockfile.Policy) Row {
	row := Row{Suite: name, Status: StatusGated}

	if b == nil {
		row.Status = StatusNew
		row.CurrentUSD = lockfile.RoundCost(u.CostUSD)
		reason := fmt.Sprintf("suite is not in the lockfile (spent $%.4f); run `costlock update` to budget it", row.CurrentUSD)
		applyAction(&row, pol.OnNewSuite, reason)
		return row
	}

	row.HasBaseline = true
	row.BaselineUSD = b.Baseline.CostUSD
	row.LimitPct = pol.TolerancePct
	if b.TolerancePct != nil {
		row.LimitPct = *b.TolerancePct
	}
	row.Gated = true

	if u == nil {
		row.Status = StatusMissing
		reason := "suite produced no records in this run"
		applyAction(&row, pol.OnMissingSuite, reason)
		return row
	}

	// The current cost is normalized to the lockfile's money precision
	// up front, so the figures quoted here match what `costlock update`
	// would commit and what every renderer prints.
	row.CurrentUSD = lockfile.RoundCost(u.CostUSD)

	// Relative gate: growth over baseline vs tolerance.
	switch {
	case b.Baseline.CostUSD > 0:
		row.DeltaPct = (row.CurrentUSD - b.Baseline.CostUSD) / b.Baseline.CostUSD * 100
		warnAt := pol.WarnPct
		if warnAt > row.LimitPct {
			warnAt = row.LimitPct
		}
		if row.DeltaPct > row.LimitPct+epsilon {
			breach(&row, fmt.Sprintf("cost %s exceeds tolerance +%.1f%% ($%.4f → $%.4f)",
				formatDelta(row.DeltaPct, false), row.LimitPct, b.Baseline.CostUSD, row.CurrentUSD))
		} else if row.DeltaPct > warnAt+epsilon {
			warn(&row, fmt.Sprintf("cost %s is above the warn threshold +%.1f%%",
				formatDelta(row.DeltaPct, false), warnAt))
		}
	case row.CurrentUSD > 0:
		row.DeltaInf = true
		breach(&row, fmt.Sprintf("baseline is $0 but the run spent $%.4f; re-baseline with `costlock update`", row.CurrentUSD))
	}

	// Absolute caps, independent of the baseline.
	if b.MaxCostUSD != nil && row.CurrentUSD > *b.MaxCostUSD+epsilon {
		breach(&row, fmt.Sprintf("cost $%.4f exceeds max_cost_usd $%.4f", row.CurrentUSD, *b.MaxCostUSD))
	}
	if b.MaxCalls != nil && u.Calls > *b.MaxCalls {
		breach(&row, fmt.Sprintf("%d calls exceed max_calls %d", u.Calls, *b.MaxCalls))
	}
	if b.MaxInputTokens != nil && u.InputTokens > *b.MaxInputTokens {
		breach(&row, fmt.Sprintf("%d input tokens exceed max_input_tokens %d", u.InputTokens, *b.MaxInputTokens))
	}
	if b.MaxOutputTokens != nil && u.OutputTokens > *b.MaxOutputTokens {
		breach(&row, fmt.Sprintf("%d output tokens exceed max_output_tokens %d", u.OutputTokens, *b.MaxOutputTokens))
	}
	return row
}

func applyAction(row *Row, action, reason string) {
	switch action {
	case lockfile.ActionFail:
		breach(row, reason)
	case lockfile.ActionWarn:
		warn(row, reason)
	}
}

func breach(row *Row, reason string) {
	row.Verdict = Breach
	row.Reasons = append(row.Reasons, reason)
}

func warn(row *Row, reason string) {
	if row.Verdict < Warn {
		row.Verdict = Warn
	}
	row.Reasons = append(row.Reasons, reason)
}

// formatDelta renders a percent delta with an explicit sign, or "+inf"
// for spend over a $0 baseline.
func formatDelta(pct float64, inf bool) string {
	if inf {
		return "+inf"
	}
	return fmt.Sprintf("%+.1f%%", pct)
}

// FormatDelta is the exported renderer used by the report writers.
func (r Row) FormatDelta() string {
	if !r.HasBaseline || r.Status == StatusMissing {
		return "—"
	}
	return formatDelta(r.DeltaPct, r.DeltaInf)
}

func joinModels(models []string) string {
	if len(models) == 0 {
		return "none"
	}
	return strings.Join(models, ", ")
}
