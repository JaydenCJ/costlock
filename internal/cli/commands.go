// Subcommand implementations: init, check, update, report.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/JaydenCJ/costlock/internal/aggregate"
	"github.com/JaydenCJ/costlock/internal/gate"
	"github.com/JaydenCJ/costlock/internal/lockfile"
	"github.com/JaydenCJ/costlock/internal/pricing"
	"github.com/JaydenCJ/costlock/internal/render"
	"github.com/JaydenCJ/costlock/internal/usage"
)

// commonFlags are shared by every log-consuming subcommand.
type commonFlags struct {
	lockPath string
	suiteKey string
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.lockPath, "lockfile", "costlock.json", "lockfile path")
	fs.StringVar(&c.suiteKey, "suite-key", "", "dotted JSON path naming the suite")
}

// loadRun parses logs and aggregates them against the given prices.
func loadRun(paths []string, stdin io.Reader, suiteKey string, prices pricing.Table, preferRecorded bool) (aggregate.Run, error) {
	recs, sources, err := usage.LoadPaths(paths, stdin, usage.Options{SuiteKey: suiteKey})
	if err != nil {
		return aggregate.Run{}, err
	}
	return aggregate.Build(recs, prices, preferRecorded, sources), nil
}

// baselineFrom converts an aggregated usage rollup into a lockfile
// baseline, rounding cost to the lockfile's money precision.
func baselineFrom(u aggregate.Usage) lockfile.Baseline {
	return lockfile.Baseline{
		CostUSD:          lockfile.RoundCost(u.CostUSD),
		Calls:            u.Calls,
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
	}
}

func runInit(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("init")
	var common commonFlags
	common.register(fs)
	pricesPath := fs.String("prices", "", "JSON price table to embed")
	tolerance := fs.Float64("tolerance", 10, "allowed cost growth percent")
	warn := fs.Float64("warn", 5, "warn threshold percent")
	allowUnpriced := fs.Bool("allow-unpriced", false, "proceed with unpriced records")
	force := fs.Bool("force", false, "overwrite an existing lockfile")
	if err := fs.Parse(args); err != nil {
		return parseErr(err, "init", stdout, stderr)
	}
	if *tolerance < 0 || *warn < 0 {
		return usageErr(stderr, "init: --tolerance and --warn must be >= 0")
	}

	if !*force {
		if _, err := os.Stat(common.lockPath); err == nil {
			return runtimeErr(stderr, fmt.Errorf("%s already exists (use --force to overwrite, or `costlock update` to refresh baselines)", common.lockPath))
		}
	}

	lf := lockfile.New()
	lf.Policy.TolerancePct = *tolerance
	lf.Policy.WarnPct = *warn
	if *pricesPath != "" {
		table, err := loadPrices(*pricesPath)
		if err != nil {
			return runtimeErr(stderr, err)
		}
		lf.Prices = table
	}

	run, err := loadRun(fs.Args(), stdin, common.suiteKey, lf.Prices, lf.Policy.PreferRecordedCost)
	if err != nil {
		return runtimeErr(stderr, err)
	}
	if run.Total.UnpricedCalls > 0 && !*allowUnpriced {
		return runtimeErr(stderr, fmt.Errorf(
			"%d record(s) could not be priced (models: %s); pass --prices or --allow-unpriced",
			run.Total.UnpricedCalls, strings.Join(run.UnpricedModels, ", ")))
	}

	for _, name := range run.SuiteNames() {
		lf.Budgets[name] = lockfile.Budget{Baseline: baselineFrom(run.Suites[name].Usage)}
	}
	lf.Total = &lockfile.Budget{Baseline: baselineFrom(run.Total)}

	if err := lf.Save(common.lockPath); err != nil {
		return runtimeErr(stderr, err)
	}
	fmt.Fprintf(stdout, "wrote %s: %d suite budget(s), total baseline $%.4f (tolerance +%.1f%%)\n",
		common.lockPath, len(lf.Budgets), lf.Total.Baseline.CostUSD, lf.Policy.TolerancePct)
	if run.Total.UnpricedCalls > 0 {
		fmt.Fprintf(stderr, "warning: %d unpriced record(s) baselined at $0 (models: %s)\n",
			run.Total.UnpricedCalls, strings.Join(run.UnpricedModels, ", "))
	}
	return ExitOK
}

func runCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("check")
	var common commonFlags
	common.register(fs)
	format := fs.String("format", "text", "output format: text, json, or markdown")
	failOnWarn := fs.Bool("fail-on-warn", false, "exit 1 on warnings too")
	if err := fs.Parse(args); err != nil {
		return parseErr(err, "check", stdout, stderr)
	}
	if !validFormat(*format) {
		return usageErr(stderr, "check: unknown --format %q (want text, json, or markdown)", *format)
	}

	lf, err := lockfile.Load(common.lockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runtimeErr(stderr, fmt.Errorf("%s not found; run `costlock init <logs...>` on a baseline run first", common.lockPath))
		}
		return runtimeErr(stderr, err)
	}
	run, err := loadRun(fs.Args(), stdin, common.suiteKey, lf.Prices, lf.Policy.PreferRecordedCost)
	if err != nil {
		return runtimeErr(stderr, err)
	}

	res := gate.Check(run, lf)
	switch *format {
	case "json":
		if err := render.CheckJSON(stdout, res, run); err != nil {
			return runtimeErr(stderr, err)
		}
	case "markdown":
		render.CheckMarkdown(stdout, res, run)
	default:
		render.CheckText(stdout, res, common.lockPath, run)
	}

	if res.Verdict == gate.Breach || (*failOnWarn && res.Verdict == gate.Warn) {
		return ExitBreach
	}
	return ExitOK
}

func runUpdate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("update")
	var common commonFlags
	common.register(fs)
	var only multiFlag
	fs.Var(&only, "suite", "update only this suite (repeatable)")
	prune := fs.Bool("prune", false, "drop budgets for suites absent from the run")
	if err := fs.Parse(args); err != nil {
		return parseErr(err, "update", stdout, stderr)
	}
	if *prune && len(only) > 0 {
		return usageErr(stderr, "update: --prune cannot be combined with --suite (pruning judges the whole run)")
	}

	lf, err := lockfile.Load(common.lockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runtimeErr(stderr, fmt.Errorf("%s not found; run `costlock init <logs...>` first", common.lockPath))
		}
		return runtimeErr(stderr, err)
	}
	run, err := loadRun(fs.Args(), stdin, common.suiteKey, lf.Prices, lf.Policy.PreferRecordedCost)
	if err != nil {
		return runtimeErr(stderr, err)
	}

	wanted := func(name string) bool {
		if len(only) == 0 {
			return true
		}
		for _, o := range only {
			if o == name {
				return true
			}
		}
		return false
	}
	for _, o := range only {
		if _, inRun := run.Suites[o]; !inRun {
			return runtimeErr(stderr, fmt.Errorf("--suite %s: no records in this run", o))
		}
	}

	updated, added, pruned := 0, 0, 0
	for _, name := range run.SuiteNames() {
		if !wanted(name) {
			continue
		}
		b, exists := lf.Budgets[name]
		b.Baseline = baselineFrom(run.Suites[name].Usage)
		lf.Budgets[name] = b
		if exists {
			updated++
		} else {
			added++
		}
	}
	if *prune {
		for name := range lf.Budgets {
			if _, inRun := run.Suites[name]; !inRun {
				delete(lf.Budgets, name)
				pruned++
			}
		}
	}
	if lf.Total != nil && len(only) == 0 {
		lf.Total.Baseline = baselineFrom(run.Total)
	}

	if err := lf.Save(common.lockPath); err != nil {
		return runtimeErr(stderr, err)
	}
	fmt.Fprintf(stdout, "updated %s: %d baseline(s) refreshed, %d suite(s) added, %d pruned\n",
		common.lockPath, updated, added, pruned)

	var stale []string
	for name := range lf.Budgets {
		if _, inRun := run.Suites[name]; !inRun {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		fmt.Fprintf(stderr, "warning: %d budgeted suite(s) had no records and kept their old baselines: %s\n",
			len(stale), strings.Join(stale, ", "))
	}
	return ExitOK
}

func runReport(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("report")
	var common commonFlags
	common.register(fs)
	format := fs.String("format", "text", "output format: text, json, or markdown")
	if err := fs.Parse(args); err != nil {
		return parseErr(err, "report", stdout, stderr)
	}
	if !validFormat(*format) {
		return usageErr(stderr, "report: unknown --format %q (want text, json, or markdown)", *format)
	}

	// report prices with the lockfile when present, but does not
	// require one: it is a summary, not a gate.
	prices := pricing.Table{}
	preferRecorded := true
	if lf, err := lockfile.Load(common.lockPath); err == nil {
		prices = lf.Prices
		preferRecorded = lf.Policy.PreferRecordedCost
	} else if !errors.Is(err, os.ErrNotExist) {
		return runtimeErr(stderr, err)
	}

	run, err := loadRun(fs.Args(), stdin, common.suiteKey, prices, preferRecorded)
	if err != nil {
		return runtimeErr(stderr, err)
	}
	switch *format {
	case "json":
		if err := render.ReportJSON(stdout, run); err != nil {
			return runtimeErr(stderr, err)
		}
	case "markdown":
		render.ReportMarkdown(stdout, run)
	default:
		render.ReportText(stdout, run)
	}
	return ExitOK
}

func validFormat(f string) bool {
	switch f {
	case "text", "json", "markdown":
		return true
	}
	return false
}

// loadPrices reads a standalone JSON price table (model -> rate).
// Parsing is as strict as the lockfile's: an unknown rate field (say,
// "input" instead of "input_per_mtok") must error out rather than
// silently price every call at $0 and bake a useless baseline in.
func loadPrices(path string) (pricing.Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var table pricing.Table
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&table); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if err := table.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return table, nil
}
