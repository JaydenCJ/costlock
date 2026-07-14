// Package lockfile defines costlock.json: the committed budget file
// that a CI run is gated against. Parsing is strict (unknown fields are
// rejected) and serialization is deterministic (sorted keys, two-space
// indent, costs rounded to six decimals), so lockfile diffs stay
// review-friendly.
package lockfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"

	"github.com/JaydenCJ/costlock/internal/pricing"
)

// SchemaVersion is the only lockfile schema this build reads or writes.
const SchemaVersion = 1

// Policy actions for the on_* knobs.
const (
	ActionFail   = "fail"
	ActionWarn   = "warn"
	ActionIgnore = "ignore"
)

// Policy holds run-wide gating rules.
type Policy struct {
	// TolerancePct is the allowed cost growth over baseline, in
	// percent, before a suite breaches. Per-suite budgets may override.
	TolerancePct float64 `json:"tolerance_pct"`
	// WarnPct is the growth that triggers a warning (still exit 0).
	WarnPct float64 `json:"warn_pct"`
	// OnNewSuite decides what an unbudgeted suite in the run does:
	// fail (default), warn, or ignore.
	OnNewSuite string `json:"on_new_suite"`
	// OnMissingSuite decides what a budgeted suite absent from the run
	// does: fail, warn (default), or ignore.
	OnMissingSuite string `json:"on_missing_suite"`
	// OnUnpriced decides what records that cannot be priced (no
	// recorded cost, no matching price) do: fail (default), warn, or
	// ignore.
	OnUnpriced string `json:"on_unpriced"`
	// PreferRecordedCost uses a cost the log itself reports over the
	// price-table computation when both are available. Default true.
	PreferRecordedCost bool `json:"prefer_recorded_cost"`
}

// DefaultPolicy returns the policy written by `costlock init` and
// assumed for fields missing from a hand-edited lockfile.
func DefaultPolicy() Policy {
	return Policy{
		TolerancePct:       10,
		WarnPct:            5,
		OnNewSuite:         ActionFail,
		OnMissingSuite:     ActionWarn,
		OnUnpriced:         ActionFail,
		PreferRecordedCost: true,
	}
}

