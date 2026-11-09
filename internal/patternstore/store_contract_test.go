package patternstore_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/patterns"
	"github.com/b1ug/nb1/internal/patternstore"
)

type contractAuthor struct {
	ID      string           `json:"id"`
	Title   string           `json:"title"`
	Program *control.Program `json:"program,omitempty"`
	Effect  *control.Effect  `json:"effect,omitempty"`
}

func contractPackage(t *testing.T, entries ...contractAuthor) []byte {
	t.Helper()
	if entries == nil {
		entries = []contractAuthor{}
	}
	data, err := json.Marshal(struct {
		Version  int              `json:"schema_version"`
		Patterns []contractAuthor `json:"patterns"`
	}{1, entries})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func contractSteady(id, title, color string) contractAuthor {
	return contractAuthor{ID: id, Title: title, Effect: &control.Effect{Kind: "steady", Color: color}}
}

func contractNew(t *testing.T, dir string) *patternstore.Store {
	t.Helper()
	store, err := patternstore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func contractExport(t *testing.T, store *patternstore.Store) []byte {
	t.Helper()
	data, err := store.Export()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func contractIDs(records []patterns.Record) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	return ids
}

func contractZeroResult(t *testing.T, result patternstore.ImportResult) {
	t.Helper()
	if !reflect.DeepEqual(result, patternstore.ImportResult{}) {
		t.Fatalf("error exposed partial import result: %#v", result)
	}
}

func contractImport(t *testing.T, store *patternstore.Store, data []byte) patternstore.ImportResult {
	t.Helper()
	result, err := store.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStoreContractConstructionAndMissingReadDoNotCreate(t *testing.T) {
	for _, value := range []string{"", "relative", "../other"} {
		if store, err := patternstore.New(value); !errors.Is(err, patternstore.ErrInvalid) || store != nil {
			t.Errorf("New(%q) = %v, %v", value, store, err)
		}
	}
	dir := filepath.Join(t.TempDir(), "absent-parent", "library")
	store := contractNew(t, dir)
	if store.Path() != dir {
		t.Fatalf("Path = %q; want %q", store.Path(), dir)
	}
	assertAbsent := func() {
		t.Helper()
		if _, err := os.Lstat(filepath.Dir(dir)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read or construction created library parent: %v", err)
		}
	}
	assertAbsent()
	listed, err := store.List()
	if err != nil || len(listed) != 0 {
		t.Fatalf("missing List = %#v, %v", listed, err)
	}
	if _, err := store.Get("missing"); !errors.Is(err, patternstore.ErrNotFound) {
		t.Fatalf("missing Get = %v", err)
	}
	if _, err := store.Resolve("missing", "sha256:"+strings.Repeat("a", 64)); !errors.Is(err, patternstore.ErrNotFound) {
		t.Fatalf("missing Resolve = %v", err)
	}
	if got := string(contractExport(t, store)); got != `{"schema_version":1,"kind":"nb1-pattern-snapshot","records":[]}` {
		t.Fatalf("missing Export = %s", got)
	}
	assertAbsent()
}

func TestStoreContractImportPersistSortAndIdempotency(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	store := contractNew(t, dir)
	program := control.Program{Steps: []control.Step{{Color: "#ABCDEF"}}}
	input := contractPackage(t,
		contractSteady("zeta", "中文标题", "#FF0000"),
		contractAuthor{ID: "Alpha", Title: "程序", Program: &program},
		contractSteady("alpha", "大小写独立", "#0000FF"),
	)
	result := contractImport(t, store, input)
	wantInputOrder := []string{"zeta", "Alpha", "alpha"}
	if !reflect.DeepEqual(result.Created, wantInputOrder) || !reflect.DeepEqual(contractIDs(result.Records), wantInputOrder) || len(result.Existing) != 0 || result.Total != 3 {
		t.Fatalf("first result = %#v", result)
	}
	wantSorted := []string{"Alpha", "alpha", "zeta"}
	before := contractExport(t, store)
	listed, err := store.List()
	if err != nil || !reflect.DeepEqual(contractIDs(listed), wantSorted) {
		t.Fatalf("sorted List = %#v, %v", contractIDs(listed), err)
	}
	decoded, err := patterns.DecodePackage(before)
	if err != nil || !reflect.DeepEqual(contractIDs(decoded), wantSorted) {
		t.Fatalf("sorted snapshot = %#v, %v", contractIDs(decoded), err)
	}
	other := contractNew(t, dir)
	if got := contractExport(t, other); !bytes.Equal(got, before) {
		t.Fatal("new Store did not preserve frozen source, program and revision")
	}
	zero := 0
	program.Repeat, program.OnFinish = 1, "off"
	program.Steps[0].Color = "#abcdef"
	canonical := contractPackage(t,
		contractAuthor{ID: "zeta", Title: "中文标题", Effect: &control.Effect{Kind: "steady", Color: "#ff0000", FadeMS: &zero}},
		contractAuthor{ID: "Alpha", Title: "程序", Program: &program},
		contractSteady("alpha", "大小写独立", "#0000ff"),
	)
	result = contractImport(t, other, canonical)
	if len(result.Created) != 0 || !reflect.DeepEqual(result.Existing, wantInputOrder) || result.Total != 3 {
		t.Fatalf("idempotent result = %#v", result)
	}
	if got := contractExport(t, store); !bytes.Equal(got, before) {
		t.Fatal("equivalent import changed library bytes")
	}
	result = contractImport(t, other, before)
	if len(result.Created) != 0 || !reflect.DeepEqual(result.Existing, wantSorted) || result.Total != 3 {
		t.Fatalf("snapshot reimport = %#v", result)
	}
	result = contractImport(t, other, contractPackage(t))
	if len(result.Records) != 0 || len(result.Created) != 0 || len(result.Existing) != 0 || result.Total != 3 || !bytes.Equal(before, contractExport(t, other)) {
		t.Fatal("empty import changed a nonempty library")
	}
}

func TestStoreContractConflictRejectsWholePackage(t *testing.T) {
	store := contractNew(t, filepath.Join(t.TempDir(), "library"))
	contractImport(t, store, contractPackage(t, contractSteady("fixed", "é", "#ff0000")))
	before := contractExport(t, store)
	for name, conflicting := range map[string]contractAuthor{
		"different color":                 contractSteady("fixed", "é", "#0000ff"),
		"different title":                 contractSteady("fixed", "other", "#ff0000"),
		"Unicode sequence":                contractSteady("fixed", "e\u0301", "#ff0000"),
		"same execution different source": {ID: "fixed", Title: "é", Program: &control.Program{Steps: []control.Step{{Color: "#ff0000"}}, Repeat: 1, OnFinish: "hold"}},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := store.Import(contractPackage(t, contractSteady("must-not-appear", "新增", "#00ff00"), conflicting))
			if !errors.Is(err, patternstore.ErrConflict) {
				t.Fatalf("conflict = %v", err)
			}
			contractZeroResult(t, result)
			if !bytes.Equal(before, contractExport(t, store)) {
				t.Fatal("conflict partially changed library")
			}
			if _, err := store.Get("must-not-appear"); !errors.Is(err, patternstore.ErrNotFound) {
				t.Fatalf("partial new entry leaked: %v", err)
			}
		})
	}
}

func TestStoreContractExactReferences(t *testing.T) {
	store := contractNew(t, filepath.Join(t.TempDir(), "library"))
	result := contractImport(t, store, contractPackage(t, contractSteady("Exact", "固定版本", "#123456")))
	record := result.Records[0]
	resolved, err := store.Resolve("Exact", record.Revision)
	if err != nil || !reflect.DeepEqual(resolved, record) {
		t.Fatalf("exact Resolve = %#v, %v", resolved, err)
	}
	for _, revision := range []string{"", "latest", "sha256:", record.Revision[:len(record.Revision)-1], record.Revision + "0", strings.ToUpper(record.Revision), "sha256:" + strings.Repeat("g", 64), "sha256:" + strings.Repeat("a", 63) + "\n"} {
		if _, err := store.Resolve("Exact", revision); !errors.Is(err, patternstore.ErrInvalid) {
			t.Errorf("invalid revision %q = %v", revision, err)
		}
	}
	other := "sha256:" + strings.Repeat("0", 64)
	if other == record.Revision {
		t.Fatal("fixture unexpectedly has all-zero digest")
	}
	if _, err := store.Resolve("Exact", other); !errors.Is(err, patternstore.ErrRevisionConflict) {
		t.Fatalf("old revision = %v", err)
	}
	for _, id := range []string{"exact", "Exac", "Unknown"} {
		if _, err := store.Get(id); !errors.Is(err, patternstore.ErrNotFound) {
			t.Errorf("Get(%q) = %v", id, err)
		}
		if _, err := store.Resolve(id, record.Revision); !errors.Is(err, patternstore.ErrNotFound) {
			t.Errorf("Resolve(%q) = %v", id, err)
		}
	}
	for _, id := range []string{"", ".", "../Exact", "Exact/other", " Exact", "中文", strings.Repeat("a", 65)} {
		if _, err := store.Get(id); !errors.Is(err, patternstore.ErrInvalid) {
			t.Errorf("invalid Get(%q) = %v", id, err)
		}
		if _, err := store.Resolve(id, record.Revision); !errors.Is(err, patternstore.ErrInvalid) {
			t.Errorf("invalid Resolve(%q) = %v", id, err)
		}
	}
}

func contractMutate(record *patterns.Record) {
	record.ID, record.Title, record.Revision = "mutated", "changed", "wrong"
	if len(record.CompiledProgram.Steps) != 0 {
		record.CompiledProgram.Steps[0].Color = "#ffffff"
	}
	if record.Source.Program != nil {
		record.Source.Program.OnFinish = "changed"
		record.Source.Program.Steps[0].Color = "#ffffff"
	}
	if effect := record.Source.Effect; effect != nil {
		effect.Kind, effect.Color = "changed", "#ffffff"
		if effect.FadeMS != nil {
			*effect.FadeMS = 777
		}
		if effect.Cycles != nil {
			*effect.Cycles = 777
		}
		if effect.PeriodMS != nil {
			*effect.PeriodMS = 777
		}
		if effect.OnFinish != nil {
			*effect.OnFinish = "changed"
		}
	}
}

func TestStoreContractReturnedValuesAreIndependent(t *testing.T) {
	store := contractNew(t, filepath.Join(t.TempDir(), "library"))
	program := control.Program{Steps: []control.Step{{Color: "#123456"}}}
	cycles, period := 2, 800
	result := contractImport(t, store, contractPackage(t,
		contractSteady("steady", "steady", "#ff0000"),
		contractAuthor{ID: "program", Title: "program", Program: &program},
		contractAuthor{ID: "pulse", Title: "pulse", Effect: &control.Effect{Kind: "pulse", Color: "#00ff00", Cycles: &cycles, PeriodMS: &period}},
	))
	before := contractExport(t, store)
	for i := range result.Records {
		if record := &result.Records[i]; record.Source.Program != nil {
			expected := record.CompiledProgram.Steps[0].Color
			record.Source.Program.Steps[0].Color = "#ffffff"
			if record.CompiledProgram.Steps[0].Color != expected {
				t.Fatal("source and compiled Program share backing storage")
			}
		}
		contractMutate(&result.Records[i])
	}
	result.Created[0] = "changed"
	listed, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, original := range listed {
		get, err := store.Get(original.ID)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := store.Resolve(original.ID, original.Revision)
		if err != nil {
			t.Fatal(err)
		}
		contractMutate(&get)
		contractMutate(&resolved)
	}
	for i := range listed {
		contractMutate(&listed[i])
	}
	exported := contractExport(t, store)
	exported[0] = 'X'
	if !bytes.Equal(before, contractExport(t, store)) {
		t.Fatal("caller mutation changed saved source, parameters, steps or bytes")
	}
}

func TestStoreContractMergedRecordLimit(t *testing.T) {
	store := contractNew(t, filepath.Join(t.TempDir(), "library"))
	entries := make([]contractAuthor, 0, patterns.MaxRecords)
	for i := 0; i < patterns.MaxRecords; i++ {
		entries = append(entries, contractSteady(fmt.Sprintf("item-%03d", i), "bounded", "#123456"))
	}
	first := contractImport(t, store, contractPackage(t, entries...))
	if first.Total != patterns.MaxRecords {
		t.Fatalf("boundary Total = %d", first.Total)
	}
	before := contractExport(t, store)
	result, err := store.Import(contractPackage(t, contractSteady("extra", "extra", "#123456")))
	if !errors.Is(err, patternstore.ErrLimit) {
		t.Fatalf("merged count overflow = %v", err)
	}
	contractZeroResult(t, result)
	if !bytes.Equal(before, contractExport(t, store)) {
		t.Fatal("count overflow changed existing library")
	}
	if got := contractImport(t, store, contractPackage(t, entries...)); got.Total != patterns.MaxRecords || len(got.Created) != 0 || len(got.Existing) != patterns.MaxRecords {
		t.Fatalf("idempotent full library = %#v", got)
	}
}

func TestStoreContractMergedCanonicalByteLimit(t *testing.T) {
	program := control.Program{Steps: make([]control.Step, control.MaxProgramSteps), Repeat: 1, OnFinish: "off"}
	for i := range program.Steps {
		program.Steps[i].Color = "#123456"
	}
	var entries []contractAuthor
	var frozen []patterns.Record
	for i := 0; i < patterns.MaxRecords; i++ {
		id := fmt.Sprintf("large-%03d", i)
		entries = append(entries, contractAuthor{ID: id, Title: "large", Program: &program})
		record, err := patterns.NewRecord(id, "large", patterns.Source{Program: &program})
		if err != nil {
			t.Fatal(err)
		}
		frozen = append(frozen, record)
		if _, err := patterns.EncodeSnapshot(frozen); err != nil {
			break
		}
	}
	if len(entries) < 2 || len(entries) >= patterns.MaxRecords {
		t.Fatal("fixture did not reach byte budget independently of count budget")
	}
	split := len(entries) / 2
	first, second := contractPackage(t, entries[:split]...), contractPackage(t, entries[split:]...)
	for i, input := range [][]byte{first, second} {
		if _, err := patterns.DecodePackage(input); err != nil {
			t.Fatalf("individual package %d should be valid: %v", i, err)
		}
	}
	store := contractNew(t, filepath.Join(t.TempDir(), "library"))
	contractImport(t, store, first)
	before := contractExport(t, store)
	result, err := store.Import(second)
	if !errors.Is(err, patternstore.ErrLimit) {
		t.Fatalf("merged canonical byte overflow = %v", err)
	}
	contractZeroResult(t, result)
	if !bytes.Equal(before, contractExport(t, store)) {
		t.Fatal("canonical byte overflow changed existing library")
	}
}

func TestStoreContractInvalidImportHasNoFilesystemEffects(t *testing.T) {
	valid := `{"schema_version":1,"patterns":[{"id":"item","title":"title","effect":{"kind":"steady","color":"#ff0000"}}]}`
	cases := map[string][]byte{
		"empty":              nil,
		"unknown field":      []byte(strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"extra":0`, 1)),
		"duplicate key":      []byte(strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1)),
		"escaped duplicate":  []byte(strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_vers\u0069on":1`, 1)),
		"null":               []byte(`{"schema_version":1,"patterns":null}`),
		"mixed package":      []byte(strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"kind":"nb1-pattern-snapshot","records":[]`, 1)),
		"duplicate id":       contractPackage(t, contractSteady("item", "title", "#ff0000"), contractSteady("item", "title", "#ff0000")),
		"bad path id":        contractPackage(t, contractSteady("../item", "title", "#ff0000")),
		"title too long":     contractPackage(t, contractSteady("item", strings.Repeat("字", 257), "#ff0000")),
		"unpaired surrogate": []byte(strings.Replace(valid, `"title":"title"`, `"title":"\ud800"`, 1)),
		"negative zero":      []byte(strings.Replace(valid, `"kind":"steady"`, `"kind":"steady","led":-0`, 1)),
		"trailing JSON":      []byte(valid + `{}`),
		"invalid UTF8":       bytes.Replace([]byte(valid), []byte(`"title":"title"`), []byte{'"', 't', 'i', 't', 'l', 'e', '"', ':', '"', 0xff, '"'}, 1),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "not-created")
			store := contractNew(t, dir)
			result, err := store.Import(data)
			if !errors.Is(err, patternstore.ErrInvalid) {
				t.Fatalf("invalid package = %v", err)
			}
			contractZeroResult(t, result)
			if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid import touched library directory: %v", err)
			}
		})
	}
}

func TestStoreContractCorruptionIsPreservedAndNeverMigrated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	store := contractNew(t, dir)
	result := contractImport(t, store, contractPackage(t, contractSteady("fixed", "title", "#123456")))
	before := contractExport(t, store)
	path := filepath.Join(dir, "patterns.json")
	for name, corrupted := range map[string][]byte{
		"empty file":           {},
		"invalid JSON":         []byte(`{"schema_version":`),
		"noncanonical newline": append(append([]byte{}, before...), '\n'),
		"authoring package":    contractPackage(t, contractSteady("fixed", "title", "#123456")),
		"tampered digest":      bytes.Replace(before, []byte(result.Records[0].Revision), []byte("sha256:"+strings.Repeat("0", 64)), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, corrupted, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.List(); !errors.Is(err, patternstore.ErrCorrupt) {
				t.Errorf("List corrupt = %v", err)
			}
			if _, err := store.Get("fixed"); !errors.Is(err, patternstore.ErrCorrupt) {
				t.Errorf("Get corrupt = %v", err)
			}
			if _, err := store.Resolve("fixed", result.Records[0].Revision); !errors.Is(err, patternstore.ErrCorrupt) {
				t.Errorf("Resolve corrupt = %v", err)
			}
			if _, err := store.Export(); !errors.Is(err, patternstore.ErrCorrupt) {
				t.Errorf("Export corrupt = %v", err)
			}
			failed, err := store.Import(contractPackage(t, contractSteady("new", "title", "#123456")))
			if !errors.Is(err, patternstore.ErrCorrupt) {
				t.Errorf("Import corrupt = %v", err)
			}
			contractZeroResult(t, failed)
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, corrupted) {
				t.Fatalf("corruption was rewritten or reset: %v", err)
			}
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, contractExport(t, store)) {
				t.Fatal("Store did not reread repaired controlled fixture")
			}
		})
	}
}

func TestStoreContractEveryOperationReadsCurrentLibrary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	first, second := contractNew(t, dir), contractNew(t, dir)
	contractImport(t, first, contractPackage(t, contractSteady("first", "first", "#ff0000")))
	contractImport(t, second, contractPackage(t, contractSteady("second", "second", "#0000ff")))
	listed, err := first.List()
	if err != nil || !reflect.DeepEqual(contractIDs(listed), []string{"first", "second"}) {
		t.Fatalf("first Store cached stale content: %#v, %v", contractIDs(listed), err)
	}
	if !bytes.Equal(contractExport(t, first), contractExport(t, second)) {
		t.Fatal("two Store instances disagree after serialized transactions")
	}
}

func TestStoreContractConcurrentImportsDoNotLoseEntries(t *testing.T) {
	store := contractNew(t, filepath.Join(t.TempDir(), "library"))
	const count = 16
	inputs := make([][]byte, count)
	for i := range inputs {
		inputs[i] = contractPackage(t, contractSteady(fmt.Sprintf("item-%02d", i), "concurrent", "#123456"))
	}
	var group sync.WaitGroup
	errorsOut := make(chan error, count)
	for _, input := range inputs {
		input := input
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := store.Import(input)
			if err == nil && (len(result.Created) != 1 || len(result.Existing) != 0) {
				err = fmt.Errorf("unexpected import result: %#v", result)
			}
			errorsOut <- err
		}()
	}
	group.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	listed, err := store.List()
	if err != nil || len(listed) != count {
		t.Fatalf("concurrent List = %d entries, %v", len(listed), err)
	}
}

func TestStoreContractConcurrentConflictingImportsHaveOneWinner(t *testing.T) {
	store := contractNew(t, filepath.Join(t.TempDir(), "library"))
	inputs := [][]byte{
		contractPackage(t, contractSteady("same", "title", "#ff0000")),
		contractPackage(t, contractSteady("same", "title", "#0000ff")),
	}
	start := make(chan struct{})
	errorsOut := make(chan error, len(inputs))
	for _, input := range inputs {
		input := input
		go func() {
			<-start
			result, err := store.Import(input)
			if err != nil && !reflect.DeepEqual(result, patternstore.ImportResult{}) {
				err = fmt.Errorf("failed import exposed result: %#v", result)
			}
			errorsOut <- err
		}()
	}
	close(start)
	winners, conflicts := 0, 0
	for range inputs {
		err := <-errorsOut
		switch {
		case err == nil:
			winners++
		case errors.Is(err, patternstore.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent conflict = %v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
	listed, err := store.List()
	if err != nil || len(listed) != 1 || listed[0].ID != "same" {
		t.Fatalf("unexpected winner library: %#v, %v", listed, err)
	}
}
