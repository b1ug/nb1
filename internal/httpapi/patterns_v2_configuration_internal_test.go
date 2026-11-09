package httpapi

import (
	"testing"

	"github.com/b1ug/nb1/internal/patternstore"
)

func TestPatternV2DefaultDirectoryConstructionUsesIndependentPurePath(t *testing.T) {
	want, err := patternstore.DefaultV2Dir()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := patternstore.DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	h := &handler{}
	WithPatternDirectoryV2("")(h)
	if h.patternErrV2 != nil || h.patternsV2 == nil || h.patternsV2.Path() != want || want == legacy {
		t.Fatal("default v2 path was not independently selected", h.patternErrV2, want, legacy)
	}
	// No List/Get/Import is called: this test never opens the user's library.
}
