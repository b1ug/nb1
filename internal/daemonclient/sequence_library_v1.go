package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/actionv1"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/jsoninput"
	"github.com/b1ug/nb1/internal/sequence"
)

type SequenceColor struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type SequencePattern struct {
	ID       string            `json:"id"`
	Title    string            `json:"title,omitempty"`
	Sequence sequence.Sequence `json:"sequence"`
}

type SequencePatternSummary struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// ParseSequenceColorInput accepts a CLI color string or a JSON ColorInput.
// Color references are retained for the daemon to resolve when it saves.
func ParseSequenceColorInput(input string) (json.RawMessage, error) {
	text := strings.TrimSpace(input)
	raw := json.RawMessage(text)
	if len(raw) == 0 || raw[0] != '{' && raw[0] != '"' {
		var err error
		raw, err = json.Marshal(text)
		if err != nil {
			return nil, err
		}
	}
	if _, err := actionv1.ResolveColor(raw, func(string) (string, error) { return "#000000", nil }); err != nil {
		return nil, err
	}
	return raw, nil
}

func ValidSequenceColorName(name string) bool {
	raw, _ := json.Marshal("@" + name)
	_, err := actionv1.ResolveColor(raw, func(string) (string, error) { return "#000000", nil })
	return err == nil
}

func (c *Client) SequenceColors(ctx context.Context) ([]SequenceColor, error) {
	var result struct {
		Items []SequenceColor `json:"items"`
	}
	if err := c.sequenceV1ListRequest(ctx, "/v1/colors", &result); err != nil {
		return nil, err
	}
	for i, color := range result.Items {
		if err := validateSequenceColor(color, color.Name); err != nil {
			return nil, err
		}
		if i > 0 && result.Items[i-1].Name >= color.Name {
			return nil, &DecodeError{Status: http.StatusOK, Err: fmt.Errorf("color list must contain unique names in order")}
		}
	}
	return result.Items, nil
}

func (c *Client) SaveSequenceColor(ctx context.Context, name string, color json.RawMessage) (SequenceColor, error) {
	if !ValidSequenceColorName(name) {
		return SequenceColor{}, fmt.Errorf("color name must start with a lowercase letter and contain 1 to 32 lowercase letters, digits or hyphens")
	}
	if _, err := actionv1.ResolveColor(color, func(string) (string, error) { return "#000000", nil }); err != nil {
		return SequenceColor{}, err
	}
	request := struct {
		Color json.RawMessage `json:"color"`
	}{Color: color}
	var result SequenceColor
	if err := c.sequenceV1Request(ctx, http.MethodPut, "/v1/colors/"+url.PathEscape(name), request, &result, http.StatusOK); err != nil {
		return result, err
	}
	return result, validateSequenceColor(result, name)
}

func validateSequenceColor(color SequenceColor, name string) error {
	if color.Name != name || !ValidSequenceColorName(color.Name) || len(color.Color) != 7 || color.Color[0] != '#' {
		return &DecodeError{Status: http.StatusOK, Err: fmt.Errorf("invalid saved color identity or Hex6 value")}
	}
	raw, _ := json.Marshal(color.Color)
	resolved, err := actionv1.ResolveColor(raw, nil)
	if err != nil || color.Color != resolved {
		return &DecodeError{Status: http.StatusOK, Err: fmt.Errorf("saved color must be canonical lowercase Hex6")}
	}
	return nil
}

func (c *Client) SequencePatterns(ctx context.Context) ([]SequencePatternSummary, error) {
	var result struct {
		Items []SequencePatternSummary `json:"items"`
	}
	if err := c.sequenceV1ListRequest(ctx, "/v1/patterns", &result); err != nil {
		return nil, err
	}
	for i, pattern := range result.Items {
		if !control.ValidSequenceJobID(pattern.ID) {
			return nil, &DecodeError{Status: http.StatusOK, Err: fmt.Errorf("invalid saved pattern ID")}
		}
		if i > 0 && result.Items[i-1].ID >= pattern.ID {
			return nil, &DecodeError{Status: http.StatusOK, Err: fmt.Errorf("pattern list must contain unique IDs in order")}
		}
	}
	return result.Items, nil
}

func (c *Client) SequencePattern(ctx context.Context, id string) (SequencePattern, error) {
	if !control.ValidSequenceJobID(id) {
		return SequencePattern{}, fmt.Errorf("invalid pattern ID")
	}
	var result SequencePattern
	if err := c.sequenceV1Request(ctx, http.MethodGet, "/v1/patterns/"+url.PathEscape(id), nil, &result, http.StatusOK); err != nil {
		return result, err
	}
	return result, validateSequencePattern(result, id)
}

func (c *Client) SaveSequencePattern(ctx context.Context, id string, input SequenceInput, title string) (SequencePattern, error) {
	if !control.ValidSequenceJobID(id) {
		return SequencePattern{}, fmt.Errorf("invalid pattern ID")
	}
	if !utf8.ValidString(title) {
		return SequencePattern{}, fmt.Errorf("pattern title must contain valid Unicode")
	}
	if utf8.RuneCountInString(title) > 256 {
		return SequencePattern{}, fmt.Errorf("pattern title cannot exceed 256 characters")
	}
	if input.PatternID != nil {
		return SequencePattern{}, fmt.Errorf("saving a pattern requires an action or sequence")
	}
	if err := input.validate(); err != nil {
		return SequencePattern{}, err
	}
	request := struct {
		Title    string          `json:"title,omitempty"`
		Action   json.RawMessage `json:"action,omitempty"`
		Sequence json.RawMessage `json:"sequence,omitempty"`
	}{Title: title, Action: input.Action, Sequence: input.Sequence}
	var result SequencePattern
	if err := c.sequenceV1Request(ctx, http.MethodPut, "/v1/patterns/"+url.PathEscape(id), request, &result, http.StatusOK); err != nil {
		return result, err
	}
	if result.Title != title {
		return SequencePattern{}, &DecodeError{Status: http.StatusOK, Err: fmt.Errorf("daemon changed the saved pattern title")}
	}
	return result, validateSequencePattern(result, id)
}

func validateSequencePattern(pattern SequencePattern, id string) error {
	normalized, err := sequence.Normalize(pattern.Sequence)
	if pattern.ID != id || !control.ValidSequenceJobID(pattern.ID) || err != nil || !reflect.DeepEqual(pattern.Sequence, normalized) {
		return &DecodeError{Status: http.StatusOK, Err: fmt.Errorf("invalid saved pattern identity or normalized Sequence")}
	}
	return nil
}

func (c *Client) sequenceV1ListRequest(ctx context.Context, path string, output interface{}) error {
	return c.sequenceV1Response(ctx, http.MethodGet, path, nil, func(response *http.Response) error {
		data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		if err != nil {
			return err
		}
		if len(data) > 2<<20 || !jsoninput.ValidUnicode(data) {
			return fmt.Errorf("list response must contain valid Unicode and be at most 2 MiB")
		}
		// The object decoder checks every array element against the typed list
		// schema, including duplicate keys and null values.
		wrapped := append([]byte(`{"items":`), data...)
		wrapped = append(wrapped, '}')
		copy := *response
		copy.Body = io.NopCloser(bytes.NewReader(wrapped))
		return decodeCompilerResponse(&copy, output)
	}, http.StatusOK)
}
