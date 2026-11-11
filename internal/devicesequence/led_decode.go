package devicesequence

import "bytes"

// UnmarshalJSON accepts exactly the integer tokens 0, 1 and 2.
func (led *LED) UnmarshalJSON(data []byte) error {
	if led == nil {
		return invalid("unsupported_led", "led", "An LED destination is required.")
	}
	value, err := decodeLED(data, "led")
	if err != nil {
		return err
	}
	*led = value
	return nil
}

func decodeLED(data []byte, path string) (LED, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 1 && data[0] >= '0' && data[0] <= '2' {
		return LED(data[0] - '0'), nil
	}
	return 0, invalid("unsupported_led", path, "Use the integer 0 (all outputs), 1 or 2.")
}
