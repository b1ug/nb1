package daemonclient

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

// InspectionError means a valid collection envelope arrived but the requested
// native observation is incomplete. Result retains partial data and errors;
// this is not a malformed response or an API admission rejection.
type InspectionError struct {
	Result nativeinspection.Result
}

func (e *InspectionError) Error() string {
	return fmt.Sprintf("native inspection %s; partial observations are retained", e.Result.Status)
}

// InspectNative performs one daemon GET, never preparation, retry, discovery or
// direct USB fallback. A request timeout does not forcibly cancel native I/O.
func (c *Client) InspectNative(ctx context.Context, serial string, request nativeinspection.Request) (nativeinspection.Result, error) {
	request = request.Clone()
	if len(serial) != 8 {
		return nativeinspection.Result{}, fmt.Errorf("native inspection requires an 8-digit hexadecimal device serial")
	}
	if _, err := hex.DecodeString(serial); err != nil {
		return nativeinspection.Result{}, fmt.Errorf("native inspection requires an 8-digit hexadecimal device serial")
	}
	if err := request.Validate(); err != nil {
		return nativeinspection.Result{}, err
	}
	path := "/v1/devices/" + url.PathEscape(serial) + "/native-sequence/" + request.Kind
	if request.Range != nil {
		query := url.Values{}
		query.Set("start_line", fmt.Sprint(request.Range.StartLine))
		query.Set("line_count", fmt.Sprint(request.Range.LineCount))
		path += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return nativeinspection.Result{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Close = true
	// Fresh transport state prevents net/http from implicitly replaying a GET
	// after a failure on a reused idle connection. Do not mutate other requests
	// using this Client or change process-wide proxy/redirect configuration.
	oneShot := *c.http
	oneShot.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	if transport, ok := c.http.Transport.(*http.Transport); ok {
		copy := transport.Clone()
		copy.Proxy, copy.DisableKeepAlives = nil, true
		oneShot.Transport = copy
		defer copy.CloseIdleConnections()
	}
	response, err := oneShot.Do(req)
	if err != nil {
		return nativeinspection.Result{}, fmt.Errorf("contact daemon: %w", err)
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, nativeinspection.MaxResponseBytes+1))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure httpapi.ErrorResponse
		if readErr != nil || len(data) > nativeinspection.MaxResponseBytes || json.Unmarshal(data, &failure) != nil || failure.Error.Code == "" {
			return nativeinspection.Result{}, &Error{Status: response.StatusCode, Code: "http_error", Message: response.Status}
		}
		return nativeinspection.Result{}, &Error{Status: response.StatusCode, Code: failure.Error.Code, Message: failure.Error.Message}
	}
	malformed := func(err error) (nativeinspection.Result, error) {
		return nativeinspection.Result{}, &DecodeError{Status: response.StatusCode, Err: err}
	}
	if response.StatusCode != http.StatusOK {
		return nativeinspection.Result{}, &Error{Status: response.StatusCode, Code: "unexpected_status", Message: "native inspection requires HTTP 200"}
	}
	media := response.Header.Values("Content-Type")
	if len(media) != 1 {
		return malformed(fmt.Errorf("native inspection requires one JSON Content-Type"))
	}
	kind, _, err := mime.ParseMediaType(media[0])
	if err != nil || kind != "application/json" {
		return malformed(fmt.Errorf("native inspection requires application/json"))
	}
	if readErr != nil {
		return malformed(fmt.Errorf("read native inspection response: %w", readErr))
	}
	if !lightResponseUnicode(data) {
		return malformed(fmt.Errorf("native inspection response contains invalid Unicode"))
	}
	value, err := nativeinspection.DecodeResult(data, serial, request)
	if err != nil {
		return malformed(err)
	}
	instance := response.Header.Values("X-NB1-Daemon-Instance-ID")
	if len(instance) != 1 || instance[0] != value.DaemonInstanceID || len(response.Header.Values("Location")) != 0 || len(response.Header.Values("X-NB1-Replayed")) != 0 {
		return malformed(fmt.Errorf("native inspection response did not confirm its instance or contains Job metadata"))
	}
	if !value.Complete {
		return value, &InspectionError{Result: value.Clone()}
	}
	return value, nil
}
