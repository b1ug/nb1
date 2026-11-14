package control

import "github.com/b1ug/nb1/internal/patternidentity"

type recordOrigin uint8

const (
	originLegacy recordOrigin = iota
	originPatternV2
)

// This identity is attached before publication and never edited. It is bounded
// scalar metadata, not an unbounded copy of the author Record or source JSON.
type patternAdmissionIdentity struct {
	ref             patternidentity.Ref
	executionDigest string
	policy          string
}

func (r *record) patternSnapshot() PatternJobEnvelope {
	result := PatternJobEnvelope{PatternRef: r.pattern.ref, ExecutionDigest: r.pattern.executionDigest}
	if r.kind == recordFrames {
		result.JobKind = "frame"
		job := r.frameSnapshot()
		result.FrameJob = &job
	} else {
		result.JobKind = "program"
		job := r.legacySnapshot()
		result.ProgramJob = &job
	}
	return result
}
