package devicesequence

import (
	"encoding/json"
	"errors"
	"strconv"
	"testing"
)

func TestSequenceLEDReadWriteVocabularyRoundTrip(t *testing.T) {
	for _, input := range []LED{0, 1, 2} {
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != strconv.Itoa(int(input)) {
			t.Fatalf("LED %d must encode as a JSON integer: %s", input, data)
		}
		var got LED
		if err := json.Unmarshal(data, &got); err != nil || got != input {
			t.Fatalf("%s: %d %v", data, got, err)
		}
	}
	for _, data := range []string{`3`, `255`, `256`, `-1`, `-0`, `"0"`, `"1"`, `"2"`, `"all"`, `"ALL"`, `null`, `true`, `{}`, `[]`, `0.0`, `1.0`, `0e0`, `1e0`} {
		var got LED
		if err := json.Unmarshal([]byte(data), &got); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %s: %v", data, err)
		}
	}
	if _, err := json.Marshal(LED(3)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("out-of-range typed LED encoded: %v", err)
	}
}
