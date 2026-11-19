package main

import (
	"os"

	"github.com/b1ug/nb1/internal/mcpserver"
)

// Release builds set this through -ldflags "-X main.buildVersion=vX.Y.Z".
// An ordinary source build must not claim to be a tagged release.
var buildVersion = "dev"

func main() {
	os.Exit(mcpserver.RunStdioCommand("nb1-mcp", os.Args[1:], buildVersion, os.Stderr))
}
