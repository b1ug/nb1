package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/daemonclient"
)

func TestOfficialStdioSequenceColorsMatchResolver(t *testing.T) {
	server, backend := engineServer(t)
	session := connectStdio(t, server.URL, 3*time.Second)
	requireSuccess(t, call(t, session, "save_color", `{"name":"status-ok","color":"#abc"}`), nil)
	for _, color := range []string{
		`"#abc"`, `"red"`, `"green"`, `"@status-ok"`,
		`{"space":"rgb8","r":0,"g":255,"b":136}`,
		`{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}`,
	} {
		var resolved resolvedColor
		requireSuccess(t, call(t, session, "resolve_color", `{"color":`+color+`}`), &resolved)
		var preview daemonclient.SequencePreview
		args := fmt.Sprintf(`{"sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":%s}]}]}}`, color)
		requireSuccess(t, call(t, session, "preview_light", args), &preview)
		if got := preview.Sequence.Steps[0].Targets[0].Color; got != resolved.Color {
			t.Fatalf("Sequence color %s resolved to %q; resolver returned %q", color, got, resolved.Color)
		}
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.sets != 0 {
		t.Fatalf("color previews performed %d device writes", backend.sets)
	}
}
