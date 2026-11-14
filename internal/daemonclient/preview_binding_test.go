package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
)

func TestCompilerClientBindsRawProgramResult(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   control.Program
		change  func(*control.Program)
		wantErr bool
	}{
		{
			name:   "red_request_green_result",
			input:  control.Program{Steps: []control.Step{{LED: 1, Color: "#FF0000"}}},
			change: func(p *control.Program) { p.Steps[0].Color = "#00ff00" }, wantErr: true,
		},
		{
			name:   "changed_repeat",
			input:  control.Program{Steps: []control.Step{{LED: 1, Color: "#FF0000"}}},
			change: func(p *control.Program) { p.Repeat = 2 }, wantErr: true,
		},
		{
			name:   "changed_finish",
			input:  control.Program{Steps: []control.Step{{LED: 1, Color: "#FF0000"}}},
			change: func(p *control.Program) { p.OnFinish = "hold" }, wantErr: true,
		},
		{
			name:   "changed_LED",
			input:  control.Program{Steps: []control.Step{{LED: 1, Color: "#FF0000"}}},
			change: func(p *control.Program) { p.Steps[0].LED = 2 }, wantErr: true,
		},
		{
			name:  "uppercase_and_omitted_defaults_normalize",
			input: control.Program{Steps: []control.Step{{LED: 1, Color: "#FF0000"}}},
		},
		{
			name:  "explicit_defaults_and_lowercase",
			input: control.Program{Steps: []control.Step{{LED: 1, Color: "#ff0000"}}, Repeat: 1, OnFinish: "off"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.input
			before.Steps = append([]control.Step(nil), tc.input.Steps...)
			expected, err := control.Normalize(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			returned := expected
			returned.Steps = append([]control.Step(nil), expected.Steps...)
			if tc.change != nil {
				tc.change(&returned)
			}
			// Even substituted responses are valid, self-consistent previews;
			// input binding must detect the difference, not arithmetic checks.
			response, err := control.Preview(&returned, nil)
			if err != nil {
				t.Fatal(err)
			}
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				if r.Method != http.MethodPost || r.URL.Path != "/v1/programs/preview" {
					t.Errorf("unexpected route: %s %s", r.Method, r.URL.Path)
				}
				var sent httpapi.PreviewRequest
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					t.Error(err)
				}
				if sent.Program == nil || !reflect.DeepEqual(*sent.Program, before) || sent.Effect != nil {
					t.Error("request was changed or sent as an effect")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			got, err := client.Preview(context.Background(), httpapi.PreviewRequest{Program: &tc.input})
			if tc.wantErr {
				var failure *DecodeError
				if !errors.As(err, &failure) || failure.Status != http.StatusOK || !reflect.DeepEqual(got, control.PreviewResult{}) {
					t.Fatalf("substituted valid program accepted: result=%#v err=%v", got, err)
				}
			} else if err != nil || !reflect.DeepEqual(got.Program, expected) {
				t.Fatalf("equivalent normalized program rejected: result=%#v err=%v", got, err)
			}
			if atomic.LoadInt32(&calls) != 1 {
				t.Fatalf("preview retried: %d calls", calls)
			}
			if !reflect.DeepEqual(tc.input, before) {
				t.Fatal("preview mutated input")
			}
		})
	}
}
