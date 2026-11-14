// Package textfmt preserves nb1's established help and configuration formatting.
package textfmt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// HereDoc removes a common ASCII space/tab margin. A leading newline is omitted;
// otherwise the first line is kept as written and does not determine the margin.
func HereDoc(raw string) string {
	lines := strings.Split(raw, "\n")
	first := 1
	if strings.HasPrefix(raw, "\n") {
		lines, first = lines[1:], 0
	}
	margin := -1
	for _, line := range lines[first:] {
		width := len(line) - len(strings.TrimLeft(line, " \t"))
		if width < len(line) && (margin < 0 || width < margin) {
			margin = width
		}
	}
	for i := first; i < len(lines); i++ {
		line := lines[i]
		if i == len(lines)-1 && strings.TrimLeft(line, " \t") == "" && (margin < 0 || len(line) < margin) {
			lines[i] = ""
		} else if margin >= 0 && len(line) >= margin {
			lines[i] = line[margin:]
		}
	}
	return strings.Join(lines, "\n")
}

// HereDocf applies fmt.Sprintf after removing the template's common margin.
func HereDocf(raw string, args ...interface{}) string {
	return fmt.Sprintf(HereDoc(raw), args...)
}

// OneLineJSON formats legacy configuration values without HTML escaping or a
// trailing newline. Values that cannot be encoded retain the historical {} fallback.
func OneLineJSON(value interface{}) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "{}"
	}
	return strings.TrimSpace(buffer.String())
}