// Baseline is the recorded usage of one suite from the run the
// lockfile was generated (or last updated) from.
type Baseline struct {
	CostUSD          float64 `json:"cost_usd"`
	Calls            int64   `json:"calls"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64   `json:"cache_write_tokens,omitempty"`
}

// Budget is the committed contract for one suite: its baseline plus
// optional hard caps and a per-suite tolerance override.
type Budget struct {
	Baseline Baseline `json:"baseline"`
	// MaxCostUSD is an absolute ceiling, independent of the baseline.
	MaxCostUSD *float64 `json:"max_cost_usd,omitempty"`
	// MaxCalls / MaxInputTokens / MaxOutputTokens are absolute caps on
	// call count and token volume.
	MaxCalls        *int64 `json:"max_calls,omitempty"`
	MaxInputTokens  *int64 `json:"max_input_tokens,omitempty"`
	MaxOutputTokens *int64 `json:"max_output_tokens,omitempty"`
	// TolerancePct overrides Policy.TolerancePct for this suite.
	TolerancePct *float64 `json:"tolerance_pct,omitempty"`
}

// File is the full costlock.json document.
type File struct {
	SchemaVersion int               `json:"schema_version"`
	Policy        Policy            `json:"policy"`
	Prices        pricing.Table     `json:"prices"`
	Total         *Budget           `json:"total,omitempty"`
	Budgets       map[string]Budget `json:"budgets"`
}

// New returns an empty lockfile with defaults applied.
func New() File {
	return File{
		SchemaVersion: SchemaVersion,
		Policy:        DefaultPolicy(),
		Prices:        pricing.Table{},
		Budgets:       map[string]Budget{},
	}
}

// Load reads and validates a lockfile from disk.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	return Parse(data, path)
}

// Parse decodes a lockfile from bytes. Unknown fields are errors: a
// typo in a budget key must never silently disable a gate.
func Parse(data []byte, name string) (File, error) {
	f := New()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("%s: %v", name, err)
	}
	if err := f.Validate(); err != nil {
		return File{}, fmt.Errorf("%s: %v", name, err)
	}
	return f, nil
}

// Validate checks schema version, policy enums, prices, and budgets.
func (f File) Validate() error {
	if f.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d (this build reads %d)",
			f.SchemaVersion, SchemaVersion)
	}
	if f.Policy.TolerancePct < 0 {
		return fmt.Errorf("policy.tolerance_pct must be >= 0")
	}
	if f.Policy.WarnPct < 0 {
		return fmt.Errorf("policy.warn_pct must be >= 0")
	}
	for name, v := range map[string]string{
		"on_new_suite":     f.Policy.OnNewSuite,
		"on_missing_suite": f.Policy.OnMissingSuite,
		"on_unpriced":      f.Policy.OnUnpriced,
	} {
		switch v {
		case ActionFail, ActionWarn, ActionIgnore:
		default:
			return fmt.Errorf("policy.%s: %q is not one of fail, warn, ignore", name, v)
		}
	}
	if err := f.Prices.Validate(); err != nil {
		return err
	}
	if f.Total != nil {
		if err := validateBudget("total", *f.Total); err != nil {
			return err
		}
	}
	for suite, b := range f.Budgets {
		if suite == "" {
			return fmt.Errorf("budgets: empty suite name")
		}
		if err := validateBudget("budgets["+suite+"]", b); err != nil {
			return err
		}
	}
	return nil
}

func validateBudget(where string, b Budget) error {
	if b.Baseline.CostUSD < 0 {
		return fmt.Errorf("%s: negative baseline cost_usd", where)
	}
	for name, v := range map[string]int64{
		"calls":              b.Baseline.Calls,
		"input_tokens":       b.Baseline.InputTokens,
		"output_tokens":      b.Baseline.OutputTokens,
		"cache_read_tokens":  b.Baseline.CacheReadTokens,
		"cache_write_tokens": b.Baseline.CacheWriteTokens,
	} {
		if v < 0 {
			return fmt.Errorf("%s: negative baseline %s", where, name)
		}
	}
	if b.MaxCostUSD != nil && *b.MaxCostUSD < 0 {
		return fmt.Errorf("%s: negative max_cost_usd", where)
	}
	if b.MaxCalls != nil && *b.MaxCalls < 0 {
		return fmt.Errorf("%s: negative max_calls", where)
	}
	if b.MaxInputTokens != nil && *b.MaxInputTokens < 0 {
		return fmt.Errorf("%s: negative max_input_tokens", where)
	}
	if b.MaxOutputTokens != nil && *b.MaxOutputTokens < 0 {
		return fmt.Errorf("%s: negative max_output_tokens", where)
	}
	if b.TolerancePct != nil && *b.TolerancePct < 0 {
		return fmt.Errorf("%s: negative tolerance_pct", where)
	}
	return nil
}

// RoundCost normalizes a USD amount to six decimals — the precision
// stored in lockfiles, chosen so float noise never churns a diff.
func RoundCost(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// Marshal serializes the lockfile deterministically: sorted object
// keys (encoding/json sorts maps), two-space indent, trailing newline,
// baseline costs rounded via RoundCost.
func (f File) Marshal() ([]byte, error) {
	f.Total = roundBudget(f.Total)
	rounded := make(map[string]Budget, len(f.Budgets))
	for k, b := range f.Budgets {
		rounded[k] = *roundBudget(&b)
	}
	f.Budgets = rounded
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Save writes the lockfile atomically-enough for CI use: full rewrite
// with 0644 permissions.
func (f File) Save(path string) error {
	data, err := f.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func roundBudget(b *Budget) *Budget {
	if b == nil {
		return nil
	}
	c := *b
	c.Baseline.CostUSD = RoundCost(c.Baseline.CostUSD)
	return &c
}
