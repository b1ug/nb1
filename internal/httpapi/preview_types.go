package httpapi

import "github.com/b1ug/nb1/internal/control"

// PreviewRequest is compilation only. Serial optionally checks one configured
// cached snapshot; policy and receipts are not accepted. A fixed Pattern
// reference is mutually exclusive with Program and Effect.
type PreviewRequest struct {
	PatternRef *PatternRef      `json:"pattern_ref,omitempty"`
	Program    *control.Program `json:"program,omitempty"`
	Effect     *control.Effect  `json:"effect,omitempty"`
	Serial     *string          `json:"serial,omitempty"`
}
