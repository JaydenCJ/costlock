#!/usr/bin/env bash
# End-to-end smoke test for costlock: builds the binary, baselines the
# example run, gates a clean run and a regressed run, and asserts on
# the real CLI output and exit codes. No network, idempotent, seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/costlock"
LOCK="$WORKDIR/costlock.json"
BASELINE="$ROOT/examples/usage.baseline.jsonl"
REGRESSION="$ROOT/examples/usage.regression.jsonl"
PRICES="$ROOT/examples/prices.json"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/costlock) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" --version | grep -qx "costlock 0.1.0" || fail "--version mismatch"

echo "3. init writes a lockfile from the baseline run"
"$BIN" init --lockfile "$LOCK" --prices "$PRICES" "$BASELINE" \
  | grep -q "2 suite budget(s)" || fail "init summary missing"
grep -q '"schema_version": 1' "$LOCK" || fail "lockfile schema missing"
grep -q '"integration"' "$LOCK" || fail "integration budget missing"

echo "4. init refuses to overwrite without --force"
if "$BIN" init --lockfile "$LOCK" "$BASELINE" >/dev/null 2>&1; then
  fail "init overwrote an existing lockfile"
fi

echo "5. clean run passes with exit 0"
OUT="$("$BIN" check --lockfile "$LOCK" "$BASELINE")" || fail "clean check exited non-zero"
echo "$OUT" | grep -q "check: PASS" || fail "PASS line missing"
echo "$OUT" | grep -q "integration" || fail "suite table missing"

echo "6. regressed run fails with exit 1 and a quotable reason"
set +e
OUT="$("$BIN" check --lockfile "$LOCK" "$REGRESSION")"
CODE=$?
set -e
[ "$CODE" -eq 1 ] || fail "regression check exit $CODE, want 1"
echo "$OUT" | grep -q "BREACH" || fail "BREACH verdict missing"
echo "$OUT" | grep -q "exceeds tolerance +10.0%" || fail "breach reason missing"
echo "$OUT" | grep -q "check: FAIL" || fail "FAIL line missing"

echo "7. JSON output is machine-readable"
set +e
JSON="$("$BIN" check --format json --lockfile "$LOCK" "$REGRESSION")"
set -e
echo "$JSON" | grep -q '"tool": "costlock"' || fail "json envelope missing"
echo "$JSON" | grep -q '"verdict": "breach"' || fail "json verdict wrong"

echo "8. markdown output is PR-comment ready"
set +e
MD="$("$BIN" check --format markdown --lockfile "$LOCK" "$REGRESSION")"
set -e
echo "$MD" | grep -q "| Suite | Baseline | Current |" || fail "markdown table missing"
echo "$MD" | grep -q "costlock check — FAIL" || fail "markdown verdict missing"

echo "9. update accepts the regression, then check passes"
"$BIN" update --lockfile "$LOCK" "$REGRESSION" >/dev/null || fail "update failed"
"$BIN" check --lockfile "$LOCK" "$REGRESSION" >/dev/null || fail "post-update check failed"

echo "10. update is byte-idempotent"
cp "$LOCK" "$WORKDIR/before.json"
"$BIN" update --lockfile "$LOCK" "$REGRESSION" >/dev/null
cmp -s "$WORKDIR/before.json" "$LOCK" || fail "idempotent update churned the lockfile"

echo "11. report summarizes without gating"
"$BIN" report --lockfile "$LOCK" "$BASELINE" | grep -q "total cost" \
  || fail "report totals missing"

echo "12. stdin input works like a file"
"$BIN" check --lockfile "$LOCK" - < "$REGRESSION" | grep -q "check: PASS" \
  || fail "stdin check should pass against the updated lockfile"

echo "13. usage errors exit 2"
set +e
"$BIN" check --format yaml --lockfile "$LOCK" "$BASELINE" >/dev/null 2>&1
CODE=$?
set -e
[ "$CODE" -eq 2 ] || fail "bad --format should exit 2, got $CODE"

echo "SMOKE OK"
