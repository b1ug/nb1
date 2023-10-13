package cmd

import "errors"

// Errors for commands and subcommands
var (
	errNotImplemented    = errors.New("not implemented")
	errConfigKeyNotFound = errors.New("config key not found")
)
