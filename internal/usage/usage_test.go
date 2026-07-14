// Tests for JSONL record normalization: provider field aliases, cache
// token semantics, suite resolution, and malformed-input errors with
// precise file:line locations.
package usage

import (
	"strings"
	"testing"
)

func parse(t *testing.T, line string, opts Options) Record {
	t.Helper()
	rec, err := ParseLine([]byte(line), "log.jsonl", 1, opts)
	if err != nil {
		t.Fatalf("ParseLine(%s): %v", line, err)
	}
	return rec
}

func TestTokenFieldAliases(t *testing.T) {
	// Nested provider payloads and flat records must normalize to the
	// same two fields, whichever naming convention the SDK used.
	cases := []struct {
		line    string
		in, out int64
	}{
		{`{"model":"m","usage":{"input_tokens":120,"output_tokens":30}}`, 120, 30},
		{`{"model":"m","usage":{"prompt_tokens":50,"completion_tokens":9}}`, 50, 9},
		{`{"model":"m","input_tokens":7,"output_tokens":3}`, 7, 3},
		{`{"model":"m","prompt_tokens":11,"completion_tokens":2}`, 11, 2},
	}
	for _, c := range cases {
		rec := parse(t, c.line, Options{})
		if rec.InputTokens != c.in || rec.OutputTokens != c.out {
			t.Fatalf("%s: tokens = %d/%d, want %d/%d", c.line, rec.InputTokens, rec.OutputTokens, c.in, c.out)
		}
	}
}

func TestNestedUsageWinsOverFlat(t *testing.T) {
	// A well-formed provider payload beats a coincidental flat key.
	rec := parse(t, `{"model":"m1","input_tokens":999,"usage":{"input_tokens":10,"output_tokens":1}}`, Options{})
	if rec.InputTokens != 10 {
		t.Fatalf("InputTokens = %d, want nested 10", rec.InputTokens)
	}
}

func TestSeparateCacheReadAndWriteTokens(t *testing.T) {
	// Provider shapes where cache reads are counted separately from
	// input tokens: nothing is subtracted.
	rec := parse(t, `{"model":"m1","usage":{"input_tokens":100,"output_tokens":5,"cache_read_input_tokens":400,"cache_creation_input_tokens":50}}`, Options{})
	if rec.InputTokens != 100 {
		t.Fatalf("InputTokens = %d, want 100 (no subtraction)", rec.InputTokens)
	}
	if rec.CacheReadTokens != 400 || rec.CacheWriteTokens != 50 {
		t.Fatalf("cache = %d/%d, want 400/50", rec.CacheReadTokens, rec.CacheWriteTokens)
	}
}

func TestCachedSubsetTokensAreCarvedOutOfInput(t *testing.T) {
	// prompt_tokens_details.cached_tokens counts tokens already inside
	// prompt_tokens; costlock must not price them twice.
	rec := parse(t, `{"model":"m1","usage":{"prompt_tokens":1000,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":600}}}`, Options{})
	if rec.InputTokens != 400 {
		t.Fatalf("InputTokens = %d, want 400 (1000 - 600 cached)", rec.InputTokens)
	}
	if rec.CacheReadTokens != 600 {
		t.Fatalf("CacheReadTokens = %d, want 600", rec.CacheReadTokens)
	}
}

func TestRecordedCostAliases(t *testing.T) {
	for _, line := range []string{
		`{"model":"m1","cost_usd":0.25}`,
		`{"model":"m1","cost":0.25}`,
		`{"model":"m1","usage":{"cost_usd":0.25}}`,
	} {
		rec := parse(t, line, Options{})
		if !rec.HasRecordedCost || rec.RecordedCostUSD != 0.25 {
			t.Fatalf("line %s: cost = %v (has=%v), want 0.25", line, rec.RecordedCostUSD, rec.HasRecordedCost)
		}
	}
}

