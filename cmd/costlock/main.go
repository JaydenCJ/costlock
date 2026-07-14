// Command costlock is a budget lockfile for CI: it parses LLM usage logs
// from test runs and fails the build when spend regresses past the
// budgets committed in costlock.json.
package main

import (
	"os"

	"github.com/JaydenCJ/costlock/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
