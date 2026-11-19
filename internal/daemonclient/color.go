package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/b1ug/nb1/internal/colorinput"
	"github.com/b1ug/nb1/internal/jsoninput"
)

// ResolveColor computes a color through the daemon exactly once. Input and
// response use the shared software contract; no device or execution API is used.
func (c *Client) ResolveColor(ctx context.Context, color json.RawMessage) (colorinput.Result, error) {
	if err := ctx.Err(); err != nil {
		return colorinput.Result{}, err
	}
	expected, err := colorinput.ResolveJSON(color)
	if err != nil {
		return colorinput.Result{}, err
	}
	input := struct {
		Color json.RawMessage `json:"color"`
	}{Color: color}
	// The HTTP limit includes the envelope. Marshal the same shape that do uses
	// and validate it before making contact, including its total byte count.
	body, err := json.Marshal(input)
	if err != nil {
		return colorinput.Result{}, err
	}
	if _, err := colorinput.ResolveRequestJSON(body); err != nil {
		return colorinput.Result{}, err
	}
	var result colorinput.Result
	if _, err := c.doResponseExpected(ctx, http.MethodPost, "/v1/colors/resolve", input, &result, http.StatusOK); err != nil {
		return colorinput.Result{}, compilerRouteError(err, "color_resolution")
	}
	if err := colorinput.ValidateResult(result); err != nil {
		return colorinput.Result{}, &DecodeError{Status: http.StatusOK, Err: err}
	}
	if !reflect.DeepEqual(result, expected) {
		return colorinput.Result{}, &DecodeError{Status: http.StatusOK, Err: fmt.Errorf("daemon color resolution differs from the requested color contract")}
	}
	return result, nil
}

type ResolvedSequenceColor struct {
	Color string `json:"color"`
}

// ResolveSequenceColor resolves a color input, including names from the local
// Sequence library, through the current v1 color endpoint.
func (c *Client) ResolveSequenceColor(ctx context.Context, raw json.RawMessage, timeout time.Duration) (ResolvedSequenceColor, error) {
	body, err := json.Marshal(struct {
		Color json.RawMessage `json:"color"`
	}{Color: raw})
	if err != nil {
		return ResolvedSequenceColor{}, err
	}
	request, err := c.newRequest(ctx, http.MethodPost, "/v1/colors/resolve", bytes.NewReader(body))
	if err != nil {
		return ResolvedSequenceColor{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.GetBody = nil
	httpClient := *c.http
	httpClient.Timeout = timeout
	response, err := httpClient.Do(request)
	if err != nil {
		return ResolvedSequenceColor{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return ResolvedSequenceColor{}, err
	}
	media, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if len(data) > 2<<20 || !jsoninput.ValidUnicode(data) || mediaErr != nil || media != "application/json" {
		return ResolvedSequenceColor{}, &DecodeError{Status: response.StatusCode, Err: fmt.Errorf("invalid color response")}
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Field   string `json:"field,omitempty"`
			} `json:"error"`
		}
		if err := jsoninput.DecodeObject(data, &failure); err != nil || failure.Error.Code == "" {
			return ResolvedSequenceColor{}, &DecodeError{Status: response.StatusCode, Err: fmt.Errorf("invalid color error response")}
		}
		return ResolvedSequenceColor{}, &Error{Status: response.StatusCode, Code: failure.Error.Code, Message: failure.Error.Message}
	}
	var result ResolvedSequenceColor
	if err := jsoninput.DecodeObject(data, &result); err != nil || len(result.Color) != 7 || result.Color[0] != '#' || strings.ToLower(result.Color) != result.Color {
		return ResolvedSequenceColor{}, &DecodeError{Status: response.StatusCode, Err: fmt.Errorf("invalid resolved color")}
	}
	for _, value := range result.Color[1:] {
		if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f') {
			return ResolvedSequenceColor{}, &DecodeError{Status: response.StatusCode, Err: fmt.Errorf("invalid resolved color")}
		}
	}
	return result, nil
}
