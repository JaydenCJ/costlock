// Tests for the three renderers. Text and Markdown are asserted on
// stable substrings; JSON is decoded back and checked structurally so
// the schema stays honest.
package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaydenCJ/costlock/internal/aggregate"
	"github.com/JaydenCJ/costlock/internal/gate"
	"github.com/JaydenCJ/costlock/internal/lockfile"
	"github.com/JaydenCJ/costlock/internal/pricing"
	"github.com/JaydenCJ/costlock/internal/usage"
)

// fixture builds a two-suite run and a lockfile where "integration"
// breaches (+25% over baseline) and "unit" passes.
func fixture() (aggregate.Run, gate.Result) {
	prices := pricing.Table{"model-a": {InputPerMTok: 5, OutputPerMTok: 10}}
	recs := []usage.Record{
		{Suite: "integration", Model: "model-a", InputTokens: 20000, OutputTokens: 2500},
		{Suite: "unit", Model: "model-a", InputTokens: 4000, OutputTokens: 500},
	}
	run := aggregate.Build(recs, prices, true, 1)
	lf := lockfile.New()
	lf.Budgets["integration"] = lockfile.Budget{Baseline: lockfile.Baseline{CostUSD: 0.10}} // current 0.125
	lf.Budgets["unit"] = lockfile.Budget{Baseline: lockfile.Baseline{CostUSD: 0.025}}       // current 0.025
	return run, gate.Check(run, lf)
}

