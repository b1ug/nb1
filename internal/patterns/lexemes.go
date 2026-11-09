package patterns

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// strictLexemes supplements jsoninput without changing the rules of existing
// consumers. encoding/json accepts lone UTF-16 surrogates by replacing them,
// and typed int decoding accepts negative zero; neither is permitted here.
func strictLexemes(data []byte) error {
	if !utf8.Valid(data) {
		return invalid("JSON must be valid UTF-8")
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		var err error
		i, err = stringEnd(data, i)
		if err != nil {
			return err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return invalid("JSON lexeme: %v", err)
		}
		if number, ok := token.(json.Number); ok {
			value := number.String()
			for _, c := range value {
				if c < '0' || c > '9' {
					return invalid("numbers must use ordinary nonnegative integer JSON tokens")
				}
			}
		}
	}
}

func stringEnd(data []byte, start int) (int, error) {
	for i := start + 1; i < len(data); i++ {
		switch data[i] {
		case '"':
			return i, nil
		case '\\':
			i++
			if i >= len(data) {
				return 0, invalid("unterminated string escape")
			}
			if data[i] != 'u' {
				continue // encoding/json checks all other escape spellings.
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
	}
	return 0, invalid("unterminated JSON string")
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
