# Contributing to costlock

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else — costlock is standard library only.

```bash
git clone https://github.com/JaydenCJ/costlock && cd costlock
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, baselines the example run, gates
a clean run and a regressed run, and asserts on real CLI output and
exit codes across every subcommand; it must finish by printing
`SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (92 deterministic tests, no network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (parsing, pricing, and gating never touch the filesystem —
   only the CLI layer does).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in
  the PR.
- No network calls, ever — costlock reads local log files and a local
  lockfile. No telemetry, no built-in vendor price fetching.
- Determinism first: identical logs plus an identical lockfile must
  produce byte-identical output, including all orderings, and
  `costlock update` on an unchanged run must not churn a single byte.
- New log-field aliases go into the candidate-path tables in
  `internal/usage/usage.go` with a test reproducing the real provider
  payload shape.
- Lockfile schema changes require a `schema_version` bump and a
  migration note in `docs/lockfile-format.md`.
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `costlock version`, the full command you ran,
the check/report output, your `costlock.json` (redact prices if you
must), and — for parsing issues — the exact offending JSONL line,
since the `file:line` in the error message points straight at it.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
