// Package pricing turns token counts into USD using the price table
// committed in the lockfile. costlock deliberately ships no built-in
// vendor prices: rates live next to the budgets they justify, so cost
// math is reproducible from the repository alone and every price change
// shows up in review.
package pricing

import (
	"fmt"
	"sort"
	"strings"
)

// Rate is the per-million-token price sheet for one model (or model
// pattern). Cache rates default to zero when omitted.
type Rate struct {
	InputPerMTok      float64 `json:"input_per_mtok"`
	OutputPerMTok     float64 `json:"output_per_mtok"`
	CacheReadPerMTok  float64 `json:"cache_read_per_mtok,omitempty"`
	CacheWritePerMTok float64 `json:"cache_write_per_mtok,omitempty"`
}

// Table maps a model name — or a prefix pattern ending in "*" — to its
// Rate. Longest match wins; an exact name always beats a pattern.
type Table map[string]Rate

// Cost prices a call's token counts in USD.
func (r Rate) Cost(input, output, cacheRead, cacheWrite int64) float64 {
	const mtok = 1e6
	return float64(input)/mtok*r.InputPerMTok +
		float64(output)/mtok*r.OutputPerMTok +
		float64(cacheRead)/mtok*r.CacheReadPerMTok +
		float64(cacheWrite)/mtok*r.CacheWritePerMTok
}

// Match resolves the rate for a model. It returns the matched table key
// so callers can report which entry priced the call. Matching is
// case-sensitive: exact key first, then the longest "prefix*" pattern.
func (t Table) Match(model string) (Rate, string, bool) {
	if r, ok := t[model]; ok {
		return r, model, true
	}
	bestKey := ""
	var bestRate Rate
	for key, rate := range t {
		if !strings.HasSuffix(key, "*") {
			continue
		}
		prefix := strings.TrimSuffix(key, "*")
		if !strings.HasPrefix(model, prefix) {
			continue
		}
		if bestKey == "" || len(key) > len(bestKey) {
			bestKey, bestRate = key, rate
		}
	}
	if bestKey == "" {
		return Rate{}, "", false
	}
	return bestRate, bestKey, true
}

// Validate rejects malformed tables: negative rates, empty keys, and
// "*" anywhere but the final position.
func (t Table) Validate() error {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == "" {
			return fmt.Errorf("prices: empty model key")
		}
		if i := strings.Index(key, "*"); i >= 0 && i != len(key)-1 {
			return fmt.Errorf("prices[%q]: %q may only end with a trailing *", key, key)
		}
		r := t[key]
		for name, v := range map[string]float64{
			"input_per_mtok":       r.InputPerMTok,
			"output_per_mtok":      r.OutputPerMTok,
			"cache_read_per_mtok":  r.CacheReadPerMTok,
			"cache_write_per_mtok": r.CacheWritePerMTok,
		} {
			if v < 0 {
				return fmt.Errorf("prices[%q]: negative %s", key, name)
			}
		}
	}
	return nil
}
