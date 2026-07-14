// JSON renderers: stable machine-readable envelopes (schema_version 1)
// for check results and run reports.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"math"

	"github.com/JaydenCJ/costlock/internal/aggregate"
	"github.com/JaydenCJ/costlock/internal/gate"
	"github.com/JaydenCJ/costlock/internal/version"
)

type jsonEnvelope struct {
	Tool          string `json:"tool"`
	Version       string `json:"version"`
	SchemaVersion int    `json:"schema_version"`
}

type jsonRow struct {
	Suite       string   `json:"suite"`
	Status      string   `json:"status"`
	Verdict     string   `json:"verdict"`
	BaselineUSD *float64 `json:"baseline_usd"`
	CurrentUSD  *float64 `json:"current_usd"`
	DeltaPct    *float64 `json:"delta_pct"`
	DeltaInf    bool     `json:"delta_inf,omitempty"`
	LimitPct    *float64 `json:"limit_pct"`
	Reasons     []string `json:"reasons,omitempty"`
}

type jsonCheck struct {
	jsonEnvelope
	Verdict  string    `json:"verdict"`
	Suites   []jsonRow `json:"suites"`
	Total    jsonRow   `json:"total"`
	Notes    []string  `json:"notes,omitempty"`
	Problems []string  `json:"problems,omitempty"`
	Sources  int       `json:"sources"`
	Records  int       `json:"records"`
}

type jsonUsage struct {
	Calls            int64   `json:"calls"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	UnpricedCalls    int64   `json:"unpriced_calls,omitempty"`
}

type jsonSuite struct {
	jsonUsage
	ByModel map[string]jsonUsage `json:"by_model"`
}

type jsonReport struct {
	jsonEnvelope
	Total          jsonUsage            `json:"total"`
	Suites         map[string]jsonSuite `json:"suites"`
	UnpricedModels []string             `json:"unpriced_models,omitempty"`
	Sources        int                  `json:"sources"`
	Records        int                  `json:"records"`
}

func envelope() jsonEnvelope {
	return jsonEnvelope{Tool: "costlock", Version: version.Version, SchemaVersion: 1}
}

// CheckJSON writes the check result as indented JSON.
func CheckJSON(w io.Writer, res gate.Result, run aggregate.Run) error {
	doc := jsonCheck{
		jsonEnvelope: envelope(),
		Verdict:      verdictWord(res.Verdict),
		Total:        toJSONRow(res.Total),
		Notes:        res.Notes,
		Problems:     res.Problems,
		Sources:      run.Sources,
		Records:      run.Records,
	}
	doc.Suites = make([]jsonRow, 0, len(res.Rows))
	for _, r := range res.Rows {
		doc.Suites = append(doc.Suites, toJSONRow(r))
	}
	return writeJSON(w, doc)
}

// ReportJSON writes the ungated run summary as indented JSON.
func ReportJSON(w io.Writer, run aggregate.Run) error {
	doc := jsonReport{
		jsonEnvelope:   envelope(),
		Total:          toJSONUsage(run.Total),
		Suites:         map[string]jsonSuite{},
		UnpricedModels: run.UnpricedModels,
		Sources:        run.Sources,
		Records:        run.Records,
	}
	for name, s := range run.Suites {
		js := jsonSuite{jsonUsage: toJSONUsage(s.Usage), ByModel: map[string]jsonUsage{}}
		for m, u := range s.ByModel {
			js.ByModel[m] = toJSONUsage(*u)
		}
		doc.Suites[name] = js
	}
	return writeJSON(w, doc)
}

func toJSONRow(r gate.Row) jsonRow {
	row := jsonRow{
		Suite:    r.Suite,
		Status:   r.Status,
		Verdict:  verdictWord(r.Verdict),
		DeltaInf: r.DeltaInf,
		Reasons:  r.Reasons,
	}
	if r.HasBaseline {
		row.BaselineUSD = ptr(round6(r.BaselineUSD))
	}
	if r.Status != gate.StatusMissing {
		row.CurrentUSD = ptr(round6(r.CurrentUSD))
	}
	if r.HasBaseline && r.Status != gate.StatusMissing && !r.DeltaInf {
		row.DeltaPct = ptr(round6(r.DeltaPct))
	}
	if r.Gated {
		row.LimitPct = ptr(r.LimitPct)
	}
	return row
}

func toJSONUsage(u aggregate.Usage) jsonUsage {
	return jsonUsage{
		Calls:            u.Calls,
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
		CostUSD:          round6(u.CostUSD),
		UnpricedCalls:    u.UnpricedCalls,
	}
}

// verdictWord is the lowercase JSON spelling ("ok", "warn", "breach").
func verdictWord(v gate.Verdict) string {
	switch v {
	case gate.Warn:
		return "warn"
	case gate.Breach:
		return "breach"
	default:
		return "ok"
	}
}

func writeJSON(w io.Writer, doc any) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

func ptr[T any](v T) *T { return &v }

// round6 applies the same six-decimal money precision the lockfile
// stores, so JSON output never leaks float noise.
func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}
