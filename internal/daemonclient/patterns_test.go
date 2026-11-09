package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patterns"
	"github.com/b1ug/nb1/internal/patternstore"
)

var patternTicket = control.ReceiptRequest{RequestID: "req_60000_0123456789abcdef0123456789abcdef", ExpectedDaemonInstanceID: "daemon_0123456789abcdef0123456789abcdef"}

func patternFixture(t *testing.T) (patterns.Record, []byte) {
	t.Helper()
	p := control.Program{Steps: []control.Step{{Color: "#123456"}}, Repeat: 2048, OnFinish: "hold"}
	r, err := patterns.NewRecord("saved", "Saved", patterns.Source{Program: &p})
	if err != nil {
		t.Fatal(err)
	}
	b, err := patterns.EncodeSnapshot([]patterns.Record{r})
	if err != nil {
		t.Fatal(err)
	}
	return r, b
}
func patternPreviewFixture(t *testing.T, r patterns.Record, serial *string) httpapi.PatternPreview {
	t.Helper()
	p, err := control.Preview(&r.CompiledProgram, nil)
	if err != nil {
		t.Fatal(err)
	}
	if serial != nil {
		p = control.WithDeviceSnapshot(p, *serial, &control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: *serial, Capabilities: []string{"set", "program", "raw-rgb", "hold"}}, Connection: "online"})
	}
	return httpapi.PatternPreview{PatternRef: httpapi.PatternRef{ID: r.ID, Revision: r.Revision}, Preview: p}
}
func patternJobFixture(r patterns.Record) control.Job {
	return control.Job{ID: "job-pattern", Device: "demo", Status: "queued", Program: r.CompiledProgram, TotalSteps: 2048, CreatedAt: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)}
}
func patternReceiptHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-NB1-Daemon-Instance-ID", patternTicket.ExpectedDaemonInstanceID)
	w.Header().Set("X-NB1-Replayed", "false")
	w.Header().Set("Location", "/v1/devices/demo/requests/"+patternTicket.RequestID+"?expected_daemon_instance_id="+patternTicket.ExpectedDaemonInstanceID)
}

func TestPatternClientLibraryRoundTripPreservesRawBytesAndLargeStepTotals(t *testing.T) {
	r, data := patternFixture(t)
	s, err := patternstore.New(filepath.Join(t.TempDir(), "library"))
	if err != nil {
		t.Fatal(err)
	}
	var imports int32
	h := httpapi.New(nil, httpapi.WithPatternStore(s))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1/patterns/import" {
			atomic.AddInt32(&imports, 1)
			body, _ := io.ReadAll(req.Body)
			if !bytes.Equal(body, data) {
				t.Error("import bytes changed")
			}
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
		h.ServeHTTP(w, req)
	}))
	defer server.Close()
	c, _ := New(server.URL)
	defer c.Close()
	ctx := context.Background()
	result, err := c.ImportPatterns(ctx, data)
	if err != nil || len(result.Created) != 1 || result.Total != 1 {
		t.Fatal(result, err)
	}
	list, err := c.ListPatterns(ctx)
	if err != nil || len(list.Patterns) != 1 || list.Patterns[0].TotalSteps != 2048 {
		t.Fatal(list, err)
	}
	got, err := c.GetPattern(ctx, r.ID)
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatal(got, err)
	}
	for _, id := range []string{"", r.ID} {
		out, err := c.ExportPatterns(ctx, id)
		if err != nil || !bytes.Equal(out, data) {
			t.Fatal(string(out), err)
		}
	}
	if atomic.LoadInt32(&imports) != 1 {
		t.Fatal(atomic.LoadInt32(&imports))
	}
}

func TestPatternClientRejectsInvalidInputsBeforeHTTP(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(500) }))
	defer server.Close()
	c, _ := New(server.URL)
	defer c.Close()
	ctx := context.Background()
	for _, data := range []string{`{"schema_version":1e0,"patterns":[]}`, `{"schema_version":1,"patterns":null}`, `{"schema_version":1,"patterns":[],"path":"/tmp/other"}`} {
		if _, err := c.ImportPatterns(ctx, []byte(data)); err == nil {
			t.Fatal("invalid package accepted")
		}
	}
	for _, id := range []string{"", "../file", "_bad"} {
		if _, err := c.GetPattern(ctx, id); err == nil {
			t.Fatal("invalid id accepted")
		}
	}
	if _, err := c.PreviewPattern(ctx, httpapi.PatternRef{ID: "saved", Revision: "latest"}, nil); err == nil {
		t.Fatal("unfixed reference accepted")
	}
	if _, err := c.SubmitPatternWithReceipt(ctx, "demo", httpapi.PatternRef{ID: "saved", Revision: "sha256:" + strings.Repeat("0", 64)}, control.ReceiptRequest{}); err == nil {
		t.Fatal("missing ticket accepted")
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal(atomic.LoadInt32(&calls))
	}
}

