package util

import "strings"

// NormalizeQuery joins the given query strings and returns a normalized query string.
func NormalizeQuery(raw ...string) string {
	return strings.TrimSpace(strings.ToLower(strings.Join(raw, " ")))
}
