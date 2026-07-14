# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- JSONL usage-log parsing with provider-agnostic field normalization:
  nested `usage` objects, `input_tokens`/`prompt_tokens` and
  `output_tokens`/`completion_tokens` aliases, separate cache-token
  fields, OpenAI-style `cached_tokens` subset carve-out, recorded
  `cost_usd`, and `file:line` errors for malformed lines.
- Committed budget lockfile `costlock.json` (schema_version 1) with
  strict parsing (unknown fields rejected), per-suite baselines,
  absolute caps (`max_cost_usd`, `max_calls`, `max_input_tokens`,
  `max_output_tokens`), per-suite tolerance overrides, an optional
  `total` budget, and byte-deterministic serialization.
- Committed price tables (USD per million tokens) with exact and
  longest-prefix `*` pattern matching, cache-read/write rates, and no
  built-in vendor prices by design.
- `init` subcommand baselining a known-good run, with `--prices`,
  `--tolerance`, `--warn`, `--allow-unpriced`, and overwrite
  protection.
- `check` subcommand gating a run against the lockfile: relative
  tolerance and warn thresholds, absolute caps, `on_new_suite` /
  `on_missing_suite` / `on_unpriced` policies, `--fail-on-warn`, and
  exit code 1 on breach.
- `update` subcommand refreshing baselines while keeping policy,
  prices, and caps, with `--suite` scoping and `--prune`;
  byte-idempotent on an unchanged run.
- `report` subcommand summarizing a run (totals, per-suite, per-model)
  without gating.
- Text, JSON (`schema_version: 1`), and PR-comment-ready Markdown
  output for `check` and `report`; logs from files, directories
  (recursive `*.jsonl`/`*.ndjson`), or stdin.
- Runnable examples (`examples/ci-gate.sh`, captured baseline and
  regression logs, a price table) and a lockfile-format reference
  (`docs/lockfile-format.md`).
- 92 deterministic offline tests (unit + in-process CLI integration
  over fabricated logs) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/costlock/releases/tag/v0.1.0
