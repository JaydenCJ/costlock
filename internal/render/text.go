// Terminal renderers: the check table CI logs show and the run report.
package render

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/JaydenCJ/costlock/internal/aggregate"
	"github.com/JaydenCJ/costlock/internal/gate"
)

// CheckText writes the check verdict table plus a reason list and the
// final PASS/FAIL line.
func CheckText(w io.Writer, res gate.Result, lockPath string, run aggregate.Run) {
	fmt.Fprintf(w, "costlock check — %s vs %d source(s), %d record(s)\n\n",
		lockPath, run.Sources, run.Records)

	rows := append([]gate.Row{}, res.Rows...)
	rows = append(rows, res.Total)

	nameW := len("suite")
	for _, r := range rows {
		if len(r.Suite) > nameW {
			nameW = len(r.Suite)
		}
	}

	fmt.Fprintf(w, "%-*s  %10s  %10s  %8s  %8s  %s\n",
		nameW, "suite", "baseline", "current", "delta", "limit", "verdict")
	for _, r := range rows {
		fmt.Fprintf(w, "%-*s  %10s  %10s  %8s  %8s  %s\n",
			nameW, r.Suite,
			baselineCell(r), currentCell(r), r.FormatDelta(), limitCell(r), verdictCell(r))
	}

	if len(res.Notes) > 0 {
		fmt.Fprintln(w)
		for _, n := range res.Notes {
			fmt.Fprintf(w, "warn: %s\n", n)
		}
	}
	if len(res.Problems) > 0 {
		fmt.Fprintln(w)
		for _, p := range res.Problems {
			fmt.Fprintf(w, "breach: %s\n", p)
		}
	}
	fmt.Fprintln(w)
	if res.Verdict == gate.Breach {
		fmt.Fprintln(w, "check: FAIL")
	} else {
		fmt.Fprintln(w, "check: PASS")
	}
}

// ReportText writes the ungated run summary: totals, per-suite spend,
// and a per-model breakdown.
func ReportText(w io.Writer, run aggregate.Run) {
	fmt.Fprintf(w, "costlock report — %d source(s), %d record(s)\n\n", run.Sources, run.Records)
	fmt.Fprintf(w, "total cost   %s\n", usd(run.Total.CostUSD))
	fmt.Fprintf(w, "calls        %s\n", groupInt(run.Total.Calls))
	fmt.Fprintf(w, "tokens       %s in / %s out / %s cache-read / %s cache-write\n",
		groupInt(run.Total.InputTokens), groupInt(run.Total.OutputTokens),
		groupInt(run.Total.CacheReadTokens), groupInt(run.Total.CacheWriteTokens))
	if run.Total.UnpricedCalls > 0 {
		fmt.Fprintf(w, "unpriced     %d call(s) — models: %s\n",
			run.Total.UnpricedCalls, strings.Join(run.UnpricedModels, ", "))
	}

	suiteW := len("by suite")
	for _, name := range run.SuiteNames() {
		if len(name)+2 > suiteW {
			suiteW = len(name) + 2
		}
	}
	fmt.Fprintf(w, "\n%-*s  %7s  %12s\n", suiteW, "by suite", "calls", "cost")
	for _, name := range run.SuiteNames() {
		s := run.Suites[name]
		fmt.Fprintf(w, "  %-*s  %7s  %12s\n", suiteW-2, name,
			groupInt(s.Calls), usd(s.CostUSD))
	}

	models := map[string]*aggregate.Usage{}
	for _, name := range run.SuiteNames() {
		for m, u := range run.Suites[name].ByModel {
			t := models[m]
			if t == nil {
				t = &aggregate.Usage{}
				models[m] = t
			}
			t.Calls += u.Calls
			t.InputTokens += u.InputTokens
			t.OutputTokens += u.OutputTokens
			t.CostUSD += u.CostUSD
		}
	}
	names := make([]string, 0, len(models))
	modelW := len("by model")
	for m := range models {
		names = append(names, m)
		if len(m)+2 > modelW {
			modelW = len(m) + 2
		}
	}
	sort.Strings(names)
	fmt.Fprintf(w, "\n%-*s  %7s  %12s  %12s  %12s\n",
		modelW, "by model", "calls", "in tokens", "out tokens", "cost")
	for _, m := range names {
		u := models[m]
		fmt.Fprintf(w, "  %-*s  %7s  %12s  %12s  %12s\n", modelW-2, m,
			groupInt(u.Calls), groupInt(u.InputTokens), groupInt(u.OutputTokens),
			usd(u.CostUSD))
	}
}

func baselineCell(r gate.Row) string {
	if !r.HasBaseline {
		return "—"
	}
	return usd(r.BaselineUSD)
}

func currentCell(r gate.Row) string {
	if r.Status == gate.StatusMissing {
		return "—"
	}
	return usd(r.CurrentUSD)
}

// usd normalizes a dollar amount to the lockfile's six-decimal money
// precision before formatting it to four for display, so the same run
// never prints two different figures across subcommands.
func usd(v float64) string {
	return fmt.Sprintf("$%.4f", round6(v))
}

func limitCell(r gate.Row) string {
	if !r.Gated {
		return "—"
	}
	return fmt.Sprintf("+%.1f%%", r.LimitPct)
}

func verdictCell(r gate.Row) string {
	switch r.Status {
	case gate.StatusNew:
		return "NEW " + r.Verdict.String()
	case gate.StatusMissing:
		return "missing " + r.Verdict.String()
	default:
		return r.Verdict.String()
	}
}

// groupInt renders an integer with thousands separators (1,234,567).
func groupInt(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
