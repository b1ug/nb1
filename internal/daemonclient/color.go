package daemonclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"

	"github.com/b1ug/nb1/internal/colorinput"
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