func TestNoCostFieldMeansNoRecordedCost(t *testing.T) {
	rec := parse(t, `{"model":"m1","usage":{"input_tokens":1,"output_tokens":1}}`, Options{})
	if rec.HasRecordedCost {
		t.Fatal("HasRecordedCost should be false")
	}
}

func TestModelAliasesAndUnknownFallback(t *testing.T) {
	if got := parse(t, `{"model_name":"alt"}`, Options{}).Model; got != "alt" {
		t.Fatalf("model_name alias: %q", got)
	}
	if got := parse(t, `{"response":{"model":"deep"}}`, Options{}).Model; got != "deep" {
		t.Fatalf("response.model alias: %q", got)
	}
	if got := parse(t, `{"usage":{"input_tokens":1}}`, Options{}).Model; got != UnknownModel {
		t.Fatalf("missing model should be %q, got %q", UnknownModel, got)
	}
}

func TestSuiteResolutionOrderAndDefault(t *testing.T) {
	if got := parse(t, `{"suite":"integration"}`, Options{}).Suite; got != "integration" {
		t.Fatalf("suite = %q", got)
	}
	if got := parse(t, `{"test":"TestLogin"}`, Options{}).Suite; got != "TestLogin" {
		t.Fatalf("test fallback = %q", got)
	}
	if got := parse(t, `{"tags":{"suite":"e2e"}}`, Options{}).Suite; got != "e2e" {
		t.Fatalf("tags.suite fallback = %q", got)
	}
	if got := parse(t, `{"model":"m1"}`, Options{}).Suite; got != DefaultSuite {
		t.Fatalf("default suite = %q", got)
	}
}

func TestSuiteKeyOverrideIsExclusive(t *testing.T) {
	// With --suite-key, the default candidates must NOT be consulted:
	// a record that lacks the key lands in "default" even though it
	// has a "suite" field.
	rec := parse(t, `{"suite":"wrong","meta":{"bucket":"right"}}`, Options{SuiteKey: "meta.bucket"})
	if rec.Suite != "right" {
		t.Fatalf("suite = %q, want right", rec.Suite)
	}
	rec = parse(t, `{"suite":"wrong"}`, Options{SuiteKey: "meta.bucket"})
	if rec.Suite != DefaultSuite {
		t.Fatalf("suite = %q, want %q", rec.Suite, DefaultSuite)
	}
}

func TestMalformedLinesRejectedWithLocation(t *testing.T) {
	// Every rejection must carry file:line plus a specific cause, so a
	// CI failure points at the exact offending log line.
	cases := []struct {
		name, line, wantErr string
	}{
		{"truncated JSON", `{"model":`, "log.jsonl:7"},
		{"non-object line", `[1,2,3]`, "not a JSON object"},
		// Two objects on one line: silently keeping only the first would
		// undercount spend, which is the one failure a budget gate can't afford.
		{"trailing second object", `{"model":"m1"} {"model":"m2"}`, "trailing data"},
		{"negative tokens", `{"usage":{"input_tokens":-5}}`, "negative token count"},
		{"fractional tokens", `{"usage":{"input_tokens":1.5}}`, "not an integer"},
		{"negative cost", `{"model":"m1","cost_usd":-0.1}`, "negative cost"},
		{"cached exceeds prompt", `{"usage":{"prompt_tokens":10,"prompt_tokens_details":{"cached_tokens":11}}}`, "cached_tokens"},
	}
	for _, c := range cases {
		_, err := ParseLine([]byte(c.line), "log.jsonl", 7, Options{})
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Fatalf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}
}

func TestLargeTokenCountsSurviveExactly(t *testing.T) {
	// json.Number keeps precision a float64 decode would lose.
	rec := parse(t, `{"usage":{"input_tokens":9007199254740993,"output_tokens":1}}`, Options{})
	if rec.InputTokens != 9007199254740993 {
		t.Fatalf("InputTokens = %d, want 9007199254740993", rec.InputTokens)
	}
}
