package devicesequence

import (
	"bytes"
	"encoding/json"
)

// UnmarshalJSON is the read-side counterpart of MarshalJSON. It preserves the
// draft's explicit all/1/2 vocabulary instead of silently accepting numeric0.
func (led *LED) UnmarshalJSON(data []byte) error {
	if led == nil {
		return invalid("unsupported_led", "led", "An LED destination is required.")
	}
	switch string(bytes.TrimSpace(data)) {
	case "1":
		*led = 1
		return nil
	case "2":
		*led = 2
		return nil
	}
	var text string
	if json.Unmarshal(data, &text) == nil && text == "all" {
		*led = 0
		return nil
	}
	return invalid("unsupported_led", "led", "Use all, 1 or 2.")
}
