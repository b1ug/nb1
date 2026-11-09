package jsoninput_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/jsoninput"
)

func TestRejectAmbiguousControlInput(t *testing.T) {
	for _, input := range []string{
		`{"steps":[{"led":1,"led":0,"color":"#ff0000"}]}`,
		`{"steps":[{"led":1,"LED":0,"color":"#ff0000"}]}`,
		`{"steps":[{"led":null,"color":"#ff0000"}]}`,
		`{"steps":[{"color":"#ff0000","co\u006cor":"#00ff00"}]}`,
		`{"steps":null}`, `{"steps":[]} {}`, `null`, `[]`, `{"steps":[{"color":true}]}`,
	} {
		var program control.Program
		if err := jsoninput.DecodeObject([]byte(input), &program); err == nil {
			t.Errorf("accepted ambiguous/invalid input %s", input)
		}
	}
}

func TestExplicitRawJSONRetainsVersionedPayloadButRejectsAmbiguity(t *testing.T) {
	type envelope struct {
		Record json.RawMessage `json:"record"`
	}
	for _, raw := range []string{
		`{"future_field":[{"value":1.0,"nested":{"color":"red"}},false,"text"]}`,
		`[{"future_field":[]}]`, `42`, `"text"`,
	} {
		var value envelope
		if err := jsoninput.DecodeObject([]byte(`{"record":`+raw+`}`), &value); err != nil || string(value.Record) != raw {
			t.Fatal("explicit RawMessage changed or rejected", raw, string(value.Record), err)
		}
	}
	for _, raw := range []string{
		`{"record":{"x":1,"x":2}}`,
		`{"record":{"x":1,"\u0078":2}}`,
		`{"record":{"a":[{"x":null}]}}`,
		`{"record":null}`, `{"Record":{}}`, `{"record":{},"extra":1}`,
		`{"record":{},"record":{}}`, `{"record":{}} {}`,
		`{"record":` + strings.Repeat("[", 66) + `1` + strings.Repeat("]", 66) + `}`,
	} {
		var value envelope
		if err := jsoninput.DecodeObject([]byte(raw), &value); err == nil {
			t.Fatal("opaque payload bypassed common guard", raw)
		}
	}
	// []byte is not a schema escape hatch; only the explicit RawMessage type
	// enables opaque JSON. Ordinary typed control fields retain strict checking.
	var ordinary struct {
		Record []byte `json:"record"`
	}
	if err := jsoninput.DecodeObject([]byte(`{"record":{"x":1}}`), &ordinary); err == nil {
		t.Fatal("ordinary byte field accepted an object")
	}
}

func TestAcceptCanonicalOmittedDefaults(t *testing.T) {
	var program control.Program
	if err := jsoninput.DecodeObject([]byte(`{"steps":[{"color":"#800000","led":2}],"on_finish":"hold"}`), &program); err != nil {
		t.Fatal(err)
	}
	p, err := control.Normalize(program)
	if err != nil {
		t.Fatal(err)
	}
	if p.Repeat != 1 || p.Steps[0].LED != 2 || p.Steps[0].Color != "#800000" {
		t.Fatalf("changed intent: %+v", p)
	}
}
