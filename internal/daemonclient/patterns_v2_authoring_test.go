package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternsv2"
)

func clientPatternV2Fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "httpapi", "testdata", "pattern-v2", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPatternV2AuthoringClientRealHTTPPreservesSourceAndLazySave(t *testing.T) {
	for _, name := range []string{"orbit-red-blue", "pulse-lime"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "private-v2")
			handler := httpapi.New(nil, httpapi.WithPatternDirectoryV2(dir))
			var mu sync.Mutex
			var bodies [][]byte
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				mu.Lock()
				bodies = append(bodies, body)
				paths = append(paths, r.Method+" "+r.URL.Path)
				mu.Unlock()
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			draft := clientPatternV2Fixture(t, "requests/preview-"+name+".json")
			want, err := patternsv2.PreviewDraft(draft)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := client.PreviewPatternDraftV2(context.Background(), draft)
			if err != nil || !reflect.DeepEqual(preview.Preview, want.Preview()) {
				t.Fatal(preview, err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("preview created library", err)
			}
			save := clientPatternV2Fixture(t, "requests/save-"+name+".json")
			for i := 0; i < 2; i++ {
				result, err := client.SavePatternV2(context.Background(), save)
				if err != nil || result.Total != 1 {
					t.Fatal(result, err)
				}
				if i == 0 && !reflect.DeepEqual(result.Created, []string{name}) {
					t.Fatal("first save not created", result)
				}
				if i == 1 && !reflect.DeepEqual(result.Existing, []string{name}) {
					t.Fatal("second explicit save not idempotent", result)
				}
			}
			got, err := client.GetPatternV2(context.Background(), name)
			if err != nil {
				t.Fatal(err)
			}
			i, err := inspectPatternRecordV2(got)
			if err != nil || !reflect.DeepEqual(i.Record(), want.Record()) {
				t.Fatal("source or frozen data changed", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(paths, []string{"POST /v2/patterns/preview", "POST /v2/patterns", "POST /v2/patterns", "GET /v2/patterns/" + name}) ||
				!bytes.Equal(bodies[0], draft) || !bytes.Equal(bodies[1], save) || !bytes.Equal(bodies[2], save) || len(bodies[3]) != 0 {
				t.Fatal("unexpected request or changed authoring bytes", paths)
			}
		})
	}
}

func TestPatternV2AuthoringClientRejectsLocalInvalidBeforeHTTP(t *testing.T) {
	client, _ := New("http://127.0.0.1:1")
	defer client.Close()
	for _, fixture := range []string{"preview-duplicate-title.json.txt", "preview-lone-surrogate.json.txt", "preview-forbidden-serial.json"} {
		_, err := client.PreviewPatternDraftV2(context.Background(), clientPatternV2Fixture(t, "requests/"+fixture))
		if !errors.Is(err, patternsv2.ErrInvalid) {
			t.Fatal(fixture, err)
		}
	}
	_, err := client.SavePatternV2(context.Background(), clientPatternV2Fixture(t, "requests/save-revision-mismatch.json"))
	if !errors.Is(err, patternsv2.ErrRevisionConflict) {
		t.Fatal(err)
	}
	if _, err := client.GetPatternV2(context.Background(), "../escape"); err == nil {
		t.Fatal("invalid ID accepted")
	}
}

