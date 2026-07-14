// Markdown renderers: PR-comment-ready tables for check and report.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/JaydenCJ/costlock/internal/aggregate"
	"github.com/JaydenCJ/costlock/internal/gate"
)

// CheckMarkdown writes the check verdict as a Markdown fragment,
// designed to be posted verbatim as a PR comment.
func CheckMarkdown(w io.Writer, res gate.Result, run aggregate.Run) {
	verdict := "PASS ✅"
	if res.Verdict == gate.Breach {
		verdict = "FAIL ❌"
	} else if res.Verdict == gate.Warn {
		verdict = "PASS ⚠️"
	}
	fmt.Fprintf(w, "### costlock check — %s\n\n", verdict)
	fmt.Fprintf(w, "%d source(s), %d record(s)\n\n", run.Sources, run.Records)
	fmt.Fprintln(w, "| Suite | Baseline | Current | Δ | Limit | Verdict |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---|")
	rows := append([]gate.Row{}, res.Rows...)
	rows = append(rows, res.Total)
	for _, r := range rows {
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s |\n",
			mdSuite(r), baselineCell(r), currentCell(r),
			r.FormatDelta(), limitCell(r), mdVerdict(r.Verdict))
	}
	if len(res.Notes) > 0 || len(res.Problems) > 0 {
		fmt.Fprintln(w)
		for _, n := range res.Notes {
			fmt.Fprintf(w, "- ⚠️ %s\n", n)
		}
		for _, p := range res.Problems {
			fmt.Fprintf(w, "- ❌ %s\n", p)
		}
	}
}

// ReportMarkdown writes the ungated run summary as Markdown tables.
func ReportMarkdown(w io.Writer, run aggregate.Run) {
	fmt.Fprintf(w, "### costlock report\n\n")
	fmt.Fprintf(w, "%d source(s), %d record(s) — total **%s** across %s call(s)\n\n",
		run.Sources, run.Records, usd(run.Total.CostUSD), groupInt(run.Total.Calls))
	fmt.Fprintln(w, "| Suite | Calls | Input tokens | Output tokens | Cost |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|")
	for _, name := range run.SuiteNames() {
		s := run.Suites[name]
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n",
			name, groupInt(s.Calls), groupInt(s.InputTokens),
			groupInt(s.OutputTokens), usd(s.CostUSD))
	}
	if run.Total.UnpricedCalls > 0 {
		fmt.Fprintf(w, "\n⚠️ %d unpriced call(s): %s\n",
			run.Total.UnpricedCalls, strings.Join(run.UnpricedModels, ", "))
	}
}

func mdSuite(r gate.Row) string {
	switch r.Status {
	case gate.StatusNew:
		return r.Suite + " *(new)*"
	case gate.StatusMissing:
		return r.Suite + " *(missing)*"
	default:
		return r.Suite
	}
}

func mdVerdict(v gate.Verdict) string {
	switch v {
	case gate.Breach:
		return "❌ breach"
	case gate.Warn:
		return "⚠️ warn"
	default:
		return "✅ ok"
	}
}
