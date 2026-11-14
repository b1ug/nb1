// Package patternidentity defines bounded Pattern identity and a fixed hash
// domain only. It does not parse JSON, verify sources or depend on execution.
package patternidentity

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

const RecordContract = "nb1-pattern-record/experimental-v2"
const ExecutionDomain = "nb1-pattern-execution/experimental-v1"

var ErrInvalid = errors.New("invalid pattern identity")

type Ref struct {
	RecordContract string `json:"record_contract"`
	ID             string `json:"id"`
	Revision       string `json:"revision"`
}

func ValidateRef(ref Ref) error {
	if ref.RecordContract != RecordContract {
		return fmt.Errorf("%w: unsupported record contract", ErrInvalid)
	}
	if len(ref.ID) < 1 || len(ref.ID) > 64 {
		return fmt.Errorf("%w: ID requires 1–64 ASCII characters", ErrInvalid)
	}
	for i, c := range []byte(ref.ID) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_')) {
			return fmt.Errorf("%w: invalid ID", ErrInvalid)
		}
	}
	return ValidateDigest(ref.Revision)
}

// ValidateDigest checks the shared revision/execution SHA spelling only.
// A syntactically valid digest does not prove source integrity or admission.
func ValidateDigest(value string) error {
	if len(value) != 71 || value[:7] != "sha256:" {
		return fmt.Errorf("%w: expected sha256 plus 64 lowercase hex digits", ErrInvalid)
	}
	for _, c := range value[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return fmt.Errorf("%w: expected lowercase hex digest", ErrInvalid)
		}
	}
	return nil
}

// HashExecutionCanonical hashes the given bytes after exactly one domain LF.
// It intentionally does not validate that bytes are canonical, trusted or even
// JSON. Callers must establish those properties; this is not a verifier.
func HashExecutionCanonical(canonical []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(ExecutionDomain + "\n"))
	_, _ = h.Write(canonical)
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}
