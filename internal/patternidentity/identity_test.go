package patternidentity_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/patternidentity"
)

func TestExactRefAndHashDomain(t *testing.T) {
	r := patternidentity.Ref{RecordContract: patternidentity.RecordContract, ID: "Latest_0-A", Revision: "sha256:" + strings.Repeat("0", 64)}
	for _, id := range []string{"a", "0", "latest", strings.Repeat("Z", 64), r.ID} {
		copy := r
		copy.ID = id
		if err := patternidentity.ValidateRef(copy); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"", "_a", "-a", "a/b", "a.b", "图案", strings.Repeat("a", 65)} {
		copy := r
		copy.ID = id
		if err := patternidentity.ValidateRef(copy); !errors.Is(err, patternidentity.ErrInvalid) {
			t.Fatal("accepted ID", id, err)
		}
	}
	for _, revision := range []string{"", "latest", "sha256:" + strings.Repeat("A", 64), "SHA256:" + strings.Repeat("0", 64), r.Revision + "\n", r.Revision[:70]} {
		copy := r
		copy.Revision = revision
		if err := patternidentity.ValidateRef(copy); !errors.Is(err, patternidentity.ErrInvalid) {
			t.Fatal("accepted revision", revision, err)
		}
	}
	r.RecordContract = "future/v3"
	if err := patternidentity.ValidateRef(r); !errors.Is(err, patternidentity.ErrInvalid) {
		t.Fatal(err)
	}
	// Hash is intentionally not a JSON/source verifier, even for arbitrary bytes.
	for _, b := range [][]byte{nil, []byte(`{"x":0}`), []byte("not JSON\n")} {
		want := fmt.Sprintf("sha256:%x", sha256.Sum256(append([]byte(patternidentity.ExecutionDomain+"\n"), b...)))
		if patternidentity.HashExecutionCanonical(b) != want {
			t.Fatal("fixed domain changed")
		}
	}
}
