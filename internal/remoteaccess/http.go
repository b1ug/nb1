package remoteaccess

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"path"
	"strings"
)

// Handler exposes only the remote API and MCP endpoints. A listener serving it
// must terminate TLS directly, since proxy-supplied authority is not trusted.
func Handler(authority, token string, api, mcp http.Handler) http.Handler {
	authenticate := bearerAuthentication(token)
	authority = strings.TrimSuffix(authority, ":443")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")

		var next http.Handler
		switch {
		case apiPath(r.URL.Path):
			next = api
		case r.URL.Path == "/mcp":
			next = mcp
		}
		if next == nil {
			writeError(w, http.StatusNotFound, "not_found", "Remote endpoint not found.")
			return
		}

		if r.TLS == nil {
			writeError(w, http.StatusForbidden, "tls_required", "Remote endpoints require HTTPS.")
			return
		}
		if authority == "" || !sameHTTPSAuthority(r.Host, authority) {
			writeError(w, http.StatusForbidden, "invalid_host", "Remote request host is not allowed.")
			return
		}
		if origins := headerValues(r.Header, "Origin"); len(origins) != 0 &&
			(len(origins) != 1 || (!strings.HasPrefix(origins[0], "https://") || !sameHTTPSAuthority(strings.TrimPrefix(origins[0], "https://"), authority))) {
			writeError(w, http.StatusForbidden, "invalid_origin", "Remote request origin is not allowed.")
			return
		}

		authenticate(w, r, next)
	})
}

// HTTPS clients can omit the default port in both Host and Origin.
func sameHTTPSAuthority(value, expected string) bool {
	if strings.EqualFold(value, expected) {
		return true
	}
	_, _, err := net.SplitHostPort(expected)
	return err != nil && strings.EqualFold(value, expected+":443")
}

// ProtectLocal applies the explicitly configured local token policy after the
// caller has verified that the request addresses the loopback HTTP API.
func ProtectLocal(token string, next http.Handler) http.Handler {
	authenticate := bearerAuthentication(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		switch {
		case r.URL.Path == "/healthz" && r.Method == http.MethodGet:
			next.ServeHTTP(w, withoutAuthorization(r))
		case r.URL.Path == "/demo" || strings.HasPrefix(r.URL.Path, "/demo/"):
			writeError(w, http.StatusForbidden, "workbench_unavailable", "The Workbench is unavailable when local API authentication is enabled.")
		default:
			authenticate(w, r, next)
		}
	})
}

// bearerAuthentication checks the same token format and response contract on
// both listeners. Credential headers never reach the downstream handler.
func bearerAuthentication(token string) func(http.ResponseWriter, *http.Request, http.Handler) {
	expectedDigest := sha256.Sum256([]byte(token))
	validToken := validBearerToken(token)
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		authorization := headerValues(r.Header, "Authorization")
		if !validToken || !authorized(authorization, expectedDigest) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="nb1"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid Bearer token is required.")
			return
		}
		next.ServeHTTP(w, withoutAuthorization(r))
	}
}

func withoutAuthorization(r *http.Request) *http.Request {
	for key := range r.Header {
		if strings.EqualFold(key, "Authorization") {
			forward := r.Clone(r.Context())
			for key := range forward.Header {
				if strings.EqualFold(key, "Authorization") {
					delete(forward.Header, key)
				}
			}
			return forward
		}
	}
	return r
}

func apiPath(requestPath string) bool {
	return (requestPath == "/v1" || strings.HasPrefix(requestPath, "/v1/")) &&
		path.Clean(requestPath) == strings.TrimSuffix(requestPath, "/")
}

// Go's wire parser canonicalizes names, but a composed handler can also receive
// a Header map that contains multiple spellings of the same field.
func headerValues(headers http.Header, name string) []string {
	var values []string
	for key, entries := range headers {
		if strings.EqualFold(key, name) {
			values = append(values, entries...)
		}
	}
	return values
}

func authorized(authorization []string, expectedDigest [sha256.Size]byte) bool {
	if len(authorization) != 1 {
		return false
	}
	scheme, token, ok := strings.Cut(authorization[0], " ")
	token = strings.TrimLeft(token, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || !validBearerToken(token) {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(expectedDigest[:], digest[:]) == 1
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]errorBody{
		"error": {Code: code, Message: message},
	})
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
