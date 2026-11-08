package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// These optional wire fields mirror control.ReceiptRequest. They are kept in
// this independent module so the MCP transport never imports the USB stack.
type requestTicket struct {
	RequestID                string `json:"request_id"`
	ExpectedDaemonInstanceID string `json:"expected_daemon_instance_id"`
}

type receiptBinding struct {
	serial string
	ticket requestTicket
}

func validateReceiptResponse(method string, status int, headers http.Header, job map[string]any, binding receiptBinding) error {
	instances := headers.Values("X-NB1-Daemon-Instance-ID")
	if len(instances) != 1 || instances[0] != binding.ticket.ExpectedDaemonInstanceID || job["device"] != binding.serial {
		return errors.New("daemon did not confirm the original instance and device binding; original outcome unknown")
	}
	if method == http.MethodPost {
		replayed := headers.Values("X-NB1-Replayed")
		if status != http.StatusAccepted || len(replayed) != 1 || (replayed[0] != "true" && replayed[0] != "false") {
			return errors.New("daemon did not confirm receipt submission/replay with HTTP202; original outcome unknown")
		}
	} else if status != http.StatusOK {
		return errors.New("daemon did not confirm receipt lookup with HTTP200; original outcome unknown")
	}
	return nil
}

func (d *daemon) requestJob(ctx context.Context, method, path string, input any, serial string, ticket *requestTicket) output {
	if ticket == nil {
		return d.request(ctx, method, path, "job", input)
	}
	out := d.requestBound(ctx, method, path, "job", input, &receiptBinding{serial: serial, ticket: *ticket})
	out = preserveTicket(out, ticket)
	if method == http.MethodGet && out.Error != nil {
		// Even a definite rejection of this lookup cannot establish whether
		// the original intent ran. This also covers an older/replaced daemon
		// with no receipt route (generic 404/501), without changing {id} reads.
		out.Error.OutcomeUnknown = true
	}
	return out
}

type instanceInfo struct {
	DaemonInstanceID string `json:"daemon_instance_id"`
	Idempotency      struct {
		Supported           bool   `json:"supported"`
		RequestIDPrefix     string `json:"request_id_prefix"`
		AcceptUntilUptimeMS int64  `json:"accept_until_uptime_ms"`
	} `json:"idempotency"`
}

