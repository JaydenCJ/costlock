// Tests for the lockfile: strict parsing, validation, defaults for
// hand-edited files, and byte-deterministic serialization.
package lockfile

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/costlock/internal/pricing"
)

func sample() File {
	f := New()
	f.Prices = pricing.Table{"model-a": {InputPerMTok: 1.5, OutputPerMTok: 6}}
	max := 0.75
	f.Budgets["integration"] = Budget{
		Baseline:   Baseline{CostUSD: 0.421337, Calls: 12, InputTokens: 52000, OutputTokens: 9100},
		MaxCostUSD: &max,
	}
	f.Total = &Budget{Baseline: Baseline{CostUSD: 0.5, Calls: 20}}
	return f
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "costlock.json")
	orig := sample()
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	b := got.Budgets["integration"]
	if b.Baseline.Calls != 12 || b.Baseline.InputTokens != 52000 {
		t.Fatalf("baseline = %+v", b.Baseline)
	}
	if b.MaxCostUSD == nil || *b.MaxCostUSD != 0.75 {
		t.Fatalf("max_cost_usd = %v", b.MaxCostUSD)
	}
	if got.Total == nil || got.Total.Baseline.Calls != 20 {
		t.Fatalf("total = %+v", got.Total)
	}
}

func TestMarshalIsByteDeterministic(t *testing.T) {
	a, err := sample().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	b, err := sample().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("two marshals of the same lockfile differ")
	}
	if !bytes.HasSuffix(a, []byte("}\n")) {
		t.Fatal("lockfile must end with a newline")
	}
}

func TestMarshalRoundsBaselineCosts(t *testing.T) {
	f := New()
	f.Budgets["s"] = Budget{Baseline: Baseline{CostUSD: 0.1234567891}}
	data, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "0.123457") {
		t.Fatalf("cost not rounded to 6dp:\n%s", data)
	}
	// RoundCost itself: down, up, and integer passthrough.
	if got := RoundCost(0.1234564); got != 0.123456 {
		t.Fatalf("RoundCost down = %v", got)
	}
	if got := RoundCost(0.1234567); got != 0.123457 {
		t.Fatalf("RoundCost up = %v", got)
	}
	if got := RoundCost(2); got != 2 {
		t.Fatalf("RoundCost integer = %v", got)
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	// A typo like "max_cost_us" must never silently disable a gate.
	doc := `{"schema_version":1,"policy":{"tolerance_pct":10,"warn_pct":5,"on_new_suite":"fail","on_missing_suite":"warn","on_unpriced":"fail","prefer_recorded_cost":true},"prices":{},"budgets":{"s":{"baseline":{"cost_usd":0,"calls":0,"input_tokens":0,"output_tokens":0},"max_cost_us":1}}}`
	_, err := Parse([]byte(doc), "costlock.json")
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("err = %v, want unknown-field rejection", err)
	}
}

func TestParseRejectsWrongSchemaVersion(t *testing.T) {
	_, err := Parse([]byte(`{"schema_version":2,"budgets":{}}`), "costlock.json")
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseAppliesPolicyDefaults(t *testing.T) {
	// A minimal hand-written lockfile gets the documented defaults.
	doc := `{"schema_version":1,"budgets":{}}`
	f, err := Parse([]byte(doc), "costlock.json")
	if err != nil {
		t.Fatal(err)
	}
	def := DefaultPolicy()
	if f.Policy != def {
		t.Fatalf("policy = %+v, want defaults %+v", f.Policy, def)
	}
}

func TestParsePartialPolicyKeepsOtherDefaults(t *testing.T) {
	doc := `{"schema_version":1,"policy":{"tolerance_pct":25},"budgets":{}}`
	f, err := Parse([]byte(doc), "costlock.json")
	if err != nil {
		t.Fatal(err)
	}
	if f.Policy.TolerancePct != 25 {
		t.Fatalf("tolerance = %v", f.Policy.TolerancePct)
	}
	if f.Policy.OnNewSuite != ActionFail || f.Policy.WarnPct != 5 {
		t.Fatalf("other defaults lost: %+v", f.Policy)
	}
}

func TestValidateRejectsMalformedFiles(t *testing.T) {
	badCalls := int64(-3)
	cases := []struct {
		name    string
		mutate  func(*File)
		wantErr string
	}{
		{"bad policy enum", func(f *File) { f.Policy.OnUnpriced = "explode" }, "on_unpriced"},
		{"negative tolerance", func(f *File) { f.Policy.TolerancePct = -1 }, "tolerance_pct"},
		{"negative budget cap", func(f *File) { f.Budgets["s"] = Budget{MaxCalls: &badCalls} }, "max_calls"},
		{"bad price table", func(f *File) { f.Prices = pricing.Table{"a*b": {InputPerMTok: 1}} }, "trailing *"},
		{"empty suite name", func(f *File) { f.Budgets[""] = Budget{} }, "empty suite name"},
		{"negative total baseline", func(f *File) { f.Total = &Budget{Baseline: Baseline{CostUSD: -1}} }, "total"},
	}
	for _, c := range cases {
		f := New()
		c.mutate(&f)
		err := f.Validate()
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Fatalf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}
}

func TestLoadMissingFileReturnsNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if !os.IsNotExist(err) {
		t.Fatalf("err = %v, want not-exist", err)
	}
}
