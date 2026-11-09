package control

import "github.com/b1ug/nb1/internal/transporttrace"

// Scope is diagnostic metadata from the admitted record, never from a fresh
// Store lookup. Only the owning worker touches a backend's optional binder.
func transportScope(r *record, phase transporttrace.Phase, pos transporttrace.Position, mode string) transporttrace.Scope {
	s := transporttrace.EmptyScope(phase)
	s.Position, s.Mode = pos, mode
	if r != nil {
		s.JobID, s.Frame = r.state.ID, r.kind == recordFrames
		if r.origin == originPatternV2 {
			s.Pattern = true
			s.PatternID = r.pattern.ref.ID
			s.Revision = r.pattern.ref.Revision
		}
	}
	return s
}
func (w *worker) scopedSet(r *record, phase transporttrace.Phase, pos transporttrace.Position, step Step) error {
	if binder, ok := w.backend.(transporttrace.ScopeBinder); ok {
		binder.SetTransportScope(transportScope(r, phase, pos, ""))
		defer binder.SetTransportScope(transporttrace.EmptyScope(transporttrace.PhaseNone))
	}
	return w.backend.Set(step)
}
func (w *worker) scopedRead(r *record, phase transporttrace.Phase, pos transporttrace.Position) (Observation, error) {
	if binder, ok := w.backend.(transporttrace.ScopeBinder); ok {
		binder.SetTransportScope(transportScope(r, phase, pos, ""))
		defer binder.SetTransportScope(transporttrace.EmptyScope(transporttrace.PhaseNone))
	}
	return w.backend.Read()
}
func (w *worker) scopedStop(r *record, phase transporttrace.Phase, mode string) error {
	if binder, ok := w.backend.(transporttrace.ScopeBinder); ok {
		binder.SetTransportScope(transportScope(r, phase, transporttrace.NoPosition(), mode))
		defer binder.SetTransportScope(transporttrace.EmptyScope(transporttrace.PhaseNone))
	}
	return w.backend.Stop(mode)
}
