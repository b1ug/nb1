package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This is the P1 JSON integrity grammar at 0fe12e9, not an Action compiler.
// Numeric lexemes survive parsing so a caller can reject integer fields such
// as cycles:1.0 before canonicalization would turn that spelling into 1.
func parsePatternAuthorValue(data []byte, maxBytes, maxDepth int) (any, error) {
	if maxBytes < 1 || maxDepth < 0 {
		return nil, fmt.Errorf("invalid Pattern JSON bounds")
	}
	if len(data) > maxBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("Pattern JSON exceeds its byte budget or is not UTF-8")
	}
	if err := patternAuthorCheckStrings(data); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := patternAuthorReadValue(decoder, 0, maxDepth)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("one Pattern JSON value required; trailing input rejected")
	}
	return value, nil
}

func patternAuthorReadValue(decoder *json.Decoder, depth, maxDepth int) (any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("Pattern JSON nesting exceeds %d", maxDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("Pattern JSON: %w", err)
	}
	switch value := token.(type) {
	case nil:
		return nil, fmt.Errorf("explicit null is not accepted")
	case string, bool:
		return value, nil
	case json.Number:
		if _, err := patternAuthorCanonicalNumber(string(value)); err != nil {
			return nil, err
		}
		return value, nil
	case json.Delim:
		switch value {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, fmt.Errorf("Pattern object key: %w", err)
				}
				key, ok := keyToken.(string)
				if !ok || !patternAuthorASCIIKey(key) {
					return nil, fmt.Errorf("Pattern object keys must be ASCII strings")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate Pattern field %q", key)
				}
				child, err := patternAuthorReadValue(decoder, depth+1, maxDepth)
				if err != nil {
					return nil, err
				}
				object[key] = child
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("invalid Pattern object end")
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				child, err := patternAuthorReadValue(decoder, depth+1, maxDepth)
				if err != nil {
					return nil, err
				}
				array = append(array, child)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("invalid Pattern array end")
			}
			return array, nil
		}
	}
	return nil, fmt.Errorf("unsupported Pattern JSON token")
}

// Canonical values are only parser AST values. In particular float64 and typed
// Go structs are not silently coerced. The P1 depth ceiling also prevents cycles
// in a manually constructed AST from exhausting the stack.
func canonicalPatternAuthorValue(value any, maxBytes int) ([]byte, error) {
	if maxBytes < 1 {
		return nil, fmt.Errorf("invalid Pattern canonical byte budget")
	}
	writer := patternAuthorJSONWriter{limit: maxBytes}
	if err := writer.value(value, 0); err != nil {
		return nil, err
	}
	return writer.data, nil
}

func canonicalPatternAuthorRaw(data []byte, maxBytes, maxDepth int) ([]byte, error) {
	value, err := parsePatternAuthorValue(data, maxBytes, maxDepth)
	if err != nil {
		return nil, err
	}
	return canonicalPatternAuthorValue(value, maxBytes)
}

type patternAuthorJSONWriter struct {
	data  []byte
	limit int
}

func (w *patternAuthorJSONWriter) raw(value string) error {
	if len(value) > w.limit-len(w.data) {
		return fmt.Errorf("canonical Pattern JSON exceeds its byte budget")
	}
	w.data = append(w.data, value...)
	return nil
}

