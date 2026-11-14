package colorinput

import (
	"encoding/json"
	"errors"

	"github.com/b1ug/colorkit"
)

func hsv(fields map[string]json.Token) (Result, error) {
	// Keep original JSON decimal tokens. Float64 would lose the lexical limits
	// and exact half-up boundary that the shared decimal converter validates.
	h, _ := fields["h_deg"].(json.Number)
	s, _ := fields["s_pct"].(json.Number)
	v, _ := fields["v_pct"].(json.Number)
	rgb, err := colorkit.FromHSVDecimal(h.String(), s.String(), v.String())
	if err != nil {
		if errors.Is(err, colorkit.ErrRange) {
			return Result{}, invalid("hsv channel outside its permitted range")
		}
		return Result{}, invalid("hsv channels require nonnegative decimals with at most six fractional digits")
	}
	return resultFromCore("hsv", rgb), nil
}