func TestPatternV2AuthoringClientReadsUnknownRecipeWithoutExecutionClaim(t *testing.T) {
	data := clientPatternV2Fixture(t, "responses/get-unknown-recipe.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v2/patterns/orbit-red-blue" {
			t.Error("unexpected operation", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	value, err := client.GetPatternV2(context.Background(), "orbit-red-blue")
	if err != nil || value.Verification.Verified {
		t.Fatal(value, err)
	}
	i, err := patternsv2.InspectRecord(value.Record)
	if err != nil || i.Record().Compiled.RecipeID != "orbit-unrecognized/experimental-v99" {
		t.Fatal("unknown source lost", err)
	}
	if _, err := i.VerifiedRecord(); !errors.Is(err, patternsv2.ErrUnsupported) {
		t.Fatal("unknown recipe treated as executable", err)
	}
}

func TestPatternV2AuthoringClientRejectsUnboundRecordMetadata(t *testing.T) {
	for _, name := range []string{"record-tamper", "wrong-id", "digest", "verification", "missing-field", "duplicate", "header", "content-type", "status"} {
		t.Run(name, func(t *testing.T) {
			data := clientPatternV2Fixture(t, "responses/get-orbit-red-blue.json")
			var value map[string]interface{}
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "record-tamper":
				value["record"].(map[string]interface{})["title"] = "tampered"
			case "wrong-id":
				data = clientPatternV2Fixture(t, "responses/get-pulse-lime.json")
			case "digest":
				value["execution_digest"] = "sha256:" + strings.Repeat("0", 64)
			case "verification":
				value["verification"] = map[string]interface{}{"verified": false, "reason": "unknown_recipe"}
			case "missing-field":
				delete(value, "execution_digest")
			}
			if name != "wrong-id" {
				data, _ = json.Marshal(value)
			}
			if name == "duplicate" {
				data = append([]byte(`{"schema_version":2,`), data[1:]...)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if name == "header" {
					w.Header().Set("X-NB1-Daemon-Instance-ID", patternTicket.ExpectedDaemonInstanceID)
				}
				if name == "content-type" {
					w.Header().Set("Content-Type", "text/plain")
				}
				if name == "status" {
					w.WriteHeader(http.StatusCreated)
				}
				w.Write(data)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			_, err := client.GetPatternV2(context.Background(), "orbit-red-blue")
			var malformed *DecodeError
			if !errors.As(err, &malformed) {
				t.Fatal("unbound response accepted", name, err)
			}
		})
	}
}

func TestPatternV2SaveClientPreservesPublicationUncertaintyWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		unknown bool
	}{
		{"rejected", 409, `{"error":{"code":"pattern_conflict","message":"conflict"}}`, false},
		{"uncertain", 503, `{"error":{"code":"pattern_commit_uncertain","message":"inspect original ID"}}`, true},
		{"malformed-rejection", 409, `{}`, true},
		{"malformed-success", 201, `{}`, true},
		{"unknown-error-code", 409, `{"error":{"code":"future_error","message":"unknown contract"}}`, true},
		{"wrong-status-for-code", 400, `{"error":{"code":"pattern_conflict","message":"wrong status"}}`, true},
		{"redirect", 307, `{"error":{"code":"pattern_conflict","message":"not a rejection"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := make(chan string, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls <- r.Method + " " + r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			_, err := client.SavePatternV2(context.Background(), clientPatternV2Fixture(t, "requests/save-orbit-red-blue.json"))
			var publication *PatternImportError
			if !errors.As(err, &publication) || publication.PublicationUnknown != tc.unknown {
				t.Fatal(tc.name, err)
			}
			if len(calls) != 1 || <-calls != "POST /v2/patterns" {
				t.Fatal("unexpected retry/read/submit")
			}
		})
	}
}

func TestPatternV2DraftClientRejectsChangedCandidateAndAvailability(t *testing.T) {
	for _, name := range []string{"other-candidate", "preview-facts", "availability", "availability-reason"} {
		t.Run(name, func(t *testing.T) {
			draft := clientPatternV2Fixture(t, "requests/preview-orbit-red-blue.json")
			responseDraft := draft
			if name == "other-candidate" {
				responseDraft = clientPatternV2Fixture(t, "requests/preview-pulse-lime.json")
			}
			r := httptest.NewRequest("POST", "http://127.0.0.1/v2/patterns/preview", bytes.NewReader(responseDraft))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			httpapi.New(nil).ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var value map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "preview-facts":
				value["preview"].(map[string]interface{})["nominal_duration_ms"] = 1
			case "availability":
				value["pattern_execution"].(map[string]interface{})["available"] = true
			case "availability-reason":
				value["pattern_execution"].(map[string]interface{})["reason"] = "anything"
			}
			data, _ := json.Marshal(value)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write(data)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			_, err := client.PreviewPatternDraftV2(context.Background(), draft)
			var malformed *DecodeError
			if !errors.As(err, &malformed) {
				t.Fatal("changed preview accepted", name, err)
			}
		})
	}
}

func TestPatternV2SaveClientRejectsNonemptyChangedConfirmation(t *testing.T) {
	for _, name := range []string{"other-record", "location", "duplicate-location", "created-existing-overlap", "status"} {
		t.Run(name, func(t *testing.T) {
			var value map[string]interface{}
			if err := json.Unmarshal(clientPatternV2Fixture(t, "responses/save-created.json"), &value); err != nil {
				t.Fatal(err)
			}
			if name == "other-record" {
				var other map[string]interface{}
				if err := json.Unmarshal(clientPatternV2Fixture(t, "responses/get-pulse-lime.json"), &other); err != nil {
					t.Fatal(err)
				}
				value["records"] = []interface{}{other["record"]}
			}
			if name == "created-existing-overlap" {
				value["existing"] = []interface{}{"orbit-red-blue"}
			}
			data, _ := json.Marshal(value)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				location := "/v2/patterns/orbit-red-blue"
				if name == "location" {
					location = "/v2/patterns/another"
				}
				w.Header().Set("Location", location)
				if name == "duplicate-location" {
					w.Header().Add("Location", location)
				}
				status := http.StatusCreated
				if name == "status" {
					status = http.StatusOK
				}
				w.WriteHeader(status)
				w.Write(data)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			_, err := client.SavePatternV2(context.Background(), clientPatternV2Fixture(t, "requests/save-orbit-red-blue.json"))
			var publication *PatternImportError
			var malformed *DecodeError
			if !errors.As(err, &publication) || !publication.PublicationUnknown || !errors.As(err, &malformed) {
				t.Fatal("changed confirmation accepted or treated as certain non-publication", name, err)
			}
		})
	}
}

func TestPatternV2ClientPreservesUnknownSourceActionAsReadOnly(t *testing.T) {
	// Fixed test-only future source, independently rehashed with Python over
	// this ASCII-keyed payload. Its typed frozen execution is still validated.
	data, err := os.ReadFile(filepath.Join("testdata", "pattern-v2-future-source.record.json"))
	if err != nil {
		t.Fatal(err)
	}
	i, err := patternsv2.InspectRecord(data)
	if err != nil || i.Support().Verified || i.Support().Reason != "unsupported_source" {
		t.Fatal(i.Support(), err)
	}
	response, err := json.Marshal(PatternRecordV2{2, httpapi.PatternLibraryV2Contract, data, i.Support(), i.ExecutionDigest()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(response)
	}))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	got, err := client.GetPatternV2(context.Background(), i.Record().ID)
	if err != nil || got.Verification.Verified {
		t.Fatal(got, err)
	}
	checked, err := patternsv2.InspectRecord(got.Record)
	if err != nil || !bytes.Equal(checked.Record().Source.Action, i.Record().Source.Action) {
		t.Fatal("future action was lost", err)
	}
	if _, err := checked.VerifiedRecord(); !errors.Is(err, patternsv2.ErrUnsupported) {
		t.Fatal("future source became executable", err)
	}
}

func TestPatternV2SaveClientTransportFailurePreservesOriginalSinglePOST(t *testing.T) {
	client, _ := New("http://127.0.0.1:8931")
	defer client.Close()
	data := clientPatternV2Fixture(t, "requests/save-orbit-red-blue.json")
	calls := 0
	cause := errors.New("confirmation connection lost")
	client.http.Transport = lightRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil || !bytes.Equal(body, data) || r.GetBody != nil || r.Method != "POST" || r.URL.Path != "/v2/patterns" {
			t.Fatal("original single POST binding changed", err, r.Method, r.URL.Path)
		}
		return nil, cause
	})
	_, err := client.SavePatternV2(context.Background(), data)
	var publication *PatternImportError
	if calls != 1 || !errors.As(err, &publication) || !publication.PublicationUnknown || !errors.Is(err, cause) {
		t.Fatal("transport failure lost uncertainty/cause or retried", calls, err)
	}
}