func (w *patternAuthorJSONWriter) value(input any, depth int) error {
	if depth > 32 {
		return fmt.Errorf("canonical Pattern JSON exceeds P1 depth 32")
	}
	switch value := input.(type) {
	case string:
		return w.text(value)
	case bool:
		if value {
			return w.raw("true")
		}
		return w.raw("false")
	case json.Number:
		number, err := patternAuthorCanonicalNumber(string(value))
		if err != nil {
			return err
		}
		return w.raw(number)
	case []any:
		if value == nil {
			return fmt.Errorf("nil Pattern array is not a parsed JSON value")
		}
		if err := w.raw("["); err != nil {
			return err
		}
		for index, child := range value {
			if index != 0 {
				if err := w.raw(","); err != nil {
					return err
				}
			}
			if err := w.value(child, depth+1); err != nil {
				return err
			}
		}
		return w.raw("]")
	case map[string]any:
		if value == nil {
			return fmt.Errorf("nil Pattern object is not a parsed JSON value")
		}
		// Every member needs at least quoted key, colon and one-byte value.
		// Refuse impossible budgets before allocating a key-sorting slice.
		if len(value) > (w.limit-len(w.data))/4 {
			return fmt.Errorf("canonical Pattern object exceeds its byte budget")
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			if !patternAuthorASCIIKey(key) {
				return fmt.Errorf("canonical Pattern object keys must be ASCII")
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if err := w.raw("{"); err != nil {
			return err
		}
		for index, key := range keys {
			if index != 0 {
				if err := w.raw(","); err != nil {
					return err
				}
			}
			if err := w.text(key); err != nil {
				return err
			}
			if err := w.raw(":"); err != nil {
				return err
			}
			if err := w.value(value[key], depth+1); err != nil {
				return err
			}
		}
		return w.raw("}")
	default:
		return fmt.Errorf("non-AST Pattern canonical value %T", input)
	}
}

func (w *patternAuthorJSONWriter) text(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("canonical Pattern string is not UTF-8")
	}
	if err := w.raw(`"`); err != nil {
		return err
	}
	for _, char := range value {
		var text string
		switch {
		case char == '"' || char == '\\':
			text = "\\" + string(char)
		case char < 32:
			text = fmt.Sprintf("\\u%04x", char)
		default:
			text = string(char)
		}
		if err := w.raw(text); err != nil {
			return err
		}
	}
	return w.raw(`"`)
}

func patternAuthorASCIIKey(value string) bool {
	for _, char := range value {
		if char > 127 {
			return false
		}
	}
	return true
}

func patternAuthorCanonicalNumber(value string) (string, error) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 19 || len(parts[0]) > 1 && parts[0][0] == '0' {
		return "", fmt.Errorf("ordinary nonnegative Pattern numbers required")
	}
	for _, char := range parts[0] {
		if char < '0' || char > '9' {
			return "", fmt.Errorf("ordinary nonnegative Pattern numbers required")
		}
	}
	if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil {
		return "", fmt.Errorf("Pattern number integer part exceeds int64")
	}
	if len(parts) == 1 {
		return value, nil
	}
	if len(parts[1]) < 1 || len(parts[1]) > 6 {
		return "", fmt.Errorf("Pattern decimal permits at most six fractional digits")
	}
	for _, char := range parts[1] {
		if char < '0' || char > '9' {
			return "", fmt.Errorf("ordinary Pattern decimal required")
		}
	}
	fraction := strings.TrimRight(parts[1], "0")
	if fraction == "" {
		return parts[0], nil
	}
	return parts[0] + "." + fraction, nil
}

// encoding/json repairs lone surrogate escapes. Check them before tokenization
// so hashes are never computed over a replacement character instead of input.
func patternAuthorCheckStrings(data []byte) error {
	for index := 0; index < len(data); index++ {
		if data[index] != '"' {
			continue
		}
		end, err := patternAuthorStringEnd(data, index)
		if err != nil {
			return err
		}
		index = end
	}
	return nil
}

func patternAuthorStringEnd(data []byte, start int) (int, error) {
	for index := start + 1; index < len(data); index++ {
		if data[index] == '"' {
			return index, nil
		}
		if data[index] != '\\' {
			continue
		}
		index++
		if index >= len(data) {
			return 0, fmt.Errorf("unterminated Pattern string escape")
		}
		if data[index] != 'u' {
			continue
		}
		value, ok := patternAuthorHexQuad(data, index+1)
		if !ok {
			return 0, fmt.Errorf("invalid Pattern Unicode escape")
		}
		index += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return 0, fmt.Errorf("unpaired Pattern low surrogate")
		}
		if value >= 0xd800 && value <= 0xdbff {
			if index+6 >= len(data) || data[index+1] != '\\' || data[index+2] != 'u' {
				return 0, fmt.Errorf("unpaired Pattern high surrogate")
			}
			low, ok := patternAuthorHexQuad(data, index+3)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return 0, fmt.Errorf("unpaired Pattern high surrogate")
			}
			index += 6
		}
	}
	return 0, fmt.Errorf("unterminated Pattern string")
}

func patternAuthorHexQuad(data []byte, start int) (uint16, bool) {
	if start < 0 || start > len(data)-4 {
		return 0, false
	}
	var value uint16
	for _, char := range data[start : start+4] {
		value <<= 4
		switch {
		case char >= '0' && char <= '9':
			value += uint16(char - '0')
		case char >= 'a' && char <= 'f':
			value += uint16(char - 'a' + 10)
		case char >= 'A' && char <= 'F':
			value += uint16(char - 'A' + 10)
		default:
			return 0, false
		}
	}
	return value, true
}
