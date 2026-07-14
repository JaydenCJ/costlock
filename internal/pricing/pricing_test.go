// Tests for the price table: pattern matching precedence, per-token
// arithmetic, and validation of malformed tables.
package pricing

import (
	"math"
	"strings"
	"testing"
)

func TestMatchPrecedence(t *testing.T) {
	tab := Table{
		"model-exact":       {InputPerMTok: 1},
		"gpt-4o-*":          {InputPerMTok: 2},
		"gpt-4o-mini":       {InputPerMTok: 3},
		"claude-*":          {InputPerMTok: 9},
		"claude-3-5-haiku*": {InputPerMTok: 4},
	}
	cases := []struct {
		model   string
		wantKey string
		wantOK  bool
	}{
		{"model-exact", "model-exact", true},                     // exact hit
		{"model-other", "", false},                               // no match at all
		{"gpt-4o-2024-08-06", "gpt-4o-*", true},                  // wildcard prefix
		{"gpt-4o-mini", "gpt-4o-mini", true},                     // exact beats wildcard
		{"claude-3-5-haiku-20241022", "claude-3-5-haiku*", true}, // longest pattern wins
		{"MODEL-EXACT", "", false},                               // case-sensitive by design
	}
	for _, c := range cases {
		_, key, ok := tab.Match(c.model)
		if ok != c.wantOK || key != c.wantKey {
			t.Fatalf("Match(%q) = %q ok=%v, want %q ok=%v", c.model, key, ok, c.wantKey, c.wantOK)
		}
	}
}

func TestBareStarMatchesEverything(t *testing.T) {
	tab := Table{"*": {InputPerMTok: 5}}
	rate, key, ok := tab.Match("anything-at-all")
	if !ok || key != "*" || rate.InputPerMTok != 5 {
		t.Fatalf("bare * match = %q %v ok=%v", key, rate, ok)
	}
}

func TestCostArithmeticAllFourRates(t *testing.T) {
	r := Rate{InputPerMTok: 3, OutputPerMTok: 15, CacheReadPerMTok: 0.3, CacheWritePerMTok: 3.75}
	// 1M of each bucket costs exactly the per-mtok rate.
	got := r.Cost(1_000_000, 1_000_000, 1_000_000, 1_000_000)
	want := 3.0 + 15 + 0.3 + 3.75
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
}

func TestCostSmallCallIsProportionalAndZeroIsFree(t *testing.T) {
	r := Rate{InputPerMTok: 2.5, OutputPerMTok: 10}
	got := r.Cost(1200, 340, 0, 0)
	want := 1200.0/1e6*2.5 + 340.0/1e6*10
	if math.Abs(got-want) > 1e-15 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
	if got := r.Cost(0, 0, 0, 0); got != 0 {
		t.Fatalf("zero tokens cost = %v, want 0", got)
	}
}

func TestValidateAcceptsGoodTable(t *testing.T) {
	tab := Table{
		"model-a":  {InputPerMTok: 1, OutputPerMTok: 2},
		"model-b*": {InputPerMTok: 0, OutputPerMTok: 0},
		"*":        {InputPerMTok: 3},
	}
	if err := tab.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsMalformedTables(t *testing.T) {
	cases := []struct {
		name    string
		tab     Table
		wantErr string
	}{
		{"negative rate", Table{"m": {InputPerMTok: -1}}, "negative"},
		{"interior star", Table{"gpt-*-mini": {InputPerMTok: 1}}, "trailing *"},
		{"empty key", Table{"": {InputPerMTok: 1}}, "empty model key"},
	}
	for _, c := range cases {
		err := c.tab.Validate()
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Fatalf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}
}
