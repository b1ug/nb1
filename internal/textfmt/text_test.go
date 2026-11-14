package textfmt

import "testing"

// Golden outputs preserve the help-text behavior used by the existing CLI,
// including its intentionally different treatment of the first and last lines.
func TestHereDocLegacyGolden(t *testing.T) {
	for _, test := range []struct{ name, raw, want string }{
		{"empty", "", ""},
		{"single line", "  nb1", "  nb1"},
		{"indented block", "\n\t\tControl blink(1)\n\t\tUse nb1 --help.\n\t", "Control blink(1)\nUse nb1 --help.\n"},
		{"inline first line", "Usage:\n\t\tnb1 control\n\t\t\t--endpoint URL\n\t\t", "Usage:\nnb1 control\n\t--endpoint URL\n"},
		{"mixed margin", "\n \tone\n  \ttwo\n", "one\n\ttwo\n"},
		{"short blank line", "\n    one\n \n      two\n  ", "one\n \n  two\n"},
		{"nested trailing indentation", "\n\t\tFoo\n\t\t\tBar\n\t\t\t\tHoge\n\t\t\t\t\t", "Foo\n\tBar\n\t\tHoge\n\t\t\t"},
		{"all blank", "\n  \n\t", "  \n"},
		{"unicode content", "\n\t灯光\n\tblink(1) 💡\n", "灯光\nblink(1) 💡\n"},
		{"unicode is not a margin", "\n\u3000fullwidth space", "\u3000fullwidth space"},
		{"CRLF preserved", "first\r\n\tsecond\r\n", "first\r\nsecond\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := HereDoc(test.raw); got != test.want {
				t.Fatalf("help text changed: got %q, want %q", got, test.want)
			}
		})
	}
	if got := HereDocf("\n\tcount: %3d\n\tlabel: %s\n", 42, "  light"); got != "count:  42\nlabel:   light\n" {
		t.Fatalf("formatting must follow margin removal: %q", got)
	}
}

func TestOneLineJSONLegacyGolden(t *testing.T) {
	for _, test := range []struct {
		name  string
		value interface{}
		want  string
	}{
		{"nil", nil, "null"},
		{"boolean", false, "false"},
		{"number", 123, "123"},
		{"html literal", "<lamp>&", `"<lamp>&"`},
		{"string whitespace", "  lamp\n", `"  lamp\n"`},
		{"unicode", "灯光💡", `"灯光💡"`},
		{"map order", map[string]interface{}{"z": true, "a": []int{1, 2}}, `{"a":[1,2],"z":true}`},
		{"unsupported fallback", make(chan int), "{}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := OneLineJSON(test.value); got != test.want {
				t.Fatalf("configuration formatting changed: got %q, want %q", got, test.want)
			}
		})
	}
}