func validLowerHexID(id, prefix string) bool {
	if len(id) != len(prefix)+32 || !strings.HasPrefix(id, prefix) {
		return false
	}
	for _, c := range id[len(prefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// This checks syntax only. Admission deadlines use the daemon's monotonic
// uptime; the client cannot validate expiry or instance identity offline.
func validateTicket(t requestTicket) error {
	if !validLowerHexID(t.ExpectedDaemonInstanceID, "daemon_") {
		return errors.New("expected_daemon_instance_id must be daemon_ plus 32 lowercase hexadecimal characters")
	}
	parts := strings.Split(t.RequestID, "_")
	if len(parts) != 3 || parts[0] != "req" || !validLowerHexID(parts[2], "") || len(parts[1]) == 0 || len(parts[1]) > 19 || parts[1][0] == '0' {
		return errors.New("request_id must be req_<positive uptime deadline>_<32 lowercase hexadecimal characters>")
	}
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return errors.New("invalid request deadline")
		}
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return errors.New("request deadline overflow")
	}
	return nil
}

func optionalTicket(requestID, instanceID string) (*requestTicket, error) {
	if requestID == "" && instanceID == "" {
		return nil, nil
	}
	t := &requestTicket{RequestID: requestID, ExpectedDaemonInstanceID: instanceID}
	if err := validateTicket(*t); err != nil {
		return nil, err
	}
	return t, nil
}

func ticketFromInstance(info instanceInfo, random io.Reader) (*requestTicket, error) {
	if !info.Idempotency.Supported {
		return nil, errors.New("daemon does not advertise idempotency support")
	}
	// Validate the server prefix before drawing randomness; neither preparation
	// nor retries reserve a receipt or admit a job.
	expectedPrefix := fmt.Sprintf("req_%d_", info.Idempotency.AcceptUntilUptimeMS)
	if info.Idempotency.AcceptUntilUptimeMS <= 0 || info.Idempotency.RequestIDPrefix != expectedPrefix {
		return nil, errors.New("invalid daemon request_id_prefix or admission deadline")
	}
	t := &requestTicket{RequestID: expectedPrefix + strings.Repeat("0", 32), ExpectedDaemonInstanceID: info.DaemonInstanceID}
	if err := validateTicket(*t); err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := io.ReadFull(random, nonce[:]); err != nil {
		return nil, fmt.Errorf("create request nonce: %w", err)
	}
	t.RequestID = expectedPrefix + hex.EncodeToString(nonce[:])
	return t, nil
}

func listDevices(ctx context.Context, d *daemon, prepare bool) output {
	out := d.request(ctx, http.MethodGet, "/v1/devices", "devices", nil)
	if out.Error != nil || !prepare {
		return out
	}
	instance := d.request(ctx, http.MethodGet, "/v1/instance", "instance", nil)
	if instance.Error != nil {
		return instance
	}
	// request has already checked that the response is an object. Extract only
	// the optional extension fields; newer daemon metadata remains compatible.
	data := instance.Data.(map[string]any)
	info := instanceInfo{}
	info.DaemonInstanceID, _ = data["daemon_instance_id"].(string)
	meta, ok := data["idempotency"].(map[string]any)
	if !ok {
		return errorOutput("invalid_daemon_response", "missing idempotency metadata", 200, false)
	}
	info.Idempotency.Supported, _ = meta["supported"].(bool)
	if !info.Idempotency.Supported {
		return errorOutput("idempotency_unsupported", "daemon does not advertise idempotency support; no job was submitted", 200, false)
	}
	info.Idempotency.RequestIDPrefix, _ = meta["request_id_prefix"].(string)
	deadline, ok := meta["accept_until_uptime_ms"].(json.Number)
	if ok {
		info.Idempotency.AcceptUntilUptimeMS, _ = deadline.Int64()
	}
	ticket, err := ticketFromInstance(info, rand.Reader)
	if err != nil {
		return errorOutput("invalid_daemon_response", err.Error(), 200, false)
	}
	out.RequestTicket = ticket
	return out
}

func getJob(ctx context.Context, d *daemon, args getJobArgs) output {
	if args.ID != "" {
		if !validToken(args.ID) || args.Serial != "" || args.RequestID != "" || args.ExpectedDaemonInstanceID != "" {
			return errorOutput("invalid_request", "provide either id or serial plus the complete request ticket", 0, false)
		}
		return d.request(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(args.ID), "job", nil)
	}
	if !validToken(args.Serial) {
		return errorOutput("invalid_request", "receipt lookup requires serial and the complete request ticket", 0, false)
	}
	ticket, err := optionalTicket(args.RequestID, args.ExpectedDaemonInstanceID)
	if err != nil || ticket == nil {
		return errorOutput("invalid_request", "receipt lookup requires a valid complete request ticket", 0, false)
	}
	query := url.Values{"expected_daemon_instance_id": []string{ticket.ExpectedDaemonInstanceID}}
	return d.requestJob(ctx, http.MethodGet, "/v1/devices/"+url.PathEscape(args.Serial)+"/requests/"+url.PathEscape(ticket.RequestID)+"?"+query.Encode(), nil, args.Serial, ticket)
}

func preserveTicket(out output, ticket *requestTicket) output {
	out.RequestTicket = ticket
	if ticket != nil && out.Error != nil {
		failure := out.Error
		// A failed read cannot settle the original write's outcome. Definite
		// structured rejections (such as idempotency_conflict) remain distinct
		// from transport/server failures and missing/expired receipt evidence.
		if failure.HTTPStatus >= 500 || failure.HTTPStatus == http.StatusGone || failure.Code == "daemon_unreachable" || failure.Code == "invalid_daemon_response" || failure.Code == "http_error" || failure.Code == "daemon_instance_changed" || failure.Code == "request_expired" || failure.Code == "request_not_found" {
			failure.OutcomeUnknown = true
		}
	}
	return out
}

func requestIDSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 38, "maxLength": 56, "pattern": "^req_[1-9][0-9]{0,18}_[0-9a-f]{32}$", "description": "Use the prepared request_id unchanged; deadlines are daemon uptime, not wall time"}
}

func instanceIDSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^daemon_[0-9a-f]{32}$", "description": "Original daemon instance binding; never replace it automatically after a restart"}
}

func submissionSchema(properties map[string]any, required ...string) map[string]any {
	properties["request_id"] = requestIDSchema()
	properties["expected_daemon_instance_id"] = instanceIDSchema()
	schema := objectSchema(properties, required...)
	schema["dependentRequired"] = map[string]any{"request_id": []string{"expected_daemon_instance_id"}, "expected_daemon_instance_id": []string{"request_id"}}
	return schema
}

func getJobSchema() map[string]any {
	id := map[string]any{"id": tokenSchema()}
	receipt := map[string]any{"serial": tokenSchema(), "request_id": requestIDSchema(), "expected_daemon_instance_id": instanceIDSchema()}
	schema := objectSchema(map[string]any{"id": tokenSchema(), "serial": tokenSchema(), "request_id": requestIDSchema(), "expected_daemon_instance_id": instanceIDSchema()})
	schema["oneOf"] = []any{objectSchema(id, "id"), objectSchema(receipt, "serial", "request_id", "expected_daemon_instance_id")}
	return schema
}
