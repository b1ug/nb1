package main

import (
	"context"
	"encoding/json"
	"errors"
)

type patternAuthorLibrary struct {
	Path            string `json:"path"`
	State           string `json:"state"`
	CreateOnly      bool   `json:"create_only"`
	MaxRecords      int    `json:"max_records"`
	MaxPackageBytes int    `json:"max_package_bytes"`
	RecordContract  string `json:"record_contract"`
	SnapshotKind    string `json:"snapshot_kind"`
}
type patternAuthorSummary struct {
	PatternRef        patternV2Ref         `json:"pattern_ref"`
	Title             string               `json:"title"`
	SourceContract    string               `json:"source_contract"`
	ExecutionKind     string               `json:"execution_kind"`
	ExecutionContract string               `json:"execution_contract"`
	Verification      patternAuthorSupport `json:"verification"`
	ExecutionDigest   string               `json:"execution_digest"`
}
type patternAuthorListData struct {
	SchemaVersion    int                    `json:"schema_version"`
	Contract         string                 `json:"contract"`
	Library          patternAuthorLibrary   `json:"library"`
	PatternExecution frameExecutionWire     `json:"pattern_execution"`
	Patterns         []patternAuthorSummary `json:"patterns"`
}

func patternAuthorValidateList(value patternAuthorListData) error {
	l := value.Library
	if value.SchemaVersion != 2 || value.Contract != patternAuthorLibraryContract || !l.CreateOnly || l.MaxRecords != 128 || l.MaxPackageBytes != 1<<20 || l.RecordContract != patternV2RecordContract || l.SnapshotKind != patternAuthorSnapshotKind || !patternAuthorDisplayPath(l.Path) || value.Patterns == nil || len(value.Patterns) > 128 {
		return errors.New("invalid Pattern library metadata/limits")
	}
	if err := patternAuthorAvailability(value.PatternExecution); err != nil {
		return err
	}
	readonly := false
	for index, row := range value.Patterns {
		if !validPatternV2Ref(row.PatternRef) || !patternRevision.MatchString(row.ExecutionDigest) || !patternAuthorIdentity(row.PatternRef.ID, row.Title) || !patternAuthorToken(row.SourceContract) || index > 0 && value.Patterns[index-1].PatternRef.ID >= row.PatternRef.ID {
			return errors.New("invalid or unsorted Pattern advertisement")
		}
		if !(row.ExecutionKind == "program" && row.ExecutionContract == compileContract || row.ExecutionKind == "frame_program" && row.ExecutionContract == frameCompileContract) {
			return errors.New("unsupported Pattern advertisement execution")
		}
		v1, v2 := row.SourceContract == lightContract, row.SourceContract == orbitLightContract
		if row.Verification.Verified {
			if row.Verification.Reason != "verified" || !(v1 && row.ExecutionKind == "program" || v2 && row.ExecutionKind == "frame_program") {
				return errors.New("contradictory advertised verification")
			}
		} else {
			readonly = true
			if !(row.Verification.Reason == "unsupported_source" && !v1 && !v2 || row.Verification.Reason == "unsupported_recipe" && v2 && row.ExecutionKind == "frame_program") {
				return errors.New("contradictory read-only advertisement")
			}
		}
	}
	if !(readonly && l.State == "read_only" || !readonly && l.State == "ready") {
		return errors.New("library state contradicts advertised verification")
	}
	return nil
}
func (d *daemon) patternAuthorList(ctx context.Context, raw json.RawMessage) patternAuthorReadOutput {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var empty struct{}
	if err := patternAuthorDecode(raw, &empty, 4096, 32); err != nil {
		return patternAuthorReadError("invalid_request", err.Error(), 0)
	}
	response := d.patternAuthorHTTP(ctx, "GET", "/v2/patterns", nil, false)
	if response.problem != nil {
		return patternAuthorReadOutput{Kind: "error", Error: response.problem}
	}
	var value patternAuthorListData
	err := patternAuthorDecode(response.body, &value, maxPatternAuthorResponse, 64)
	if response.status != 200 {
		err = errors.New("list requires HTTP200")
	}
	if err == nil {
		err = patternAuthorValidateList(value)
	}
	if err != nil {
		return patternAuthorReadError("invalid_daemon_response", err.Error(), response.status)
	}
	return patternAuthorReadOutput{Kind: "pattern_library_v2", Data: value}
}
