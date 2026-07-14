# costlock.json — lockfile format (schema_version 1)

`costlock.json` is the committed contract between your test suite and
your invoice. It is strict JSON: unknown fields are **rejected** at
load time, so a typo can never silently disable a gate. Serialization
is deterministic (sorted keys, two-space indent, costs rounded to six
decimals), so `costlock update` on an unchanged run does not churn a
single byte.

```json
{
  "schema_version": 1,
  "policy": { … },
  "prices": { … },
  "total": { … },
  "budgets": { "<suite>": { … } }
}
```

## policy

Run-wide gating rules. Fields missing from a hand-edited file get the
defaults below.

| Key | Default | Effect |
|---|---|---|
| `tolerance_pct` | `10` | allowed cost growth over baseline before a suite breaches |
| `warn_pct` | `5` | growth that prints a warning but exits 0 |
| `on_new_suite` | `"fail"` | suite in the run but not in the lockfile: `fail`, `warn`, or `ignore` |
| `on_missing_suite` | `"warn"` | budgeted suite absent from the run: `fail`, `warn`, or `ignore` |
| `on_unpriced` | `"fail"` | records with no recorded cost and no matching price: `fail`, `warn`, or `ignore` |
| `prefer_recorded_cost` | `true` | a `cost_usd` in the log wins over the price table |

Notes:

- Growth past `tolerance_pct` always breaches, even when `warn_pct` is
  misconfigured above it.
- A `$0` baseline with any current spend breaches (rendered `+inf`) —
  there is no meaningful percentage; re-baseline with `costlock update`.

## prices

USD per **million** tokens, keyed by model name or a prefix pattern
ending in `*`. An exact key beats any pattern; among patterns, the
longest wins. Matching is case-sensitive.

```json
"prices": {
  "gpt-4o-mini*":       { "input_per_mtok": 0.15, "output_per_mtok": 0.6 },
  "claude-sonnet-4-5*": { "input_per_mtok": 3.0,  "output_per_mtok": 15.0,
                          "cache_read_per_mtok": 0.3, "cache_write_per_mtok": 3.75 }
}
```

costlock deliberately ships **no built-in vendor prices**: rates live
next to the budgets they justify, so cost math is reproducible from
the repository alone and every price change is visible in review.

## budgets and total

One entry per suite; `total` (optional, same shape) gates the whole
run. `costlock init` writes baselines; the caps are yours to add.

| Key | Default | Effect |
|---|---|---|
| `baseline` | written by init/update | the accepted usage: `cost_usd`, `calls`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens` |
| `max_cost_usd` | unset | absolute ceiling, checked independently of the baseline |
| `max_calls` | unset | absolute cap on call count |
| `max_input_tokens` | unset | absolute cap on input-token volume |
| `max_output_tokens` | unset | absolute cap on output-token volume |
| `tolerance_pct` | unset | per-suite override of `policy.tolerance_pct` |

## How records are priced

For each log record, in order:

1. If `prefer_recorded_cost` and the record carries `cost_usd` (or
   `cost`, or `usage.cost_usd` / `usage.cost`) → use it.
2. Else, if a `prices` entry matches the model → compute
   `input/1e6·rate + output/1e6·rate + cache_read/1e6·rate + cache_write/1e6·rate`.
3. Else, if the record carries a recorded cost anyway → use it.
4. Else the record is **unpriced**: it contributes $0 and trips
   `on_unpriced`.

One subtlety: OpenAI-style `prompt_tokens_details.cached_tokens` counts
tokens already included in `prompt_tokens`, so costlock subtracts them
from the input bucket and prices them at the cache-read rate — cached
tokens are never charged twice. Provider shapes with separate
`cache_read_input_tokens` are taken as-is.
