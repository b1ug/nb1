// Package buildmeta contains small linker-set release fields shared by the
// native CLI, diagnostics and daemon HTTP API. It intentionally has no runtime
// configuration or external dependencies so the standalone MCP build can use it.
package buildmeta

var (
	GitCommit  string
	GitSummary string
)