func TestPatternListTreatsCrossPlatformAbsolutePathsOnlyAsDisplayMetadata(t *testing.T) {
	for _, path := range []string{filepath.Join(t.TempDir(), "not-opened"), `C:\Users\test\AppData\Roaming\nb1\patterns`, `\\server\share\patterns`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(httpapi.PatternList{SchemaVersion: 1, Contract: httpapi.PatternLibraryContract, Library: httpapi.PatternLibraryInfo{Path: path, State: "ready", CreateOnly: true, MaxRecords: 128, MaxPackageBytes: 1 << 20, RecordContract: patterns.RevisionDomain, CompileContract: patterns.CompileContract, SnapshotKind: patterns.SnapshotKind}, Patterns: []httpapi.PatternSummary{}})
		}))
		c, _ := New(server.URL)
		got, err := c.ListPatterns(context.Background())
		c.Close()
		server.Close()
		if err != nil || got.Library.Path != path {
			t.Fatal(got, err)
		}
	}
}

func TestPatternClientRejectsCorruptListRecordSnapshotAndMedia(t *testing.T) {
	r, data := patternFixture(t)
	for _, mode := range []string{"list-contract", "list-order", "list-bounds", "record-revision", "record-noncanonical", "snapshot-whitespace", "snapshot-authoring", "snapshot-other-id", "duplicate-media", "duplicate-key"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "list-contract", "list-order", "list-bounds":
					p := patternPreviewFixture(t, r, nil).Preview
					v := httpapi.PatternList{SchemaVersion: 1, Contract: httpapi.PatternLibraryContract, Library: httpapi.PatternLibraryInfo{Path: filepath.Join(t.TempDir(), "reported-not-opened"), State: "ready", CreateOnly: true, MaxRecords: 128, MaxPackageBytes: 1 << 20, RecordContract: patterns.RevisionDomain, CompileContract: patterns.CompileContract, SnapshotKind: patterns.SnapshotKind}, Patterns: []httpapi.PatternSummary{{ID: r.ID, Title: r.Title, Revision: r.Revision, CompileContract: r.CompileContract, TotalSteps: p.TotalSteps, NominalDurationMS: p.NominalDurationMS, OnFinish: r.CompiledProgram.OnFinish, RequiredCapabilities: p.RequiredCapabilities}}}
					if mode == "list-contract" {
						v.Contract = "future"
					}
					if mode == "list-order" {
						v.Patterns = append(v.Patterns, v.Patterns[0])
					}
					if mode == "list-bounds" {
						v.Patterns[0].NominalDurationMS = 1
					}
					_ = json.NewEncoder(w).Encode(v)
				case "record-revision":
					r.Revision = "sha256:" + strings.Repeat("0", 64)
					_ = json.NewEncoder(w).Encode(r)
				case "record-noncanonical":
					r.CompiledProgram.Repeat = 0
					_ = json.NewEncoder(w).Encode(r)
				case "snapshot-whitespace":
					_, _ = w.Write(append(data, ' '))
				case "snapshot-authoring":
					_, _ = io.WriteString(w, `{"schema_version":1,"patterns":[]}`)
				case "snapshot-other-id":
					_, _ = w.Write(data)
				case "duplicate-media":
					w.Header().Add("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(r)
				case "duplicate-key":
					_, _ = io.WriteString(w, `{"id":"other",`+string(mustPatternJSON(t, r)[1:]))
				}
			}))
			defer server.Close()
			c, _ := New(server.URL)
			defer c.Close()
			var err error
			switch {
			case strings.HasPrefix(mode, "list-"):
				_, err = c.ListPatterns(context.Background())
			case strings.HasPrefix(mode, "snapshot-"):
				id := ""
				if mode == "snapshot-other-id" {
					id = "other"
				}
				_, err = c.ExportPatterns(context.Background(), id)
			default:
				_, err = c.GetPattern(context.Background(), "saved")
			}
			var malformed *DecodeError
			if !errors.As(err, &malformed) {
				t.Fatalf("malformed response accepted: %v", err)
			}
		})
	}
}
func mustPatternJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPatternImportPublicationUncertaintyDoesNotRetry(t *testing.T) {
	_, data := patternFixture(t)
	for _, mode := range []string{"transport", "malformed", "conflict", "uncertain", "duplicate-error"} {
		t.Run(mode, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				if mode == "transport" {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "malformed":
					fmt.Fprint(w, `{"records":[]}`)
				case "conflict":
					w.WriteHeader(409)
					fmt.Fprint(w, `{"error":{"code":"pattern_conflict","message":"exists"}}`)
				case "uncertain":
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":{"code":"pattern_commit_uncertain","message":"check library"}}`)
				case "duplicate-error":
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":{"code":"pattern_commit_uncertain","code":"invalid_request","message":"ambiguous"}}`)
				}
			}))
			defer server.Close()
			c, _ := New(server.URL)
			defer c.Close()
			value, err := c.ImportPatterns(context.Background(), data)
			var failure *PatternImportError
			if !errors.As(err, &failure) || failure.PublicationUnknown != (mode != "conflict") || value.Records != nil || atomic.LoadInt32(&calls) != 1 {
				t.Fatalf("%+v %v calls=%d", value, err, atomic.LoadInt32(&calls))
			}
			if mode == "conflict" {
				var api *Error
				if !errors.As(err, &api) || api.Code != "pattern_conflict" {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPatternRunOnePostOriginalTicketAndLookupNeverResolvesAgain(t *testing.T) {
	r, _ := patternFixture(t)
	serial := "demo"
	ref := httpapi.PatternRef{ID: r.ID, Revision: r.Revision}
	p := patternPreviewFixture(t, r, &serial)
	job := patternJobFixture(r)
	for _, mode := range []string{"ok", "wrong-ref", "wrong-program", "wrong-instance", "duplicate-media", "preflight-not-found", "post-transport", "lookup-changed"} {
		t.Run(mode, func(t *testing.T) {
			var previews, writes, lookups int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if req.URL.Path == "/v1/programs/preview" {
					atomic.AddInt32(&previews, 1)
					var sent httpapi.PreviewRequest
					_ = json.NewDecoder(req.Body).Decode(&sent)
					if sent.PatternRef == nil || *sent.PatternRef != ref || sent.Serial == nil || *sent.Serial != serial {
						t.Error("preview reference changed")
					}
					if mode == "preflight-not-found" {
						w.WriteHeader(404)
						fmt.Fprint(w, `{"error":{"code":"pattern_not_found","message":"missing"}}`)
						return
					}
					v := p
					if mode == "wrong-ref" {
						v.PatternRef.Revision = "sha256:" + strings.Repeat("0", 64)
					}
					_ = json.NewEncoder(w).Encode(v)
					return
				}
				if req.Method == http.MethodGet {
					atomic.AddInt32(&lookups, 1)
					if req.URL.Path != "/v1/devices/demo/requests/"+patternTicket.RequestID || req.URL.Query().Get("expected_daemon_instance_id") != patternTicket.ExpectedDaemonInstanceID {
						t.Error("lookup changed ticket")
					}
					w.Header().Set("X-NB1-Daemon-Instance-ID", patternTicket.ExpectedDaemonInstanceID)
					j := job
					j.Status = "succeeded"
					j.CompletedSteps = j.TotalSteps
					if mode == "lookup-changed" {
						j.ID = "wrong"
					}
					_ = json.NewEncoder(w).Encode(j)
					return
				}
				atomic.AddInt32(&writes, 1)
				if req.URL.Path != "/v1/devices/demo/jobs" {
					t.Error(req.URL.Path)
				}
				var sent httpapi.SubmitRequest
				_ = json.NewDecoder(req.Body).Decode(&sent)
				if sent.PatternRef == nil || *sent.PatternRef != ref || sent.Policy != "reject" || sent.RequestID == nil || *sent.RequestID != patternTicket.RequestID || sent.ExpectedDaemonInstanceID == nil || *sent.ExpectedDaemonInstanceID != patternTicket.ExpectedDaemonInstanceID {
					t.Error("submission changed input")
				}
				if mode == "post-transport" {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				patternReceiptHeaders(w)
				j := job
				if mode == "wrong-program" {
					j.Program.Steps = append([]control.Step(nil), job.Program.Steps...)
					j.Program.Steps[0].Color = "#ffffff"
				}
				if mode == "wrong-instance" {
					w.Header().Set("X-NB1-Daemon-Instance-ID", "wrong")
				}
				if mode == "duplicate-media" {
					w.Header().Add("Content-Type", "application/json")
				}
				w.WriteHeader(202)
				_ = json.NewEncoder(w).Encode(j)
			}))
			defer server.Close()
			c, _ := New(server.URL)
			defer c.Close()
			result, err := c.SubmitPatternWithReceipt(context.Background(), serial, ref, patternTicket)
			if mode == "ok" || mode == "lookup-changed" {
				if err != nil || result.Job.ID != job.ID {
					t.Fatal(result, err)
				}
				got, lookupErr := c.LookupPatternRequest(context.Background(), serial, patternTicket, result.Job)
				if mode == "ok" {
					if lookupErr != nil || got.Status != "succeeded" {
						t.Fatal(got, lookupErr)
					}
				} else {
					var failure *ReceiptError
					if !errors.As(lookupErr, &failure) || !failure.OutcomeUnknown {
						t.Fatal(lookupErr)
					}
				}
				if atomic.LoadInt32(&lookups) != 1 {
					t.Fatal(atomic.LoadInt32(&lookups))
				}
			} else {
				var failure *ReceiptError
				if !errors.As(err, &failure) || !failure.OutcomeUnknown || failure.Receipt != patternTicket {
					t.Fatal(result, err)
				}
			}
			wantWrites := int32(1)
			if mode == "wrong-ref" || mode == "preflight-not-found" {
				wantWrites = 0
			}
			if atomic.LoadInt32(&previews) != 1 || atomic.LoadInt32(&writes) != wantWrites {
				t.Fatalf("unexpected HTTP %d/%d", atomic.LoadInt32(&previews), atomic.LoadInt32(&writes))
			}
		})
	}
}
