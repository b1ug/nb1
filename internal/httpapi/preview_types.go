package httpapi

import "github.com/b1ug/nb1/internal/control"

// PreviewRequest is compilation only. Serial optionally checks one configured
// cached snapshot; policy, receipts and Pattern references are not accepted.
type PreviewRequest struct {
	Program *control.Program `json:"program,omitempty"`
	Effect  *control.Effect  `json:"effect,omitempty"`
	Serial  *string          `json:"serial,omitempty"`
}
