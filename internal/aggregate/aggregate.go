// Package aggregate folds normalized usage records into per-suite and
// per-model totals, pricing each call along the way. It is pure: same
// records + same price table = byte-identical downstream reports.
package aggregate

import (
	"sort"

	"github.com/JaydenCJ/costlock/internal/pricing"
	"github.com/JaydenCJ/costlock/internal/usage"
)

// Usage is a rollup of calls, tokens, and priced cost.
type Usage struct {
	Calls            int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
	// UnpricedCalls counts records that contributed $0 because they
	// had neither a recorded cost nor a matching price-table entry.
	UnpricedCalls int64
}

// Suite is the rollup for one budget group, with a per-model breakdown.
type Suite struct {
	Usage
	ByModel map[string]*Usage
}

// Run is a whole test run, aggregated.
type Run struct {
	Suites map[string]*Suite
	Total  Usage
	// UnpricedModels lists (sorted, unique) the model names that had
	// unpriced records — the actionable detail behind UnpricedCalls.
	UnpricedModels []string
	// Sources is the number of log files/streams the records came from.
	Sources int
	Records int
}

// Build aggregates records, pricing each one. When preferRecorded is
// true a cost recorded in the log wins over the price table; either
// way, a record with only one of the two still gets priced by it.
func Build(records []usage.Record, prices pricing.Table, preferRecorded bool, sources int) Run {
	run := Run{
		Suites:  map[string]*Suite{},
		Sources: sources,
		Records: len(records),
	}
	unpriced := map[string]bool{}

	for _, rec := range records {
		cost, priced := price(rec, prices, preferRecorded)

		s := run.Suites[rec.Suite]
		if s == nil {
			s = &Suite{ByModel: map[string]*Usage{}}
			run.Suites[rec.Suite] = s
		}
		m := s.ByModel[rec.Model]
		if m == nil {
			m = &Usage{}
			s.ByModel[rec.Model] = m
		}

		for _, u := range []*Usage{&run.Total, &s.Usage, m} {
			u.Calls++
			u.InputTokens += rec.InputTokens
			u.OutputTokens += rec.OutputTokens
			u.CacheReadTokens += rec.CacheReadTokens
			u.CacheWriteTokens += rec.CacheWriteTokens
			u.CostUSD += cost
			if !priced {
				u.UnpricedCalls++
			}
		}
		if !priced {
			unpriced[rec.Model] = true
		}
	}

	for m := range unpriced {
		run.UnpricedModels = append(run.UnpricedModels, m)
	}
	sort.Strings(run.UnpricedModels)
	return run
}

// price resolves one record's cost in USD. The second return value is
// false when the record could not be priced at all.
func price(rec usage.Record, prices pricing.Table, preferRecorded bool) (float64, bool) {
	rate, _, matched := prices.Match(rec.Model)
	switch {
	case rec.HasRecordedCost && (preferRecorded || !matched):
		return rec.RecordedCostUSD, true
	case matched:
		return rate.Cost(rec.InputTokens, rec.OutputTokens, rec.CacheReadTokens, rec.CacheWriteTokens), true
	default:
		return 0, false
	}
}

// SuiteNames returns the run's suite names, sorted.
func (r Run) SuiteNames() []string {
	names := make([]string, 0, len(r.Suites))
	for n := range r.Suites {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
