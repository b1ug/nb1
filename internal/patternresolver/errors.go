package patternresolver

import (
	"context"
	"errors"
	"os"
	"syscall"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/b1ug/nb1/internal/patternsv2"
)

// classifyStoreError never accepts an upstream availability label as evidence.
// The caller may preserve errors.Is/As while the outer non-availability kind
// prevents the core from replaying a malicious nested availability wrapper.
func classifyStoreError(err error) *control.PatternResolveError {
	kind := control.PatternResolveUnknown
	integrity, unsupported, configuration, interrupted, markedUnknown := false, false, false, false, false
	var inspect func(error, int)
	inspect = func(cause error, depth int) {
		if cause == nil {
			return
		}
		if depth > 64 {
			markedUnknown = true
			return
		}
		if nodeMatches(cause, patternstore.ErrCorrupt) || nodeMatches(cause, patternstore.ErrUnsafe) ||
			nodeMatches(cause, patternstore.ErrInvalid) || nodeMatches(cause, patternstore.ErrLimit) ||
			nodeMatches(cause, patternstore.ErrRevisionConflict) || nodeMatches(cause, patternsv2.ErrInvalid) ||
			nodeMatches(cause, patternidentity.ErrInvalid) || nodeMatches(cause, control.ErrInvalid) || nodeMatches(cause, ErrReference) {
			integrity = true
		}
		if nodeMatches(cause, patternsv2.ErrUnsupported) || nodeMatches(cause, patternstore.ErrUnsupported) || nodeMatches(cause, patternstore.ErrReadOnly) {
			unsupported = true
		}
		if nodeMatches(cause, patternstore.ErrFormatMismatch) || nodeMatches(cause, ErrConfiguration) || nodeMatches(cause, os.ErrPermission) {
			configuration = true
		}
		if nodeMatches(cause, context.Canceled) || nodeMatches(cause, context.DeadlineExceeded) {
			interrupted = true
		}
		if marker, ok := cause.(*control.PatternResolveError); ok {
			if marker == nil {
				markedUnknown = true
			} else {
				switch marker.Kind {
				case control.PatternResolveIntegrity:
					integrity = true
				case control.PatternResolveUnsupported:
					unsupported = true
				case control.PatternResolveConfiguration:
					configuration = true
				case control.PatternResolveAvailability:
					// Ignore this assertion; prove actual whitelist below.
				default:
					markedUnknown = true
				}
			}
		}
		for _, child := range errorChildren(cause) {
			inspect(child, depth+1)
		}
	}
	inspect(err, 0)
	switch {
	case integrity:
		kind = control.PatternResolveIntegrity
	case unsupported:
		kind = control.PatternResolveUnsupported
	case configuration:
		kind = control.PatternResolveConfiguration
	case interrupted, markedUnknown:
		kind = control.PatternResolveUnknown
	case availabilityProof(err, "", 0):
		kind = control.PatternResolveAvailability
	}
	return classified(kind, err)
}

// nodeMatches checks only this node, not an unbounded errors.Is traversal. We
// still honor a conservative local Is assertion for blockers (never for the
// availability whitelist). Standard wrapping is traversed explicitly, including
// multi-Unwrap without depending on Go1.20's errors.Join support.
func nodeMatches(err, target error) bool {
	if err == target {
		return true
	}
	matcher, ok := err.(interface{ Is(error) bool })
	return ok && matcher.Is(target)
}

func errorChildren(err error) []error {
	if multiple, ok := err.(interface{ Unwrap() []error }); ok {
		return multiple.Unwrap()
	}
	if child := errors.Unwrap(err); child != nil {
		return []error{child}
	}
	return nil
}

// Every non-nil parallel branch needs its own whitelist proof. Plain errno,
// Temporary()/Timeout() claims, open/close failures and message substrings are
// not read-phase proof. EINTR/EAGAIN/EWOULDBLOCK/ETIMEDOUT qualify only underneath
// a concrete *os.PathError whose Op is exactly "read". Missing OS paths qualify
// only for explicit "open"/"read" PathErrors; bare ENOENT/ErrNotExist is unknown.
// An inner PathError replaces the outer phase. EIO is not whitelisted.
func availabilityProof(err error, phase string, depth int) bool {
	if err == nil || depth > 64 {
		return false
	}
	if err == patternstore.ErrNotFound || err == patternstore.ErrBusy {
		return true
	}
	if path, ok := err.(*os.PathError); ok && path != nil {
		phase = path.Op
	}
	if err == os.ErrNotExist && (phase == "open" || phase == "read") {
		return true
	}
	if errno, ok := err.(syscall.Errno); ok {
		if (phase == "open" || phase == "read") && errno.Is(os.ErrNotExist) {
			return true
		}
		if phase == "read" && (errno == syscall.EINTR || errno == syscall.EAGAIN || errno == syscall.EWOULDBLOCK || errno == syscall.ETIMEDOUT) {
			return true
		}
	}
	children := errorChildren(err)
	nonNil := false
	for _, child := range children {
		if child == nil {
			continue
		}
		nonNil = true
		if !availabilityProof(child, phase, depth+1) {
			return false
		}
	}
	return nonNil
}
