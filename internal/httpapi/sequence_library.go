package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/actionv1"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/libraryv1"
)

var (
	errSequenceLibraryUnavailable = errors.New("local library is unavailable")
	sequenceColorName             = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// WithLibraryDirectory selects a local v1 library without accessing its files.
// An empty directory leaves library operations unavailable.
func WithLibraryDirectory(dir string) Option {
	return func(h *handler) {
		h.library = nil
		if dir != "" {
			h.library = libraryv1.New(dir)
		}
	}
}

// The authoring compiler attaches field errors to invalid colors. Preserve an
// unavailable store separately so a filesystem failure remains a service error.
type sequenceColorResolver struct {
	store       *libraryv1.Store
	unavailable bool
}

func (resolver *sequenceColorResolver) resolve(name string) (string, error) {
	if resolver.store == nil {
		resolver.unavailable = true
		return "", errSequenceLibraryUnavailable
	}
	color, err := resolver.store.ResolveColor(name)
	if err != nil && !errors.Is(err, libraryv1.ErrNotFound) {
		resolver.unavailable = true
	}
	return color, err
}

func (h *handler) resolveSequenceColorInput(raw []byte) (string, error) {
	resolver := sequenceColorResolver{store: h.library}
	color, err := actionv1.ResolveColor(raw, resolver.resolve)
	if resolver.unavailable {
		return "", errSequenceLibraryUnavailable
	}
	return color, err
}

func sequenceLibraryError(err error) error {
	if err == nil || errors.Is(err, libraryv1.ErrNotFound) || errors.Is(err, libraryv1.ErrCapacity) {
		return err
	}
	return errSequenceLibraryUnavailable
}

func (h *handler) requireSequenceLibrary(w http.ResponseWriter) bool {
	if h.library == nil {
		sequenceAPIError(w, errSequenceLibraryUnavailable)
		return false
	}
	return true
}

func (h *handler) serveSequenceLibrary(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 2 || parts[0] != "v1" {
		return false
	}
	switch parts[1] {
	case "colors":
		if len(parts) == 2 {
			h.listSequenceColors(w, r)
			return true
		}
		if len(parts) == 3 {
			h.saveSequenceColor(w, r, parts[2])
			return true
		}
	case "patterns":
		if len(parts) == 2 {
			h.listSequencePatterns(w, r)
			return true
		}
		if len(parts) == 3 {
			h.sequencePattern(w, r, parts[2])
			return true
		}
	}
	return false
}

func (h *handler) listSequenceColors(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) || !frameNoBodyOrQuery(w, r) || !h.requireSequenceLibrary(w) {
		return
	}
	colors, err := h.library.ListColors()
	if err != nil {
		sequenceAPIError(w, sequenceLibraryError(err))
		return
	}
	if colors == nil {
		colors = []libraryv1.Color{}
	}
	writeJSON(w, http.StatusOK, colors)
}

func (h *handler) saveSequenceColor(w http.ResponseWriter, r *http.Request, name string) {
	if !method(w, r, http.MethodPut) {
		return
	}
	if !sequenceColorName.MatchString(name) {
		writeError(w, http.StatusBadRequest, "invalid_request", "color name must begin with a lowercase ASCII letter and contain 1–32 letters, digits or hyphens")
		return
	}
	var input struct {
		Color json.RawMessage `json:"color"`
	}
	if !readSequenceJSON(w, r, &input) || !h.requireSequenceLibrary(w) {
		return
	}
	color, err := h.resolveSequenceColorInput(input.Color)
	if err != nil {
		if errors.Is(err, errSequenceLibraryUnavailable) {
			sequenceAPIError(w, err)
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		}
		return
	}
	result, err := h.library.SaveColor(name, color)
	if err != nil {
		sequenceAPIError(w, sequenceLibraryError(err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listSequencePatterns(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) || !frameNoBodyOrQuery(w, r) || !h.requireSequenceLibrary(w) {
		return
	}
	patterns, err := h.library.ListPatterns()
	if err != nil {
		sequenceAPIError(w, sequenceLibraryError(err))
		return
	}
	type summary struct {
		ID    string `json:"id"`
		Title string `json:"title,omitempty"`
	}
	summaries := make([]summary, 0, len(patterns))
	for _, pattern := range patterns {
		summaries = append(summaries, summary{ID: pattern.ID, Title: pattern.Title})
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (h *handler) sequencePattern(w http.ResponseWriter, r *http.Request, id string) {
	if !control.ValidSequenceJobID(id) {
		writeError(w, http.StatusBadRequest, "invalid_request", "pattern ID must contain 1–64 ASCII letters, digits, hyphens or underscores")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !frameNoBodyOrQuery(w, r) || !h.requireSequenceLibrary(w) {
			return
		}
		pattern, err := h.library.GetPattern(id)
		if err != nil {
			sequenceAPIError(w, sequenceLibraryError(err))
			return
		}
		writeJSON(w, http.StatusOK, pattern)
	case http.MethodPut:
		h.saveSequencePattern(w, r, id)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (h *handler) saveSequencePattern(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Action   json.RawMessage `json:"action,omitempty"`
		Sequence json.RawMessage `json:"sequence,omitempty"`
		Title    string          `json:"title,omitempty"`
	}
	if !readSequenceJSON(w, r, &body) {
		return
	}
	input := sequenceInput{Action: body.Action, Sequence: body.Sequence}
	if !input.validate(w, false) {
		return
	}
	if utf8.RuneCountInString(body.Title) > libraryv1.MaxTitleRunes {
		writeError(w, http.StatusBadRequest, "invalid_request", "title must contain at most 256 Unicode characters")
		return
	}
	if !h.requireSequenceLibrary(w) {
		return
	}
	compiled, err := h.compileSequenceInput(input)
	if err != nil {
		sequenceAPIError(w, err)
		return
	}
	inputPattern := libraryv1.Pattern{ID: id, Title: body.Title, Sequence: compiled}
	encoded, err := json.MarshalIndent(inputPattern, "", "  ")
	if err != nil || len(encoded)+1 > libraryv1.MaxRecordBytes {
		writeError(w, http.StatusBadRequest, "invalid_request", "compiled Pattern exceeds the local record byte limit")
		return
	}
	pattern, err := h.library.SavePattern(inputPattern)
	if err != nil {
		sequenceAPIError(w, sequenceLibraryError(err))
		return
	}
	writeJSON(w, http.StatusOK, pattern)
}
