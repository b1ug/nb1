package jsoninput

import "testing"

func TestUnicodeValidationDoesNotSilentlyRepairEscapedSurrogates(t *testing.T) {
	for _, data := range []string{`{"text":"plain"}`, `{"text":"\u0000\ud7ff\ue000\ufffd"}`, `{"text":"\ud800\udc00"}`, `{"text":"\uD83D\uDE00"}`, `{"text":"\\ud800"}`, `{"text":"�😀"}`, `{"text":"escaped\" quote and \\ slash"}`} {
		if !ValidUnicode([]byte(data)) {
			t.Fatalf("rejected valid Unicode %s", data)
		}
	}
	for _, data := range []string{`{"text":"\ud800"}`, `{"text":"\udfff"}`, `{"text":"\ud800x"}`, `{"text":"\ud800\ud800"}`, `{"text":"\ud800\u0000"}`, `{"text":"\ud800\udc"}`, `{"\ud800":"value"}`, `{"text":"\uGGGG"}`} {
		if ValidUnicode([]byte(data)) {
			t.Fatalf("accepted invalid Unicode %s", data)
		}
	}
	if ValidUnicode([]byte{'"', 0xff, '"'}) {
		t.Fatal("accepted invalid raw UTF-8")
	}
}
