// costlock — budget lockfile for CI: parses LLM usage logs from test
// runs and fails the build on cost regressions.
//
// version:    0.1.0
// author:     JaydenCJ
// license:    MIT
// repository: https://github.com/JaydenCJ/costlock
// keywords:   ci, llm, cost, budget, lockfile, tokens, regression
//
// Zero runtime dependencies: standard library only.
module github.com/JaydenCJ/costlock

go 1.22
