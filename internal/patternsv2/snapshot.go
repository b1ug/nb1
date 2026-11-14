package patternsv2

import (
	"encoding/json"
	"sort"
)

// SnapshotInspection can carry unsupported records for explicit read-only
// backup. Strict DecodeSnapshot/EncodeSnapshot accept verified records only.
// It does not imply a store exists, or that any record can run on a device.
type SnapshotInspection struct {
	records   []Inspection
	canonical []byte
}

func (s SnapshotInspection) Records() []Inspection { return append([]Inspection(nil), s.records...) }
func (s SnapshotInspection) CanonicalJSON() []byte { return append([]byte(nil), s.canonical...) }
func (s SnapshotInspection) VerifiedRecords() ([]Record, error) {
	values := make([]Record, 0, len(s.records))
	for _, i := range s.records {
		r, err := i.VerifiedRecord()
		if err != nil {
			return nil, err
		}
		values = append(values, r)
	}
	return values, nil
}

func InspectSnapshot(data []byte) (SnapshotInspection, error) {
	n, err := parse(data)
	if err != nil {
		return SnapshotInspection{}, err
	}
	m, err := object(n, []string{"schema_version", "kind", "records"})
	if err != nil {
		return SnapshotInspection{}, err
	}
	if version, ok := m["schema_version"].(json.Number); !ok || version.String() != "2" {
		return SnapshotInspection{}, invalid("schema_version must be integer 2")
	}
	kind, err := textField(m, "kind")
	if err != nil {
		return SnapshotInspection{}, err
	}
	if kind != SnapshotKind {
		return SnapshotInspection{}, invalid("unsupported snapshot kind")
	}
	input, ok := m["records"].([]interface{})
	if !ok || len(input) > MaxRecords {
		return SnapshotInspection{}, invalid("records must be an array of at most 128 records")
	}
	values := make([]Inspection, 0, len(input))
	seen := make(map[string]bool)
	for _, value := range input {
		r, err := inspectNode(value)
		if err != nil {
			return SnapshotInspection{}, err
		}
		if seen[r.record.ID] {
			return SnapshotInspection{}, invalid("duplicate id %q", r.record.ID)
		}
		seen[r.record.ID] = true
		values = append(values, r)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].record.ID < values[j].record.ID })
	b, err := encodeInspections(values)
	if err != nil {
		return SnapshotInspection{}, err
	}
	return SnapshotInspection{records: values, canonical: b}, nil
}

func DecodeSnapshot(data []byte) ([]Record, error) {
	s, err := InspectSnapshot(data)
	if err != nil {
		return nil, err
	}
	return s.VerifiedRecords()
}

func EncodeSnapshot(records []Record) ([]byte, error) {
	if len(records) > MaxRecords {
		return nil, invalid("snapshot exceeds 128 records")
	}
	values := make([]Inspection, 0, len(records))
	seen := make(map[string]bool)
	for _, r := range records {
		if seen[r.ID] {
			return nil, invalid("duplicate id %q", r.ID)
		}
		seen[r.ID] = true
		b, err := EncodeRecord(r)
		if err != nil {
			return nil, err
		}
		i, err := InspectRecord(b)
		if err != nil {
			return nil, err
		}
		values = append(values, i)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].record.ID < values[j].record.ID })
	return encodeInspections(values)
}

func encodeInspections(values []Inspection) ([]byte, error) {
	array := make([]interface{}, 0, len(values))
	for _, value := range values {
		n, err := parse(value.canonical)
		if err != nil {
			return nil, err
		}
		array = append(array, n)
	}
	return canonical(map[string]interface{}{"schema_version": json.Number("2"), "kind": SnapshotKind, "records": array})
}
