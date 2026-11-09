package patternsv2

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

// parse retains numeric lexemes until their source grammar has been checked.
// Canonicalizing first would incorrectly turn an illegal time 1.0 into 1.
func parse(data []byte) (interface{}, error) {
	if len(data) > MaxBytes || !utf8.Valid(data) {
		return nil, invalid("JSON exceeds 1 MiB or is not valid UTF-8")
	}
	if err := checkStrings(data); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	n, err := readNode(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, invalid("one JSON value required; trailing input rejected")
	}
	return n, nil
}

func readNode(d *json.Decoder, depth int) (interface{}, error) {
	if depth > MaxDepth {
		return nil, invalid("JSON nesting exceeds %d", MaxDepth)
	}
	t, err := d.Token()
	if err != nil {
		return nil, invalid("JSON: %v", err)
	}
	switch v := t.(type) {
	case nil:
		return nil, invalid("explicit null is not accepted")
	case string, bool:
		return v, nil
	case json.Number:
		if _, err := canonicalNumber(string(v)); err != nil {
			return nil, err
		}
		return v, nil
	case json.Delim:
		switch v {
		case '{':
			out := make(map[string]interface{})
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, invalid("object key: %v", err)
				}
				k, ok := key.(string)
				if !ok {
					return nil, invalid("object key must be text")
				}
				for _, c := range k {
					if c > 127 {
						return nil, invalid("schema keys must be ASCII")
					}
				}
				if _, ok := out[k]; ok {
					return nil, invalid("duplicate field %q", k)
				}
				value, err := readNode(d, depth+1)
				if err != nil {
					return nil, err
				}
				out[k] = value
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, invalid("invalid object end")
			}
			return out, nil
		case '[':
			out := make([]interface{}, 0)
			for d.More() {
				value, err := readNode(d, depth+1)
				if err != nil {
					return nil, err
				}
				out = append(out, value)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, invalid("invalid array end")
			}
			return out, nil
		}
	}
	return nil, invalid("unsupported JSON token")
}

func object(n interface{}, required []string, optional ...string) (map[string]interface{}, error) {
	m, ok := n.(map[string]interface{})
	if !ok {
		return nil, invalid("object required")
	}
	allowed := make(map[string]bool)
	for _, k := range required {
		allowed[k] = true
		if _, ok := m[k]; !ok {
			return nil, invalid("missing field %q", k)
		}
	}
	for _, k := range optional {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return nil, invalid("unknown field %q", k)
		}
	}
	return m, nil
}

func textField(m map[string]interface{}, key string) (string, error) {
	v, ok := m[key].(string)
	if !ok {
		return "", invalid("%s must be a string", key)
	}
	return v, nil
}

func raw(n interface{}) ([]byte, error) { // preserves numbers until validation
	b, err := json.Marshal(n)
	if err != nil {
		return nil, invalid("JSON: %v", err)
	}
	return b, nil
}

func canonical(n interface{}) ([]byte, error) {
	var b bytes.Buffer
	if err := writeNode(&b, n); err != nil {
		return nil, err
	}
	if b.Len() > MaxBytes {
		return nil, invalid("canonical JSON exceeds 1 MiB")
	}
	return b.Bytes(), nil
}

func canonicalValue(v interface{}) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, invalid("value: %v", err)
	}
	n, err := parse(b)
	if err != nil {
		return nil, err
	}
	return canonical(n)
}

func writeNode(b *bytes.Buffer, n interface{}) error {
	switch v := n.(type) {
	case string:
		writeString(b, v)
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case json.Number:
		x, err := canonicalNumber(string(v))
		if err != nil {
			return err
		}
		b.WriteString(x)
	case []interface{}:
		b.WriteByte('[')
		for i, value := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeNode(b, value); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, key)
			b.WriteByte(':')
			if err := writeNode(b, v[key]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return invalid("non-JSON canonical value")
	}
	return nil
}

func writeString(b *bytes.Buffer, value string) {
	b.WriteByte('"')
	for _, c := range value {
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteRune(c)
		case c < 32:
			fmt.Fprintf(b, "\\u%04x", c)
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
}

func canonicalNumber(value string) (string, error) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts[0]) == 0 || len(parts[0]) > 19 || len(parts[0]) > 1 && parts[0][0] == '0' {
		return "", invalid("ordinary nonnegative numbers required")
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return "", invalid("ordinary nonnegative numbers required")
		}
	}
	if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil {
		return "", invalid("number exceeds int64 range")
	}
	if len(parts) == 1 {
		return value, nil
	}
	if len(parts[1]) < 1 || len(parts[1]) > 6 {
		return "", invalid("decimal permits at most six fractional digits")
	}
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return "", invalid("ordinary decimal required")
		}
	}
	fraction := strings.TrimRight(parts[1], "0")
	if fraction == "" {
		return parts[0], nil
	}
	return parts[0] + "." + fraction, nil
}

// encoding/json replaces lone UTF-16 surrogates. Reject them before tokenizing.
func checkStrings(data []byte) error {
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		end, err := stringEnd(data, i)
		if err != nil {
			return err
		}
		i = end
	}
	return nil
}

func stringEnd(data []byte, start int) (int, error) {
	for i := start + 1; i < len(data); i++ {
		if data[i] == '"' {
			return i, nil
		}
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return 0, invalid("unterminated escape")
		}
		if data[i] != 'u' {
			continue
		}
		value, ok := hexQuad(data, i+1)
		if !ok {
			return 0, invalid("invalid Unicode escape")
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return 0, invalid("unpaired low surrogate")
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return 0, invalid("unpaired high surrogate")
			}
			low, ok := hexQuad(data, i+3)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return 0, invalid("unpaired high surrogate")
			}
			i += 6
		}
	}
	return 0, invalid("unterminated string")
}

func hexQuad(data []byte, start int) (uint16, bool) {
	if start+4 > len(data) {
		return 0, false
	}
	var value uint16
	for _, c := range data[start : start+4] {
		value <<= 4
		switch {
		case c >= '0' && c <= '9':
			value += uint16(c - '0')
		case c >= 'a' && c <= 'f':
			value += uint16(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			value += uint16(c - 'A' + 10)
		default:
			return 0, false
		}
	}
	return value, true
}
