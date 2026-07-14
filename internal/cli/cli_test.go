// In-process CLI integration tests: full init → check → update flows
// over fabricated JSONL logs in temp dirs, asserting on exit codes and
// real output. No binary is built and nothing touches the network.
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI invokes the CLI in-process and captures both streams.
func runCLI(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func write(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// pricesJSON is the committed price table used across these tests.
const pricesJSON = `{
  "model-a": {"input_per_mtok": 5, "output_per_mtok": 10},
  "model-b*": {"input_per_mtok": 1, "output_per_mtok": 2}
}`

// baselineLog yields integration=$0.125 and unit=$0.025 under pricesJSON.
const baselineLog = `{"suite":"integration","model":"model-a","usage":{"input_tokens":20000,"output_tokens":2500}}
{"suite":"unit","model":"model-a","usage":{"input_tokens":4000,"output_tokens":500}}
`

// regressionLog doubles integration's spend.
const regressionLog = `{"suite":"integration","model":"model-a","usage":{"input_tokens":40000,"output_tokens":5000}}
{"suite":"unit","model":"model-a","usage":{"input_tokens":4000,"output_tokens":500}}
`

// setup creates a workspace with prices, a baseline log, and an
// initialized lockfile; it returns the dir and the lockfile path.
func setup(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	prices := write(t, filepath.Join(dir, "prices.json"), pricesJSON)
	log := write(t, filepath.Join(dir, "baseline.jsonl"), baselineLog)
	lock := filepath.Join(dir, "costlock.json")
	code, out, errOut := runCLI(t, "", "init", "--lockfile", lock, "--prices", prices, log)
	if code != ExitOK {
		t.Fatalf("init exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	return dir, lock
}

func TestTopLevelDispatch(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		code, out, _ := runCLI(t, "", arg)
		if code != ExitOK || out != "costlock 0.1.0\n" {
			t.Fatalf("%s: exit %d out %q", arg, code, out)
		}
	}
	code, out, _ := runCLI(t, "", "help")
	if code != ExitOK || !strings.Contains(out, "budget lockfile for CI") {
		t.Fatalf("help: exit %d out %q", code, out)
	}
	code, _, errOut := runCLI(t, "")
	if code != ExitUsage || !strings.Contains(errOut, "Usage:") {
		t.Fatalf("no args: exit %d", code)
	}
	code, _, errOut = runCLI(t, "", "frobnicate")
	if code != ExitUsage || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("unknown command: exit %d stderr %q", code, errOut)
	}
}

func TestSubcommandHelpFlagPrintsUsageAndExitsZero(t *testing.T) {
	// `costlock check --help` is a question, not a mistake: it must
	// print the usage text on stdout and exit 0, not 2.
	for _, cmd := range []string{"init", "check", "update", "report"} {
		code, out, _ := runCLI(t, "", cmd, "--help")
		if code != ExitOK || !strings.Contains(out, "Usage:") {
			t.Fatalf("%s --help: exit %d out %q", cmd, code, out)
		}
	}
}

func TestInitWritesLockfileWithBaselines(t *testing.T) {
	_, lock := setup(t)
	data, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("lockfile is not JSON: %v", err)
	}
	budgets := doc["budgets"].(map[string]any)
	if len(budgets) != 2 {
		t.Fatalf("budgets = %v", budgets)
	}
	integ := budgets["integration"].(map[string]any)["baseline"].(map[string]any)
	if integ["cost_usd"].(float64) != 0.125 {
		t.Fatalf("integration baseline = %v", integ)
	}
	if doc["total"] == nil {
		t.Fatal("total budget missing")
	}
}

func TestInitRefusesToOverwriteWithoutForce(t *testing.T) {
	dir, lock := setup(t)
	log := filepath.Join(dir, "baseline.jsonl")
	code, _, errOut := runCLI(t, "", "init", "--lockfile", lock, log)
	if code != ExitRuntime || !strings.Contains(errOut, "already exists") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	code, _, _ = runCLI(t, "", "init", "--force", "--lockfile", lock,
		"--prices", filepath.Join(dir, "prices.json"), log)
	if code != ExitOK {
		t.Fatalf("--force exit %d", code)
	}
}

func TestInitFailsOnUnpricedWithoutOptOut(t *testing.T) {
	dir := t.TempDir()
	log := write(t, filepath.Join(dir, "run.jsonl"), `{"suite":"s","model":"mystery","usage":{"input_tokens":10,"output_tokens":1}}`+"\n")
	lock := filepath.Join(dir, "costlock.json")
	code, _, errOut := runCLI(t, "", "init", "--lockfile", lock, log)
	if code != ExitRuntime || !strings.Contains(errOut, "mystery") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
	code, _, errOut = runCLI(t, "", "init", "--allow-unpriced", "--lockfile", lock, log)
	if code != ExitOK || !strings.Contains(errOut, "warning:") {
		t.Fatalf("--allow-unpriced exit %d stderr %q", code, errOut)
	}

	// A typo'd rate field in --prices must be rejected, not silently
	// decoded to a $0 rate that "prices" everything and defeats the
	// on_unpriced gate. Regression guard for loadPrices strictness.
	typoed := write(t, filepath.Join(dir, "typo-prices.json"),
		`{"mystery": {"input": 5, "output_per_mtok": 10}}`)
	code, _, errOut = runCLI(t, "", "init", "--force", "--lockfile", lock,
		"--prices", typoed, log)
	if code != ExitRuntime || !strings.Contains(errOut, "input") {
		t.Fatalf("typo'd prices exit %d stderr %q", code, errOut)
	}
}

func TestCheckPassesBaselineAndFailsRegression(t *testing.T) {
	dir, lock := setup(t)
	code, out, _ := runCLI(t, "", "check", "--lockfile", lock, filepath.Join(dir, "baseline.jsonl"))
	if code != ExitOK || !strings.Contains(out, "check: PASS") {
		t.Fatalf("baseline: exit %d\n%s", code, out)
	}
	log := write(t, filepath.Join(dir, "regression.jsonl"), regressionLog)
	code, out, _ = runCLI(t, "", "check", "--lockfile", lock, log)
	if code != ExitBreach {
		t.Fatalf("regression: exit %d, want %d\n%s", code, ExitBreach, out)
	}
	for _, want := range []string{"integration", "+100.0%", "BREACH", "check: FAIL"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestCheckJSONFormatIsMachineReadable(t *testing.T) {
	dir, lock := setup(t)
	log := write(t, filepath.Join(dir, "regression.jsonl"), regressionLog)
	code, out, _ := runCLI(t, "", "check", "--format", "json", "--lockfile", lock, log)
	if code != ExitBreach {
		t.Fatalf("exit %d", code)
	}
	var doc struct {
		Tool    string `json:"tool"`
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if doc.Tool != "costlock" || doc.Verdict != "breach" {
		t.Fatalf("doc = %+v", doc)
	}
}

func TestCheckMarkdownFormat(t *testing.T) {
	dir, lock := setup(t)
	code, out, _ := runCLI(t, "", "check", "--format", "markdown", "--lockfile", lock, filepath.Join(dir, "baseline.jsonl"))
	if code != ExitOK || !strings.Contains(out, "### costlock check — PASS") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	dir, lock := setup(t)
	code, _, errOut := runCLI(t, "", "check", "--format", "yaml", "--lockfile", lock, filepath.Join(dir, "baseline.jsonl"))
	if code != ExitUsage || !strings.Contains(errOut, "yaml") {
		t.Fatalf("bad --format: exit %d stderr %q", code, errOut)
	}
	if code, _, _ := runCLI(t, "", "check", "--frobnicate"); code != ExitUsage {
		t.Fatalf("unknown flag: exit %d, want %d", code, ExitUsage)
	}
	if code, _, _ := runCLI(t, "", "init", "--tolerance", "-5", "x.jsonl"); code != ExitUsage {
		t.Fatalf("negative tolerance: exit %d, want %d", code, ExitUsage)
	}
}

func TestCheckWithoutLockfileExplainsInit(t *testing.T) {
	dir := t.TempDir()
	log := write(t, filepath.Join(dir, "run.jsonl"), baselineLog)
	code, _, errOut := runCLI(t, "", "check", "--lockfile", filepath.Join(dir, "costlock.json"), log)
	if code != ExitRuntime || !strings.Contains(errOut, "costlock init") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
}

func TestCheckNewSuiteFailsByDefault(t *testing.T) {
	dir, lock := setup(t)
	log := write(t, filepath.Join(dir, "extra.jsonl"),
		baselineLog+`{"suite":"brand-new","model":"model-a","usage":{"input_tokens":100,"output_tokens":10}}`+"\n")
	code, out, _ := runCLI(t, "", "check", "--lockfile", lock, log)
	if code != ExitBreach || !strings.Contains(out, "brand-new") || !strings.Contains(out, "NEW") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestCheckFailOnWarnPromotesWarnings(t *testing.T) {
	dir, lock := setup(t)
	// +8%: above warn (5), below tolerance (10).
	log := write(t, filepath.Join(dir, "drift.jsonl"),
		`{"suite":"integration","model":"model-a","usage":{"input_tokens":21600,"output_tokens":2700}}
{"suite":"unit","model":"model-a","usage":{"input_tokens":4000,"output_tokens":500}}
`)
	code, out, _ := runCLI(t, "", "check", "--lockfile", lock, log)
	if code != ExitOK || !strings.Contains(out, "warn") {
		t.Fatalf("plain check: exit %d\n%s", code, out)
	}
	code, _, _ = runCLI(t, "", "check", "--fail-on-warn", "--lockfile", lock, log)
	if code != ExitBreach {
		t.Fatalf("--fail-on-warn: exit %d, want %d", code, ExitBreach)
	}
}

func TestCheckReadsStdin(t *testing.T) {
	_, lock := setup(t)
	code, out, _ := runCLI(t, baselineLog, "check", "--lockfile", lock, "-")
	if code != ExitOK || !strings.Contains(out, "check: PASS") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestCheckReadsLogDirectory(t *testing.T) {
	dir, lock := setup(t)
	logs := filepath.Join(dir, "logs")
	write(t, filepath.Join(logs, "a.jsonl"), `{"suite":"integration","model":"model-a","usage":{"input_tokens":20000,"output_tokens":2500}}`+"\n")
	write(t, filepath.Join(logs, "nested", "b.jsonl"), `{"suite":"unit","model":"model-a","usage":{"input_tokens":4000,"output_tokens":500}}`+"\n")
	code, out, _ := runCLI(t, "", "check", "--lockfile", lock, logs)
	if code != ExitOK || !strings.Contains(out, "2 source(s)") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestUpdateRefreshesBaselines(t *testing.T) {
	dir, lock := setup(t)
	log := write(t, filepath.Join(dir, "regression.jsonl"), regressionLog)
	code, out, _ := runCLI(t, "", "update", "--lockfile", lock, log)
	if code != ExitOK || !strings.Contains(out, "2 baseline(s) refreshed") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// The regression is now the accepted baseline: check passes.
	code, _, _ = runCLI(t, "", "check", "--lockfile", lock, log)
	if code != ExitOK {
		t.Fatalf("post-update check exit %d", code)
	}
}

func TestUpdateOnlyNamedSuite(t *testing.T) {
	dir, lock := setup(t)
	log := write(t, filepath.Join(dir, "regression.jsonl"), regressionLog)
	code, _, _ := runCLI(t, "", "update", "--suite", "unit", "--lockfile", lock, log)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	// integration's baseline was NOT refreshed, so it still breaches.
	code, out, _ := runCLI(t, "", "check", "--lockfile", lock, log)
	if code != ExitBreach || !strings.Contains(out, "integration") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// Naming a suite with no records in the run is a hard error.
	code, _, errOut := runCLI(t, "", "update", "--suite", "nope", "--lockfile", lock, log)
	if code != ExitRuntime || !strings.Contains(errOut, "nope") {
		t.Fatalf("unknown suite: exit %d stderr %q", code, errOut)
	}
}

func TestUpdatePruneDropsStaleSuites(t *testing.T) {
	dir, lock := setup(t)
	// A run where only "unit" remains.
	log := write(t, filepath.Join(dir, "unit-only.jsonl"),
		`{"suite":"unit","model":"model-a","usage":{"input_tokens":4000,"output_tokens":500}}`+"\n")
	code, out, errOut := runCLI(t, "", "update", "--prune", "--lockfile", lock, log)
	if code != ExitOK || !strings.Contains(out, "1 pruned") {
		t.Fatalf("exit %d out %q stderr %q", code, out, errOut)
	}
	data, _ := os.ReadFile(lock)
	if strings.Contains(string(data), "integration") {
		t.Fatalf("integration should be pruned:\n%s", data)
	}
}

func TestUpdatePruneRejectsSuiteScoping(t *testing.T) {
	// --prune judges every budget against the whole run; combined with
	// --suite it would delete budgets the user never asked it to look
	// at, so the pairing is an explicit usage error, not a silent no-op.
	dir, lock := setup(t)
	log := filepath.Join(dir, "baseline.jsonl")
	code, _, errOut := runCLI(t, "", "update", "--prune", "--suite", "unit", "--lockfile", lock, log)
	if code != ExitUsage || !strings.Contains(errOut, "--prune cannot be combined with --suite") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
}

func TestUpdateWithoutPruneWarnsAboutStale(t *testing.T) {
	dir, lock := setup(t)
	log := write(t, filepath.Join(dir, "unit-only.jsonl"),
		`{"suite":"unit","model":"model-a","usage":{"input_tokens":4000,"output_tokens":500}}`+"\n")
	code, _, errOut := runCLI(t, "", "update", "--lockfile", lock, log)
	if code != ExitOK || !strings.Contains(errOut, "integration") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
}

func TestReportTextWithoutLockfile(t *testing.T) {
	dir := t.TempDir()
	log := write(t, filepath.Join(dir, "run.jsonl"),
		`{"suite":"unit","model":"m","cost_usd":0.03,"usage":{"input_tokens":100,"output_tokens":10}}`+"\n")
	code, out, _ := runCLI(t, "", "report", "--lockfile", filepath.Join(dir, "absent.json"), log)
	if code != ExitOK || !strings.Contains(out, "total cost   $0.0300") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestReportUsesLockfilePricesInTextAndJSON(t *testing.T) {
	dir, lock := setup(t)
	code, out, _ := runCLI(t, "", "report", "--lockfile", lock, filepath.Join(dir, "baseline.jsonl"))
	if code != ExitOK || !strings.Contains(out, "total cost   $0.1500") {
		t.Fatalf("text: exit %d\n%s", code, out)
	}
	code, out, _ = runCLI(t, "", "report", "--format", "json", "--lockfile", lock, filepath.Join(dir, "baseline.jsonl"))
	if code != ExitOK {
		t.Fatalf("json: exit %d", code)
	}
	var doc struct {
		Total struct {
			CostUSD float64 `json:"cost_usd"`
		} `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Total.CostUSD != 0.15 {
		t.Fatalf("err=%v doc=%+v\n%s", err, doc, out)
	}
}

func TestSuiteKeyFlagRoutesRecords(t *testing.T) {
	dir, lock := setup(t)
	log := write(t, filepath.Join(dir, "tagged.jsonl"),
		`{"meta":{"bucket":"integration"},"model":"model-a","usage":{"input_tokens":20000,"output_tokens":2500}}
{"meta":{"bucket":"unit"},"model":"model-a","usage":{"input_tokens":4000,"output_tokens":500}}
`)
	code, out, _ := runCLI(t, "", "check", "--suite-key", "meta.bucket", "--lockfile", lock, log)
	if code != ExitOK || !strings.Contains(out, "check: PASS") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestMalformedLogLineIsRuntimeErrorWithLocation(t *testing.T) {
	dir, lock := setup(t)
	log := write(t, filepath.Join(dir, "bad.jsonl"), "{\"suite\":\"unit\"}\nnot json\n")
	code, _, errOut := runCLI(t, "", "check", "--lockfile", lock, log)
	if code != ExitRuntime || !strings.Contains(errOut, "bad.jsonl:2") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
}

func TestCorruptLockfileIsRuntimeError(t *testing.T) {
	dir := t.TempDir()
	lock := write(t, filepath.Join(dir, "costlock.json"), `{"schema_version": 99}`)
	log := write(t, filepath.Join(dir, "run.jsonl"), baselineLog)
	code, _, errOut := runCLI(t, "", "check", "--lockfile", lock, log)
	if code != ExitRuntime || !strings.Contains(errOut, "schema_version") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
}

func TestLockfileIsStableAcrossIdempotentUpdate(t *testing.T) {
	// update with the same logs must not churn a single byte —
	// that's what makes costlock.json reviewable.
	dir, lock := setup(t)
	before, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ := runCLI(t, "", "update", "--lockfile", lock, filepath.Join(dir, "baseline.jsonl"))
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	after, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("idempotent update changed the lockfile:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
