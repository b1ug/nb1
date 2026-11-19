// Package main is the entry point of the application.
package main

import (
	"os"

	"github.com/b1ug/nb1/cmd"
	"github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/internal/mcpserver"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		os.Exit(mcpserver.RunStdioCommand("nb1 mcp", os.Args[2:], mcpserver.Version(config.GitSummary), os.Stderr))
	}
	cmd.Execute()
}
