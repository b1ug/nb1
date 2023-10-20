package util

import (
	"path/filepath"
	"strings"
)

// NormalizeQuery joins the given query strings and returns a normalized query string.
func NormalizeQuery(raw ...string) string {
	return strings.TrimSpace(strings.ToLower(strings.Join(raw, " ")))
}

// SplitConfigKey splits the given config key into two parts: the first part is the top-level key, the second part is the rest.
func SplitConfigKey(key string) (k1 string, kr string) {
	ks := strings.SplitN(strings.ToLower(strings.TrimSpace(key)), ".", 2)
	k1 = ks[0]
	if len(ks) >= 2 {
		kr = ks[1]
	}
	return k1, kr
}

// JoinWrapSlice joins the given slice of strings with the given separator and wraps the result string to multiple lines if it exceeds the given maxLen.
func JoinWrapSlice(elems []string, separator string, maxLen int) string {
	var result strings.Builder
	curLen := 0

	// Iterating over slice to join return string
	for i, str := range elems {

		sepLen := len(separator)
		strLenWithSep := len(str) + sepLen

		if i != 0 && curLen+strLenWithSep > maxLen {
			// Start new line when exceeding maxLen
			result.WriteRune('\n')
			curLen = 0
		}

		if i > 0 && curLen > 0 {
			// Adds separator only between words and not at the beginning of a new line
			result.WriteString(separator)
			curLen += sepLen
		}

		result.WriteString(str)
		curLen += len(str)
	}

	return result.String()
}

// ChangeFileExt changes the file extension of the given file path to the given new extension.
func ChangeFileExt(filePath string, newExt string) string {
	ext := filepath.Ext(filePath)
	base := filePath[:len(filePath)-len(ext)]
	newFilePath := base + newExt
	return newFilePath
}
