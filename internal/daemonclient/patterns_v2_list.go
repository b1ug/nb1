package daemonclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/lightactionv2"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternsv2"
)

// PatternLibraryInfoV2 describes the daemon's selected library. Path is display
// metadata only, including when the daemon runs on a different OS.
type PatternLibraryInfoV2 struct {
	Path            string `json:"path"`
	State           string `json:"state"`
	CreateOnly      bool   `json:"create_only"`
	MaxRecords      int    `json:"max_records"`
	MaxPackageBytes int    `json:"max_package_bytes"`
	RecordContract  string `json:"record_contract"`
	SnapshotKind    string `json:"snapshot_kind"`
}

// PatternSummaryV2 contains advertised metadata, not a complete verified Record.
// Use GetPatternV2 for independent source/recipe/revision/digest verification.
type PatternSummaryV2 struct {
	PatternRef        patternidentity.Ref `json:"pattern_ref"`
	Title             string              `json:"title"`
	SourceContract    string              `json:"source_contract"`
	ExecutionKind     string              `json:"execution_kind"`
	ExecutionContract string              `json:"execution_contract"`
	Verification      patternsv2.Support  `json:"verification"`
	ExecutionDigest   string              `json:"execution_digest"`
}

type PatternListV2 struct {
	SchemaVersion    int                            `json:"schema_version"`
	Contract         string                         `json:"contract"`
	Library          PatternLibraryInfoV2           `json:"library"`
	PatternExecution PatternExecutionAvailabilityV2 `json:"pattern_execution"`
	Patterns         []PatternSummaryV2             `json:"patterns"`
}

// ListPatternsV2 performs one bounded GET, without per-record fetches, tickets,
// preview, device work or local filesystem access. Unknown summaries stay visible
// and read-only; advertised verification is never promoted to execution consent.
func (c *Client) ListPatternsV2(ctx context.Context) (PatternListV2, error) {
	var value PatternListV2
	headers, err := c.doLightResponse(ctx, http.MethodGet, "/v2/patterns", nil, &value, http.StatusOK)
	if err != nil {
		return PatternListV2{}, err
	}
	if err = validatePatternListV2(value); err == nil {
		err = patternV2ReadHeaders(headers)
	}
	if err != nil {
		return PatternListV2{}, patternDecode(err)
	}
	return value, nil
}

func validatePatternListV2(value PatternListV2) error {
	library := value.Library
	if value.SchemaVersion != patternsv2.SchemaVersion || value.Contract != httpapi.PatternLibraryV2Contract ||
		!library.CreateOnly || library.MaxRecords != patternsv2.MaxRecords || library.MaxPackageBytes != patternsv2.MaxBytes ||
		library.RecordContract != patternsv2.RecordContract || library.SnapshotKind != patternsv2.SnapshotKind ||
		!patternDisplayPathV2(library.Path) || value.Patterns == nil || len(value.Patterns) > patternsv2.MaxRecords {
		return fmt.Errorf("invalid Pattern v2 library metadata or limits")
	}
	if err := validatePatternAvailabilityV2(value.PatternExecution); err != nil {
		return err
	}
	readOnly := false
	for index, summary := range value.Patterns {
		if patternidentity.ValidateRef(summary.PatternRef) != nil || patternidentity.ValidateDigest(summary.ExecutionDigest) != nil ||
			index > 0 && value.Patterns[index-1].PatternRef.ID >= summary.PatternRef.ID ||
			len(summary.Title) < 1 || len(summary.Title) > 1024 || !utf8.ValidString(summary.Title) || utf8.RuneCountInString(summary.Title) > 256 ||
			!patternSourceTokenV2(summary.SourceContract) {
			return fmt.Errorf("invalid or unsorted Pattern v2 summary")
		}
		switch summary.ExecutionKind {
		case "program":
			if summary.ExecutionContract != control.CompileContract {
				return fmt.Errorf("Pattern summary program contract mismatch")
			}
		case "frame_program":
			if summary.ExecutionContract != control.FrameContract {
				return fmt.Errorf("Pattern summary frame contract mismatch")
			}
		default:
			return fmt.Errorf("unsupported Pattern summary execution kind")
		}
		knownV1 := summary.SourceContract == lightaction.Contract
		knownV2 := summary.SourceContract == lightactionv2.Contract
		if summary.Verification.Verified {
			if summary.Verification.Reason != "verified" ||
				!(knownV1 && summary.ExecutionKind == "program" || knownV2 && summary.ExecutionKind == "frame_program") {
				return fmt.Errorf("contradictory Pattern summary verification")
			}
		} else {
			readOnly = true
			if !(summary.Verification.Reason == "unsupported_source" && !knownV1 && !knownV2 ||
				summary.Verification.Reason == "unsupported_recipe" && knownV2 && summary.ExecutionKind == "frame_program") {
				return fmt.Errorf("contradictory read-only Pattern summary")
			}
		}
	}
	if !(readOnly && library.State == "read_only" || !readOnly && library.State == "ready") {
		return fmt.Errorf("Pattern library state contradicts advertised summary verification")
	}
	return nil
}

// Path syntax belongs to the daemon OS, not the client's filepath package.
// Accept POSIX, Windows drive-rooted and complete UNC metadata on either host.
func patternDisplayPathV2(path string) bool {
	if strings.HasPrefix(path, "/") {
		return true
	}
	if len(path) >= 3 && (path[0] >= 'a' && path[0] <= 'z' || path[0] >= 'A' && path[0] <= 'Z') && path[1] == ':' && (path[2] == '/' || path[2] == '\\') {
		return true
	}
	if strings.HasPrefix(path, `\\`) {
		parts := strings.SplitN(path[2:], `\`, 3)
		return len(parts) >= 2 && parts[0] != "" && parts[1] != ""
	}
	return false
}

// Matches the Record codec's bounded source-contract alphabet, without
// interpreting a future source or fetching full records from a listing.
func patternSourceTokenV2(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '/' || char == '.') {
			return false
		}
	}
	return true
}
