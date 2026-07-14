// Package usage parses JSONL usage logs emitted by test runs into
// normalized records: model, suite, token counts, and (when present) a
// recorded cost. It understands the common provider shapes — nested
// `usage` objects with `input_tokens`/`output_tokens` or
// `prompt_tokens`/`completion_tokens` aliases, cache-token fields, and
// flat records — without any configuration.
package usage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Record is one normalized usage event (typically one API call made
// during a test run).
type Record struct {
	// Model is the model identifier from the log line, or "unknown"
	// when the line carries no model field.
	Model string
	// Suite is the budget group this record belongs to (see suitePaths
	// and Options.SuiteKey). Records with no suite land in "default".
	Suite string

	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64

	// RecordedCostUSD is a cost the log itself reported; valid only
	// when HasRecordedCost is true.
	RecordedCostUSD float64
	HasRecordedCost bool

	// File and Line locate the source log line for error messages.
	File string
	Line int
}

// Options tunes parsing.
type Options struct {
	// SuiteKey, when non-empty, is the only dotted path consulted for
	// the record's suite (e.g. "tags.suite"). When empty, the default
	// candidates in suitePaths are tried in order.
	SuiteKey string
}

// DefaultSuite is the suite assigned to records that carry no suite field.
const DefaultSuite = "default"

// UnknownModel is the model assigned to records that carry no model field.
const UnknownModel = "unknown"

// Dotted paths tried in order for each normalized field. Nested paths
// are checked before flat aliases so a well-formed provider payload
// always wins over a coincidental top-level key.
var (
	modelPaths = []string{"model", "model_name", "response.model"}
	suitePaths = []string{"suite", "test", "group", "tags.suite"}
	inputPaths = []string{
		"usage.input_tokens", "usage.prompt_tokens",
		"input_tokens", "prompt_tokens",
	}
	outputPaths = []string{
		"usage.output_tokens", "usage.completion_tokens",
		"output_tokens", "completion_tokens",
	}
	cacheReadPaths = []string{
		"usage.cache_read_input_tokens",
		"usage.prompt_tokens_details.cached_tokens",
		"cache_read_input_tokens",
	}
	cacheWritePaths = []string{
		"usage.cache_creation_input_tokens",
		"cache_creation_input_tokens",
	}
	costPaths = []string{"cost_usd", "cost", "usage.cost_usd", "usage.cost"}
)

// cachedSubsetPath marks the one cache-read source whose value is a
// subset of the input-token count (OpenAI-style
// `prompt_tokens_details.cached_tokens` counts tokens already included
// in `prompt_tokens`). When cache reads come from this path they are
// subtracted from InputTokens so pricing never double-charges them.
const cachedSubsetPath = "usage.prompt_tokens_details.cached_tokens"

// ParseLine normalizes one JSONL line. name and lineNo are used only
// for error messages and the resulting Record's provenance.
func ParseLine(line []byte, name string, lineNo int, opts Options) (Record, error) {
	rec := Record{File: name, Line: lineNo}

	var raw any
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return rec, fmt.Errorf("%s:%d: invalid JSON: %v", name, lineNo, err)
	}
	// Two objects on one line would mean the second silently drops out
	// of the cost math — for a budget gate that is a hole, not a nicety.
	if dec.More() {
		return rec, fmt.Errorf("%s:%d: trailing data after JSON object (one record per line)", name, lineNo)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return rec, fmt.Errorf("%s:%d: line is not a JSON object", name, lineNo)
	}

	rec.Model = firstString(obj, modelPaths)
	if rec.Model == "" {
		rec.Model = UnknownModel
	}

	if opts.SuiteKey != "" {
		rec.Suite = stringAt(obj, opts.SuiteKey)
	} else {
		rec.Suite = firstString(obj, suitePaths)
	}
	if rec.Suite == "" {
		rec.Suite = DefaultSuite
	}

	var err error
	if rec.InputTokens, _, err = firstCount(obj, inputPaths, name, lineNo); err != nil {
		return rec, err
	}
	if rec.OutputTokens, _, err = firstCount(obj, outputPaths, name, lineNo); err != nil {
		return rec, err
	}
	var cacheReadSrc string
	if rec.CacheReadTokens, cacheReadSrc, err = firstCount(obj, cacheReadPaths, name, lineNo); err != nil {
		return rec, err
	}
	if rec.CacheWriteTokens, _, err = firstCount(obj, cacheWritePaths, name, lineNo); err != nil {
		return rec, err
	}

	// OpenAI-style cached tokens are a subset of the prompt tokens;
	// carve them out so the uncached rate applies only to the remainder.
	if cacheReadSrc == cachedSubsetPath {
		if rec.CacheReadTokens > rec.InputTokens {
			return rec, fmt.Errorf("%s:%d: cached_tokens (%d) exceeds prompt_tokens (%d)",
				name, lineNo, rec.CacheReadTokens, rec.InputTokens)
		}
		rec.InputTokens -= rec.CacheReadTokens
	}

	if v, _, ok := firstNumber(obj, costPaths); ok {
		if v < 0 {
			return rec, fmt.Errorf("%s:%d: negative cost %v", name, lineNo, v)
		}
		rec.RecordedCostUSD = v
		rec.HasRecordedCost = true
	}

	return rec, nil
}

// lookup walks a dotted path through nested JSON objects.
func lookup(obj map[string]any, dotted string) (any, bool) {
	parts := strings.Split(dotted, ".")
	cur := any(obj)
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func stringAt(obj map[string]any, path string) string {
	v, ok := lookup(obj, path)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func firstString(obj map[string]any, paths []string) string {
	for _, p := range paths {
		if s := stringAt(obj, p); s != "" {
			return s
		}
	}
	return ""
}

// firstNumber returns the first numeric value found along paths,
// together with the path that matched.
func firstNumber(obj map[string]any, paths []string) (float64, string, bool) {
	for _, p := range paths {
		v, ok := lookup(obj, p)
		if !ok {
			continue
		}
		n, ok := v.(json.Number)
		if !ok {
			continue
		}
		f, err := n.Float64()
		if err != nil {
			continue
		}
		return f, p, true
	}
	return 0, "", false
}

// firstCount resolves a token count: the first numeric match along
// paths, validated to be a non-negative integer. Integers are decoded
// via json.Number directly so counts beyond 2^53 keep full precision.
func firstCount(obj map[string]any, paths []string, name string, lineNo int) (int64, string, error) {
	for _, p := range paths {
		v, ok := lookup(obj, p)
		if !ok {
			continue
		}
		n, ok := v.(json.Number)
		if !ok {
			continue
		}
		if i, err := n.Int64(); err == nil {
			if i < 0 {
				return 0, "", fmt.Errorf("%s:%d: negative token count %v at %q", name, lineNo, n, p)
			}
			return i, p, nil
		}
		f, err := n.Float64()
		if err != nil {
			continue
		}
		if f < 0 {
			return 0, "", fmt.Errorf("%s:%d: negative token count %v at %q", name, lineNo, n, p)
		}
		if f != float64(int64(f)) {
			return 0, "", fmt.Errorf("%s:%d: token count %v at %q is not an integer", name, lineNo, n, p)
		}
		return int64(f), p, nil
	}
	return 0, "", nil
}
