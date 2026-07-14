#!/usr/bin/env bash
# ci-gate.sh — the costlock CI story in one script: baseline a run,
# gate a clean run (pass), gate a regressed run (fail), then accept the
# regression with `update`. Offline, deterministic, idempotent.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

BIN="$WORKDIR/costlock"
LOCK="$WORKDIR/costlock.json"
(cd "$ROOT" && go build -o "$BIN" ./cmd/costlock)

echo "== 1. baseline the known-good run =="
"$BIN" init --lockfile "$LOCK" --prices "$ROOT/examples/prices.json" \
  "$ROOT/examples/usage.baseline.jsonl"

echo
echo "== 2. clean run passes =="
"$BIN" check --lockfile "$LOCK" "$ROOT/examples/usage.baseline.jsonl"
echo "exit: $?"

echo
echo "== 3. regressed run fails the build =="
if "$BIN" check --lockfile "$LOCK" "$ROOT/examples/usage.regression.jsonl"; then
  echo "unexpected: regression passed" >&2
  exit 1
else
  echo "exit: $? (this is what fails your CI job)"
fi

echo
echo "== 4. the regression was intentional: accept it =="
"$BIN" update --lockfile "$LOCK" "$ROOT/examples/usage.regression.jsonl"
"$BIN" check --lockfile "$LOCK" "$ROOT/examples/usage.regression.jsonl" >/dev/null
echo "post-update check exit: $?"
