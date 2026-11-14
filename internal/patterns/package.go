package patterns

import (
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/jsoninput"
)

type authoringRecord struct {
	ID      string           `json:"id"`
	Title   string           `json:"title"`
	Program *control.Program `json:"program,omitempty"`
	Effect  *control.Effect  `json:"effect,omitempty"`
}

// Pointer slices preserve the difference between an absent discriminator and a
// present empty array. jsoninput independently rejects explicit null.
type packageWire struct {
	SchemaVersion *int               `json:"schema_version"`
	Kind          *string            `json:"kind,omitempty"`
	Patterns      *[]authoringRecord `json:"patterns,omitempty"`
	Records       *[]Record          `json:"records,omitempty"`
}

// DecodePackage accepts one complete authoring or frozen package. Errors never
// expose a partially accepted slice; the canonical full export is also bounded.
func DecodePackage(data []byte) ([]Record, error) {
	if len(data) > MaxPackageBytes {
		return nil, invalid("package exceeds 1 MiB")
	}
	if err := strictLexemes(data); err != nil {
		return nil, err
	}
	var input packageWire
	if err := jsoninput.DecodeObject(data, &input); err != nil {
		return nil, invalid("package JSON: %v", err)
	}
	if input.SchemaVersion == nil || *input.SchemaVersion != SchemaVersion {
		return nil, invalid("schema_version must be 1")
	}
	var records []Record
	switch {
	case input.Patterns != nil && input.Kind == nil && input.Records == nil:
		if len(*input.Patterns) > MaxRecords {
			return nil, invalid("package exceeds 128 records")
		}
		records = make([]Record, 0, len(*input.Patterns))
		seen := make(map[string]bool)
		for _, item := range *input.Patterns {
			if seen[item.ID] {
				return nil, invalid("duplicate id %q", item.ID)
			}
			seen[item.ID] = true
			value, err := NewRecord(item.ID, item.Title, Source{Program: item.Program, Effect: item.Effect})
			if err != nil {
				return nil, err
			}
			records = append(records, value)
		}
	case input.Patterns == nil && input.Kind != nil && *input.Kind == SnapshotKind && input.Records != nil:
		values, err := normalizeRecords(*input.Records)
		if err != nil {
			return nil, err
		}
		records = values
	default:
		return nil, invalid("expected schema_version/patterns or schema_version/kind/records without mixed fields")
	}
	if _, err := encodeSnapshot(records); err != nil {
		return nil, err
	}
	return records, nil
}

// EncodeSnapshot validates every record and returns the exact frozen wire
// bytes. Caller order is retained, input values are not mutated, and nil means
// an empty library rather than a JSON null array.
func EncodeSnapshot(records []Record) ([]byte, error) {
	values, err := normalizeRecords(records)
	if err != nil {
		return nil, err
	}
	return encodeSnapshot(values)
}

func normalizeRecords(records []Record) ([]Record, error) {
	if len(records) > MaxRecords {
		return nil, invalid("package exceeds 128 records")
	}
	values := make([]Record, 0, len(records))
	seen := make(map[string]bool)
	for _, item := range records {
		if seen[item.ID] {
			return nil, invalid("duplicate id %q", item.ID)
		}
		seen[item.ID] = true
		value, err := NormalizeRecord(item)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}
