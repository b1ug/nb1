package devicesequence

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSequenceLEDReadWriteVocabularyRoundTrip(t *testing.T) {
	for _, input := range []LED{0, 1, 2} {
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var got LED
		if err := json.Unmarshal(data, &got); err != nil || got != input {
			t.Fatalf("%s: %d %v", data, got, err)
		}
	}
	for _, data := range []string{`0`, `3`, `-1`, `"1"`, `"ALL"`, `null`, `true`, `{}`, `[]`, `1.0`, `1e0`} {
		var got LED
		if err := json.Unmarshal([]byte(data), &got); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %s: %v", data, err)
		}
	}
}
