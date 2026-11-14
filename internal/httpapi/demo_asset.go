package httpapi

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const demoContentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'"
const demoStyleNoncePlaceholder = "__NB1_STYLE_NONCE__"
const demoStyleNonceMeta = `<meta name="nb1-style-nonce" content="__NB1_STYLE_NONCE__">`

// renderDemoHTML leaves the verified template unchanged and creates a response
// copy. The only dynamic bytes are the nonce in its one canonical meta element.
func renderDemoHTML(source []byte, randomness io.Reader) ([]byte, string, error) {
	if bytes.Count(source, []byte(demoStyleNonceMeta)) != 1 ||
		bytes.Count(source, []byte(demoStyleNoncePlaceholder)) != 1 ||
		bytes.Count(source, []byte("nb1-style-nonce")) != 1 {
		return nil, "", fmt.Errorf("invalid embedded style nonce template")
	}
	var randomBytes [24]byte
	if _, err := io.ReadFull(randomness, randomBytes[:]); err != nil {
		return nil, "", fmt.Errorf("generate style nonce: %w", err)
	}
	nonce := base64.StdEncoding.EncodeToString(randomBytes[:])
	response := bytes.Replace(source, []byte(demoStyleNoncePlaceholder), []byte(nonce), 1)
	policy := strings.Replace(demoContentSecurityPolicy, "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1)
	return response, policy, nil
}

func writeDemoAsset(w http.ResponseWriter, contentType string, data []byte, randomness io.Reader) {
	policy := demoContentSecurityPolicy
	if contentType == "text/html; charset=utf-8" {
		var err error
		data, policy, err = renderDemoHTML(data, randomness)
		if err != nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			writeError(w, http.StatusInternalServerError, "internal_error", "embedded demo HTML unavailable")
			return
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", policy)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