func TestCheckTextTableAndFailLine(t *testing.T) {
	run, res := fixture()
	var buf bytes.Buffer
	CheckText(&buf, res, "costlock.json", run)
	out := buf.String()
	for _, want := range []string{
		"costlock check — costlock.json",
		"suite", "baseline", "current", "delta", "limit", "verdict",
		"integration", "$0.1000", "$0.1250", "+25.0%", "+10.0%", "BREACH",
		"breach: integration:",
		"check: FAIL",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCheckTextPassLine(t *testing.T) {
	run, _ := fixture()
	lf := lockfile.New()
	lf.Policy.OnNewSuite = lockfile.ActionIgnore
	res := gate.Check(run, lf)
	var buf bytes.Buffer
	CheckText(&buf, res, "costlock.json", run)
	if !strings.Contains(buf.String(), "check: PASS") {
		t.Fatalf("missing PASS in:\n%s", buf.String())
	}
}

func TestCheckTextShowsNewAndMissingStatuses(t *testing.T) {
	run, _ := fixture()
	lf := lockfile.New()
	lf.Budgets["ghost"] = lockfile.Budget{Baseline: lockfile.Baseline{CostUSD: 0.01}}
	res := gate.Check(run, lf)
	var buf bytes.Buffer
	CheckText(&buf, res, "costlock.json", run)
	out := buf.String()
	if !strings.Contains(out, "NEW") {
		t.Fatalf("missing NEW marker:\n%s", out)
	}
	if !strings.Contains(out, "missing") {
		t.Fatalf("missing 'missing' marker:\n%s", out)
	}
	// The missing suite has no current spend to show.
	if !strings.Contains(out, "—") {
		t.Fatalf("missing em-dash placeholder:\n%s", out)
	}
}

func TestCheckJSONRoundTrips(t *testing.T) {
	run, res := fixture()
	var buf bytes.Buffer
	if err := CheckJSON(&buf, res, run); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tool          string `json:"tool"`
		Version       string `json:"version"`
		SchemaVersion int    `json:"schema_version"`
		Verdict       string `json:"verdict"`
		Suites        []struct {
			Suite       string   `json:"suite"`
			Verdict     string   `json:"verdict"`
			BaselineUSD *float64 `json:"baseline_usd"`
			CurrentUSD  *float64 `json:"current_usd"`
			DeltaPct    *float64 `json:"delta_pct"`
			LimitPct    *float64 `json:"limit_pct"`
		} `json:"suites"`
		Records int `json:"records"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if doc.Tool != "costlock" || doc.SchemaVersion != 1 || doc.Verdict != "breach" {
		t.Fatalf("envelope = %+v", doc)
	}
	if len(doc.Suites) != 2 || doc.Suites[0].Suite != "integration" {
		t.Fatalf("suites = %+v", doc.Suites)
	}
	integ := doc.Suites[0]
	if integ.Verdict != "breach" || integ.DeltaPct == nil || *integ.DeltaPct < 24.9 || *integ.DeltaPct > 25.1 {
		t.Fatalf("integration row = %+v", integ)
	}
	if doc.Records != 2 {
		t.Fatalf("records = %d", doc.Records)
	}
}

func TestCheckJSONOmitsBaselineForNewSuites(t *testing.T) {
	run, _ := fixture()
	res := gate.Check(run, lockfile.New())
	var buf bytes.Buffer
	if err := CheckJSON(&buf, res, run); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Suites []struct {
			Status      string   `json:"status"`
			BaselineUSD *float64 `json:"baseline_usd"`
		} `json:"suites"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, s := range doc.Suites {
		if s.Status == "new" && s.BaselineUSD != nil {
			t.Fatalf("new suite must have null baseline: %+v", s)
		}
	}
}

func TestCheckMarkdownTable(t *testing.T) {
	run, res := fixture()
	var buf bytes.Buffer
	CheckMarkdown(&buf, res, run)
	out := buf.String()
	for _, want := range []string{
		"### costlock check — FAIL ❌",
		"| Suite | Baseline | Current | Δ | Limit | Verdict |",
		"| integration | $0.1000 | $0.1250 | +25.0% | +10.0% | ❌ breach |",
		"| unit |",
		"- ❌ integration:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestReportTextTotalsAndBreakdowns(t *testing.T) {
	run, _ := fixture()
	var buf bytes.Buffer
	ReportText(&buf, run)
	out := buf.String()
	for _, want := range []string{
		"costlock report — 1 source(s), 2 record(s)",
		"total cost   $0.1500",
		"calls        2",
		"24,000 in / 3,000 out",
		"by suite",
		"integration",
		"by model",
		"model-a",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// A run with unpriced records grows an explicit unpriced line.
	recs := []usage.Record{{Suite: "unit", Model: "mystery", InputTokens: 10}}
	unpriced := aggregate.Build(recs, pricing.Table{}, true, 1)
	buf.Reset()
	ReportText(&buf, unpriced)
	if !strings.Contains(buf.String(), "unpriced     1 call(s) — models: mystery") {
		t.Fatalf("missing unpriced line:\n%s", buf.String())
	}
}

func TestReportJSONStructure(t *testing.T) {
	run, _ := fixture()
	var buf bytes.Buffer
	if err := ReportJSON(&buf, run); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tool  string `json:"tool"`
		Total struct {
			CostUSD float64 `json:"cost_usd"`
			Calls   int64   `json:"calls"`
		} `json:"total"`
		Suites map[string]struct {
			CostUSD float64 `json:"cost_usd"`
			ByModel map[string]struct {
				Calls int64 `json:"calls"`
			} `json:"by_model"`
		} `json:"suites"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc.Tool != "costlock" || doc.Total.Calls != 2 || doc.Total.CostUSD != 0.15 {
		t.Fatalf("doc = %+v", doc)
	}
	if doc.Suites["integration"].ByModel["model-a"].Calls != 1 {
		t.Fatalf("by_model = %+v", doc.Suites)
	}
}

func TestReportMarkdownTable(t *testing.T) {
	run, _ := fixture()
	var buf bytes.Buffer
	ReportMarkdown(&buf, run)
	out := buf.String()
	if !strings.Contains(out, "| Suite | Calls | Input tokens | Output tokens | Cost |") {
		t.Fatalf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "total **$0.1500**") {
		t.Fatalf("missing bold total:\n%s", out)
	}
}

func TestGroupInt(t *testing.T) {
	cases := map[int64]string{
		0: "0", 7: "7", 999: "999", 1000: "1,000",
		1234567: "1,234,567", -9876543: "-9,876,543",
	}
	for n, want := range cases {
		if got := groupInt(n); got != want {
			t.Fatalf("groupInt(%d) = %q, want %q", n, got, want)
		}
	}
}
