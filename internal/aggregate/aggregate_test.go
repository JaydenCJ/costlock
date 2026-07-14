// Tests for aggregation: suite/model rollups, pricing precedence
// (recorded cost vs price table), and unpriced accounting.
package aggregate

import (
	"math"
	"testing"

	"github.com/JaydenCJ/costlock/internal/pricing"
	"github.com/JaydenCJ/costlock/internal/usage"
)

var prices = pricing.Table{
	"model-a": {InputPerMTok: 2, OutputPerMTok: 10},
	"model-b": {InputPerMTok: 1, OutputPerMTok: 1, CacheReadPerMTok: 0.1},
}

func rec(suite, model string, in, out int64) usage.Record {
	return usage.Record{Suite: suite, Model: model, InputTokens: in, OutputTokens: out}
}

func approx(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func TestBuildRollsUpSuitesModelsAndTotal(t *testing.T) {
	run := Build([]usage.Record{
		rec("unit", "model-a", 1000, 100),
		rec("unit", "model-b", 2000, 200),
		rec("integration", "model-a", 500, 50),
	}, prices, true, 2)

	if run.Records != 3 || run.Sources != 2 {
		t.Fatalf("records=%d sources=%d", run.Records, run.Sources)
	}
	if len(run.Suites) != 2 {
		t.Fatalf("suites = %v", run.SuiteNames())
	}
	unit := run.Suites["unit"]
	if unit.Calls != 2 || unit.InputTokens != 3000 || unit.OutputTokens != 300 {
		t.Fatalf("unit rollup = %+v", unit.Usage)
	}
	wantUnit := 1000.0/1e6*2 + 100.0/1e6*10 + 2000.0/1e6*1 + 200.0/1e6*1
	approx(t, unit.CostUSD, wantUnit, "unit cost")
	approx(t, run.Total.CostUSD, wantUnit+500.0/1e6*2+50.0/1e6*10, "total cost")
	if run.Total.Calls != 3 {
		t.Fatalf("total calls = %d", run.Total.Calls)
	}
}

func TestPerModelBreakdown(t *testing.T) {
	run := Build([]usage.Record{
		rec("unit", "model-a", 100, 10),
		rec("unit", "model-a", 100, 10),
		rec("unit", "model-b", 100, 10),
	}, prices, true, 1)
	unit := run.Suites["unit"]
	if unit.ByModel["model-a"].Calls != 2 || unit.ByModel["model-b"].Calls != 1 {
		t.Fatalf("by-model = %+v", unit.ByModel)
	}
}

func TestRecordedCostPreferredOverTable(t *testing.T) {
	r := rec("unit", "model-a", 1_000_000, 0) // table price would be $2
	r.HasRecordedCost = true
	r.RecordedCostUSD = 0.5
	run := Build([]usage.Record{r}, prices, true, 1)
	approx(t, run.Total.CostUSD, 0.5, "cost")
	if run.Total.UnpricedCalls != 0 {
		t.Fatalf("unpriced = %d", run.Total.UnpricedCalls)
	}
}

func TestTablePreferredWhenPreferRecordedIsFalse(t *testing.T) {
	r := rec("unit", "model-a", 1_000_000, 0)
	r.HasRecordedCost = true
	r.RecordedCostUSD = 0.5
	run := Build([]usage.Record{r}, prices, false, 1)
	approx(t, run.Total.CostUSD, 2.0, "cost")
}

func TestRecordedCostUsedWhenNoPriceEvenIfNotPreferred(t *testing.T) {
	// prefer_recorded_cost=false must not throw away the only price
	// information available.
	r := rec("unit", "mystery-model", 1000, 0)
	r.HasRecordedCost = true
	r.RecordedCostUSD = 0.07
	run := Build([]usage.Record{r}, prices, false, 1)
	approx(t, run.Total.CostUSD, 0.07, "cost")
	if run.Total.UnpricedCalls != 0 {
		t.Fatal("record with recorded cost is priced")
	}
}

func TestUnpricedRecordsAreCountedAndListed(t *testing.T) {
	run := Build([]usage.Record{
		rec("unit", "mystery-b", 10, 1),
		rec("unit", "mystery-a", 10, 1),
		rec("unit", "model-a", 10, 1),
	}, prices, true, 1)
	if run.Total.UnpricedCalls != 2 {
		t.Fatalf("unpriced = %d, want 2", run.Total.UnpricedCalls)
	}
	if len(run.UnpricedModels) != 2 || run.UnpricedModels[0] != "mystery-a" || run.UnpricedModels[1] != "mystery-b" {
		t.Fatalf("UnpricedModels = %v (must be sorted, unique)", run.UnpricedModels)
	}
	if run.Suites["unit"].UnpricedCalls != 2 {
		t.Fatalf("suite unpriced = %d", run.Suites["unit"].UnpricedCalls)
	}
}

func TestCacheTokensArePriced(t *testing.T) {
	r := rec("unit", "model-b", 0, 0)
	r.CacheReadTokens = 1_000_000
	run := Build([]usage.Record{r}, prices, true, 1)
	approx(t, run.Total.CostUSD, 0.1, "cache-read cost")
	if run.Total.CacheReadTokens != 1_000_000 {
		t.Fatalf("cache read tokens = %d", run.Total.CacheReadTokens)
	}
}

func TestSuiteNamesSortedAndEmptyRunWellFormed(t *testing.T) {
	run := Build([]usage.Record{
		rec("zz", "model-a", 1, 1),
		rec("aa", "model-a", 1, 1),
		rec("mm", "model-a", 1, 1),
	}, prices, true, 1)
	got := run.SuiteNames()
	if len(got) != 3 || got[0] != "aa" || got[1] != "mm" || got[2] != "zz" {
		t.Fatalf("SuiteNames = %v", got)
	}
	empty := Build(nil, prices, true, 0)
	if empty.Records != 0 || empty.Total.Calls != 0 || len(empty.Suites) != 0 {
		t.Fatalf("empty run = %+v", empty)
	}
}
