package jsoninput

import (
	"testing"

	"github.com/b1ug/nb1/internal/control"
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
		if err := DecodeObject([]byte(input), &program); err == nil {
			t.Errorf("accepted ambiguous/invalid input %s", input)
		}
	}
}

func TestAcceptCanonicalOmittedDefaults(t *testing.T) {
	var program control.Program
	if err := DecodeObject([]byte(`{"steps":[{"color":"#800000","led":2}],"on_finish":"hold"}`), &program); err != nil {
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
