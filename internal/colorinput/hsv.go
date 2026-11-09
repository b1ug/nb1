package colorinput

import (
	"encoding/json"
	"math/big"
)

func decimal(value json.Token, max int64) (*big.Rat, error) {
	n, ok := value.(json.Number)
	if !ok || !decimalSyntax(n.String(), 6) {
		return nil, invalid("hsv channels require nonnegative decimals with at most six fractional digits")
	}
	r, ok := new(big.Rat).SetString(n.String())
	if !ok || r.Cmp(big.NewRat(max, 1)) > 0 {
		return nil, invalid("hsv channel outside its permitted range")
	}
	return r, nil
}

func hsv(fields map[string]json.Token) (Result, error) {
	h, err := decimal(fields["h_deg"], 360)
	if err != nil {
		return Result{}, err
	}
	s, err := decimal(fields["s_pct"], 100)
	if err != nil {
		return Result{}, err
	}
	v, err := decimal(fields["v_pct"], 100)
	if err != nil {
		return Result{}, err
	}
	s.Quo(s, big.NewRat(100, 1))
	v.Quo(v, big.NewRat(100, 1))
	h.Quo(h, big.NewRat(60, 1))
	if h.Cmp(big.NewRat(6, 1)) == 0 {
		h.SetInt64(0)
	}
	sector := new(big.Int).Quo(h.Num(), h.Denom()).Int64()
	f := new(big.Rat).Sub(h, big.NewRat(sector, 1))
	one := big.NewRat(1, 1)
	p := new(big.Rat).Mul(v, new(big.Rat).Sub(one, s))
	q := new(big.Rat).Mul(v, new(big.Rat).Sub(one, new(big.Rat).Mul(s, f)))
	t := new(big.Rat).Mul(v, new(big.Rat).Sub(one, new(big.Rat).Mul(s, new(big.Rat).Sub(one, f))))
	var channels [3]*big.Rat
	switch sector {
	case 0:
		channels = [3]*big.Rat{v, t, p}
	case 1:
		channels = [3]*big.Rat{q, v, p}
	case 2:
		channels = [3]*big.Rat{p, v, t}
	case 3:
		channels = [3]*big.Rat{p, q, v}
	case 4:
		channels = [3]*big.Rat{t, p, v}
	case 5:
		channels = [3]*big.Rat{v, p, q}
	default:
		return Result{}, invalid("invalid hue sector")
	}
	return result("hsv", RGB8{quantize(channels[0]), quantize(channels[1]), quantize(channels[2])}), nil
}

func quantize(channel *big.Rat) int {
	rounded := new(big.Rat).Add(new(big.Rat).Mul(channel, big.NewRat(255, 1)), big.NewRat(1, 2))
	return int(new(big.Int).Quo(rounded.Num(), rounded.Denom()).Int64())
}
