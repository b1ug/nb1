package httpapi_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/libraryv1"
	"github.com/b1ug/nb1/internal/sequence"
)

func sequenceColorAuthor(color string) string {
	return `{"timing":"serial","steps":[{"targets":[{"led":0,"color":` + color + `}]}]}`
}

func TestSequenceAuthoringPreviewUsesSharedColorInputsWithoutPlayback(t *testing.T) {
	server, backend := sequenceRouteServer(t, httpapi.WithLibraryDirectory(""))
	for _, test := range []struct{ raw, want string }{
		{`"#abc"`, "#aabbcc"}, {`"red"`, "#ff0000"},
		{`{"space":"rgb8","r":0,"g":128,"b":255}`, "#0080ff"},
		{`{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":100}`, "#00ff00"},
	} {
		data := sequenceLibraryResponse(t, server, "POST", "/v1/preview", `{"sequence":`+sequenceColorAuthor(test.raw)+`}`, 200)
		var result struct {
			Sequence sequence.Sequence `json:"sequence"`
		}
		if err := json.Unmarshal(data, &result); err != nil || result.Sequence.Steps[0].Targets[0].Color != test.want {
			t.Fatalf("preview %s: %s, %v", test.raw, data, err)
		}
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	for _, op := range backend.operations {
		if op == "set" || strings.HasPrefix(op, "stop:") {
			t.Fatalf("preview performed %s", op)
		}
	}
}

func TestSequenceAuthoringAliasesFreezeInPatternsAndKeepErrorCategories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	server, backend := sequenceRouteServer(t, httpapi.WithLibraryDirectory(dir))
	sequenceLibraryResponse(t, server, "PUT", "/v1/colors/ready", `{"color":"#abc"}`, 200)
	data := sequenceLibraryResponse(t, server, "PUT", "/v1/patterns/colored", `{"sequence":`+sequenceColorAuthor(`"@ready"`)+`}`, 200)
	var pattern libraryv1.Pattern
	if err := json.Unmarshal(data, &pattern); err != nil || pattern.Sequence.Steps[0].Targets[0].Color != "#aabbcc" {
		t.Fatalf("saved pattern: %s %v", data, err)
	}
	sequenceLibraryResponse(t, server, "PUT", "/v1/colors/ready", `{"color":"red"}`, 200)
	data = sequenceLibraryResponse(t, server, "POST", "/v1/preview", `{"pattern_id":"colored"}`, 200)
	if !strings.Contains(string(data), `"color":"#aabbcc"`) {
		t.Fatalf("saved color reference was not frozen: %s", data)
	}
	data = sequenceLibraryResponse(t, server, "POST", "/v1/preview", `{"sequence":`+sequenceColorAuthor(`"@missing"`)+`}`, 400)
	if !strings.Contains(string(data), `"code":"invalid_request"`) {
		t.Fatalf("missing alias category: %s", data)
	}
	makeSequenceLibraryUnavailable(t, dir)
	data = sequenceLibraryResponse(t, server, "POST", "/v1/preview", `{"sequence":`+sequenceColorAuthor(`"@ready"`)+`}`, 503)
	if !strings.Contains(string(data), `"code":"unavailable"`) {
		t.Fatalf("unreadable library category: %s", data)
	}
	absent, _ := sequenceRouteServer(t, httpapi.WithLibraryDirectory(""))
	sequenceLibraryResponse(t, absent, "POST", "/v1/preview", `{"sequence":`+sequenceColorAuthor(`"@ready"`)+`}`, 503)
	backend.mu.Lock()
	defer backend.mu.Unlock()
	for _, op := range backend.operations {
		if op == "set" || strings.HasPrefix(op, "stop:") {
			t.Fatalf("authoring performed %s", op)
		}
	}
}

func TestSequenceAuthoringRetainsRawInputIdentityAcrossAliasChanges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	server, backend := sequenceRouteServer(t, httpapi.WithLibraryDirectory(dir))
	sequenceLibraryResponse(t, server, "PUT", "/v1/colors/ready", `{"color":"red"}`, 200)
	raw := `{"id":"color-alias-once","serial":"20000001","sequence":` + sequenceColorAuthor(`"@ready"`) + `}`
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", raw, 202)
	waitSequenceLibraryJob(t, server, "color-alias-once")
	backend.mu.Lock()
	writes := len(backend.steps)
	backend.mu.Unlock()
	sequenceLibraryResponse(t, server, "PUT", "/v1/colors/ready", `{"color":"blue"}`, 200)
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", raw, 200)
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", strings.Replace(raw, `"@ready"`, `"red"`, 1), 409)
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.steps) != writes {
		t.Fatal("replay executed a newly resolved color")
	}
}
