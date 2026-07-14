# costlock examples

Everything here is offline and self-contained: two captured usage logs,
a committed price table, and a gate script.

## Files

- `prices.json` — a price table (USD per million tokens) with wildcard
  model patterns and cache rates. Rates here are examples; commit the
  ones your provider actually charges you.
- `usage.baseline.jsonl` — a captured test run mixing provider shapes:
  `input_tokens`/`output_tokens`, `prompt_tokens`/`completion_tokens`,
  separate cache fields, and OpenAI-style `cached_tokens` subsets.
- `usage.regression.jsonl` — the same suites after a prompt-bloat
  regression: the `integration` suite's spend roughly doubles.
- `ci-gate.sh` — the whole CI story in one script.

## Walkthrough

```bash
go build -o costlock ./cmd/costlock

# 1. baseline a known-good run and commit the result
./costlock init --prices examples/prices.json examples/usage.baseline.jsonl
git add costlock.json

# 2. every CI run afterwards
./costlock check examples/usage.baseline.jsonl     # exit 0, "check: PASS"
./costlock check examples/usage.regression.jsonl   # exit 1, "check: FAIL"

# 3. a regression was intentional? accept it explicitly
./costlock update examples/usage.regression.jsonl
```

## ci-gate.sh

Runs the sequence above end-to-end in a temp dir and shows the exit
codes a CI job would see:

```bash
bash examples/ci-gate.sh
```

Both logs pin every token count, so the costs, deltas, and verdicts are
identical on every machine.
