package nativeinspection

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/jsoninput"
)

// DecodeResult accepts the complete nullable read-side shape. Existing request
// decoders intentionally forbid null and must not be reused for observations.
// Every field is explicit; omission is never filled with a plausible zero.
func DecodeResult(data []byte, serial string, request Request) (Result, error) {
	if len(data) == 0 || len(data) > MaxResponseBytes || !utf8.Valid(data) || !jsoninput.ValidUnicode(data) {
		return Result{}, invalidResult("response bytes")
	}
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if err := checkValue(check, reflect.TypeOf(Result{}), 0); err != nil {
		return Result{}, err
	}
	if _, err := check.Token(); err != io.EOF {
		return Result{}, invalidResult("trailing data")
	}
	var out Result
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return Result{}, fmt.Errorf("decode native inspection response: %w", err)
	}
	if err := ValidateResult(out, serial, request); err != nil {
		return Result{}, err
	}
	return out, nil
}

func checkValue(decoder *json.Decoder, kind reflect.Type, depth int) error {
	if depth > 32 {
		return invalidResult("nesting limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		if kind.Kind() != reflect.Ptr {
			return invalidResult("null non-nullable value")
		}
		return nil
	}
	for kind.Kind() == reflect.Ptr {
		kind = kind.Elem()
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delim {
	case '{':
		if kind.Kind() != reflect.Struct {
			return invalidResult("object type")
		}
		fields := map[string]reflect.Type{}
		for index := 0; index < kind.NumField(); index++ {
			field := kind.Field(index)
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if field.PkgPath == "" && tag != "-" {
				if tag == "" {
					tag = field.Name
				}
				fields[tag] = field.Type
			}
		}
		seen := map[string]bool{}
		for decoder.More() {
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := value.(string)
			if !ok || seen[key] {
				return invalidResult("duplicate object key")
			}
			field, known := fields[key]
			if !known {
				return invalidResult("unknown or noncanonical field: " + key)
			}
			seen[key] = true
			if err := checkValue(decoder, field, depth+1); err != nil {
				return err
			}
		}
		if len(seen) != len(fields) {
			return invalidResult("missing explicit field")
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return invalidResult("object end")
		}
		return nil
	case '[':
		if kind.Kind() != reflect.Array && kind.Kind() != reflect.Slice {
			return invalidResult("array type")
		}
		length := 0
		for decoder.More() {
			if err := checkValue(decoder, kind.Elem(), depth+1); err != nil {
				return err
			}
			length++
		}
		if kind.Kind() == reflect.Array && length != kind.Len() {
			return invalidResult("fixed array length")
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return invalidResult("array end")
		}
		return nil
	default:
		return invalidResult("delimiter")
	}
}
