// Package jsoninput validates canonical, unambiguous JSON request objects.
package jsoninput

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// DecodeObject rejects null, duplicate keys, noncanonical field names, excessive
// nesting, unknown fields and trailing data. Limits on total bytes belong to the
// transport. Reflection follows the actual Go request type's JSON field tags.
func DecodeObject(data []byte, out interface{}) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("one JSON object is required")
	}
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Ptr || reflect.ValueOf(out).IsNil() {
		return fmt.Errorf("JSON destination must be a non-nil pointer")
	}
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if err := value(check, t.Elem(), 0); err != nil {
		return err
	}
	if _, err := check.Token(); err != io.EOF {
		return fmt.Errorf("one JSON object is required; trailing data is not allowed")
	}
	decode := json.NewDecoder(bytes.NewReader(data))
	decode.DisallowUnknownFields()
	return decode.Decode(out)
}

func value(dec *json.Decoder, t reflect.Type, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds 64 levels")
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return fmt.Errorf("explicit null is not accepted; omit optional fields")
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delim {
	case '{':
		fields := make(map[string]reflect.Type)
		if t.Kind() == reflect.Struct {
			for i := 0; i < t.NumField(); i++ {
				field := t.Field(i)
				if field.PkgPath != "" {
					continue
				}
				tag := strings.Split(field.Tag.Get("json"), ",")[0]
				if tag == "-" {
					continue
				}
				if tag == "" {
					tag = field.Name
				}
				fields[tag] = field.Type
			}
		} else if t.Kind() != reflect.Map {
			return fmt.Errorf("unexpected JSON object")
		}
		seen := make(map[string]bool)
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key must be a string")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = true
			field, known := fields[key]
			if t.Kind() == reflect.Map {
				field = t.Elem()
				known = true
			}
			if !known {
				return fmt.Errorf("unknown or noncanonical JSON field %q", key)
			}
			if err := value(dec, field, depth+1); err != nil {
				return fmt.Errorf("field %s: %w", key, err)
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
			return fmt.Errorf("unexpected JSON array")
		}
		for dec.More() {
			if err := value(dec, t.Elem(), depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
}
