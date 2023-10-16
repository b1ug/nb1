package util

import "strings"

// NormalizeQuery joins the given query strings and returns a normalized query string.
func NormalizeQuery(raw ...string) string {
	return strings.TrimSpace(strings.ToLower(strings.Join(raw, " ")))
}

// SplitConfigKey splits the given config key into two parts: the first part is the top-level key, the second part is the rest.
func SplitConfigKey(key string) (string, string) {
	ks := strings.SplitN(strings.ToLower(strings.TrimSpace(key)), ".", 2)
	k1 := ks[0]
	var kr string
	if len(ks) >= 2 {
		kr = ks[1]
	}
	return k1, kr
}
