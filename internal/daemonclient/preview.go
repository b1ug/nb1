package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/jsoninput"
)

// Capabilities returns the daemon's software compiler catalog, not hardware
// discovery. An older daemon is explicitly unsupported; no fallback is made.
func (c *Client) Capabilities(ctx context.Context) (control.CapabilityCatalog, error) {
	var result control.CapabilityCatalog
	if _, err := c.doResponseExpected(ctx, http.MethodGet, "/v1/capabilities", nil, &result, http.StatusOK); err != nil {
		return control.CapabilityCatalog{}, compilerRouteError(err, "capabilities")
	}
	if err := control.ValidateCapabilityCatalog(result); err != nil {
		return control.CapabilityCatalog{}, &DecodeError{Status: http.StatusOK, Err: err}
	}
	return result, nil
}

// Preview invokes the daemon's authoritative compiler exactly once. It never
// submits a Job, prepares a receipt, retries a POST or opens a device.
func (c *Client) Preview(ctx context.Context, input httpapi.PreviewRequest) (control.PreviewResult, error) {
	if (input.Program == nil) == (input.Effect == nil) {
		return control.PreviewResult{}, fmt.Errorf("preview requires exactly one program or effect")
	}
	if input.Serial != nil && !control.ValidDeviceSerial(*input.Serial) {
		return control.PreviewResult{}, fmt.Errorf("preview serial is invalid")
	}
	kind := "program"
	if input.Effect != nil {
		kind = "effect"
	}
	var result control.PreviewResult
	if _, err := c.doResponseExpected(ctx, http.MethodPost, "/v1/programs/preview", input, &result, http.StatusOK); err != nil {
		return control.PreviewResult{}, compilerRouteError(err, "preview")
	}
	if err := control.ValidatePreviewResult(result, kind, input.Serial); err != nil {
		return control.PreviewResult{}, &DecodeError{Status: http.StatusOK, Err: err}
	}
	return result, nil
}

func compilerRouteError(err error, route string) error {
	var failure *Error
	if errors.As(err, &failure) && (failure.Status == http.StatusNotFound || failure.Status == http.StatusNotImplemented) {
		return &Error{Status: failure.Status, Code: route + "_unsupported", Message: "daemon does not support " + route + "; no direct fallback"}
	}
	return err
}

func decodeCompilerResponse(response *http.Response, output interface{}) error {
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return fmt.Errorf("compiler response requires application/json")
	}
	const maxResponseBytes = 2 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("compiler response exceeds 2 MiB")
	}
	if err := jsoninput.DecodeObject(data, output); err != nil {
		return err
	}
	// Comparing decoded JSON with the typed round trip requires zero/false
	// fields and canonical omission rules without duplicating the wire schema.
	canonical, err := json.Marshal(output)
	if err != nil {
		return err
	}
	decode := func(body []byte) (interface{}, error) {
		var value interface{}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		err := decoder.Decode(&value)
		return value, err
	}
	actual, err := decode(data)
	if err != nil {
		return err
	}
	expected, err := decode(canonical)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("compiler response has missing or noncanonical fields")
	}
	return nil
}
