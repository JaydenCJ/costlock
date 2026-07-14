// Package cli implements the costlock command-line interface. Run
// takes argv plus three streams and returns an exit code, so the whole
// surface is testable in-process without building a binary.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/JaydenCJ/costlock/internal/version"
)

// Exit codes. Documented in the README; `check` uses ExitBreach as its
// machine-readable verdict.
const (
	ExitOK      = 0
	ExitBreach  = 1
	ExitUsage   = 2
	ExitRuntime = 3
)

// Run dispatches argv and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "init":
		return runInit(args[1:], stdin, stdout, stderr)
	case "check":
		return runCheck(args[1:], stdin, stdout, stderr)
	case "update":
		return runUpdate(args[1:], stdin, stdout, stderr)
	case "report":
		return runReport(args[1:], stdin, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "costlock %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		printUsage(stdout)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "costlock: unknown command %q\n\n", args[0])
		printUsage(stderr)
		return ExitUsage
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `costlock — budget lockfile for CI

Usage:
  costlock init   [flags] <logs...>   create costlock.json from a baseline run
  costlock check  [flags] <logs...>   gate a run against costlock.json (exit 1 on breach)
  costlock update [flags] <logs...>   refresh baselines; policy, prices and caps are kept
  costlock report [flags] <logs...>   summarize a run without gating
  costlock version                    print the version

Logs are JSONL files, directories (searched for *.jsonl / *.ndjson), or - for stdin.

Common flags:
  --lockfile PATH    lockfile location (default costlock.json)
  --suite-key KEY    dotted JSON path that names the suite (default: suite, test, group, tags.suite)

init flags:
  --prices FILE      merge a JSON price table into the new lockfile
  --tolerance PCT    allowed cost growth before breach (default 10)
  --warn PCT         growth that triggers a warning (default 5)
  --allow-unpriced   proceed even when records cannot be priced
  --force            overwrite an existing lockfile

check / report flags:
  --format FORMAT    text, json, or markdown (default text)

check flags:
  --fail-on-warn     exit 1 on warnings too

update flags:
  --suite NAME       update only this suite's baseline (repeatable)
  --prune            drop budgets for suites absent from the run

Exit codes: 0 ok, 1 breach, 2 usage error, 3 runtime error.
`)
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// newFlagSet builds a silent FlagSet whose errors we render ourselves.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// parseErr maps a flag-parse failure to an exit code: -h/--help on a
// subcommand prints the usage text and succeeds, anything else is a
// usage error.
func parseErr(err error, cmd string, stdout, stderr io.Writer) int {
	if errors.Is(err, flag.ErrHelp) {
		printUsage(stdout)
		return ExitOK
	}
	return usageErr(stderr, "%s: %v", cmd, err)
}

// usageErr prints a usage-class error and returns ExitUsage.
func usageErr(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "costlock: "+format+"\n", args...)
	return ExitUsage
}

// runtimeErr prints a runtime-class error and returns ExitRuntime.
func runtimeErr(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "costlock: %v\n", err)
	return ExitRuntime
}
