package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/libraryv1"
)

func TestSequenceDirectCompilesFlexibleTargetColorsBeforeDeviceAccess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	if _, err := libraryv1.New(dir).SaveColor("ready", "#112233"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ raw, want string }{
		{`"#abc"`, "#aabbcc"}, {`"red"`, "#ff0000"}, {`"@ready"`, "#112233"},
		{`{"space":"rgb8","r":1,"g":2,"b":3}`, "#010203"},
		{`{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":100}`, "#00ff00"},
	} {
		raw := `{"timing":"serial","steps":[{"targets":[{"led":0,"color":` + test.raw + `}]}]}`
		input := daemonclient.SequenceInput{Sequence: json.RawMessage(raw)}
		compiled, err := compileSequenceDirect(input, dir)
		if err != nil || compiled.Steps[0].Targets[0].Color != test.want {
			t.Fatalf("direct compile %s = %+v, %v", test.raw, compiled, err)
		}
		if string(input.Sequence) != raw {
			t.Fatal("direct compilation changed author input")
		}
	}
	input := daemonclient.SequenceInput{Sequence: json.RawMessage(`{"timing":"serial","steps":[{"targets":[{"led":0,"color":"@missing"}]}]}`)}
	if _, err := compileSequenceDirect(input, dir); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("direct missing alias did not fail preflight: %v", err)
	}
}
