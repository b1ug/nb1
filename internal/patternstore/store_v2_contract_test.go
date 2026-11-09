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

	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/lightactionv2"
	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/b1ug/nb1/internal/patternsv2"
)

func v2Store(t *testing.T, dir string) *patternstore.StoreV2 {
	t.Helper()
	s, err := patternstore.NewV2(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func v2Record(t *testing.T, id, title, contract, action string) patternsv2.Record {
	t.Helper()
	r, err := patternsv2.NewRecord(id, title, patternsv2.Source{Kind: "light_action", Contract: contract, Action: []byte(action)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func v2Off(t *testing.T, id string) patternsv2.Record {
	return v2Record(t, id, "关灯", lightaction.Contract, `{"kind":"off"}`)
}
func v2Snapshot(t *testing.T, records ...patternsv2.Record) []byte {
	t.Helper()
	b, err := patternsv2.EncodeSnapshot(records)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func v2Export(t *testing.T, s *patternstore.StoreV2) []byte {
	t.Helper()
	b, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func v2Import(t *testing.T, s *patternstore.StoreV2, data []byte) patternstore.ImportResultV2 {
	t.Helper()
	r, err := s.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func v2Zero(t *testing.T, r patternstore.ImportResultV2) {
	t.Helper()
	if !reflect.DeepEqual(r, patternstore.ImportResultV2{}) {
		t.Fatalf("partial import result: %+v", r)
	}
}

func TestV2ConstructionAndMissingReadNeverCreate(t *testing.T) {
	for _, dir := range []string{"", "relative", "../parent"} {
		if s, err := patternstore.NewV2(dir); s != nil || !errors.Is(err, patternstore.ErrInvalid) {
			t.Fatalf("invalid directory: %v %v", s, err)
		}
	}
	var nilStore *patternstore.StoreV2
	if nilStore.Path() != "" {
		t.Fatal("nil path")
	}
	if _, err := nilStore.List(); !errors.Is(err, patternstore.ErrInvalid) {
		t.Fatalf("nil list: %v", err)
	}
	legacy, err := patternstore.DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	current, err := patternstore.DefaultV2Dir()
	if err != nil || current == legacy || filepath.Base(current) != "patterns-v2" || filepath.Dir(current) != filepath.Dir(legacy) {
		t.Fatalf("default path: %s %v", current, err)
	}
	dir := filepath.Join(t.TempDir(), "absent", "library")
	s := v2Store(t, dir)
	if s.Path() != dir {
		t.Fatal("path changed")
	}
	items, err := s.List()
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("missing list: %v %v", items, err)
	}
	if _, err := s.Get("missing"); !errors.Is(err, patternstore.ErrNotFound) {
		t.Fatalf("missing get: %v", err)
	}
	if _, err := s.Resolve("missing", "sha256:"+strings.Repeat("0", 64)); !errors.Is(err, patternstore.ErrNotFound) {
		t.Fatalf("missing resolve: %v", err)
	}
	if !bytes.Equal(v2Export(t, s), v2Snapshot(t)) {
		t.Fatal("missing export differs")
	}
	if _, err := os.Lstat(filepath.Dir(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created absent parent: %v", err)
	}
}

func TestV2RealPersistenceExactResolveAndIdempotency(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	s := v2Store(t, dir)
	orbit := v2Record(t, "z-orbit", "红蓝环绕", lightactionv2.Contract, `{"kind":"orbit","colors":["red","blue"],"cycles":3}`)
	pulse := v2Record(t, "a-pulse", "绿色脉冲", lightaction.Contract, `{"kind":"pulse","color":"lime"}`)
	result := v2Import(t, s, v2Snapshot(t, orbit, pulse))
	if result.Total != 2 || !reflect.DeepEqual(result.Created, []string{"a-pulse", "z-orbit"}) || len(result.Existing) != 0 {
		t.Fatalf("first import: %+v", result)
	}
	exported := v2Export(t, s)
	if !bytes.Equal(exported, v2Snapshot(t, pulse, orbit)) {
		t.Fatal("disk format was not canonical sorted snapshot")
	}
	before, err := os.Stat(filepath.Join(dir, "patterns.json"))
	if err != nil {
		t.Fatal(err)
	}
	reopened := v2Store(t, dir)
	if !bytes.Equal(v2Export(t, reopened), exported) {
		t.Fatal("reopening changed bytes")
	}
	listed, err := reopened.List()
	if err != nil || len(listed) != 2 || listed[0].Record().ID != "a-pulse" || !listed[1].Support().Verified {
		t.Fatalf("list: %+v %v", listed, err)
	}
	for _, record := range []patternsv2.Record{pulse, orbit} {
		got, err := reopened.Resolve(record.ID, record.Revision)
		if err != nil || !reflect.DeepEqual(got, record) {
			t.Fatalf("exact resolve: %+v %v", got, err)
		}
		if _, err := reopened.Resolve(record.ID, "latest"); !errors.Is(err, patternstore.ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := reopened.Resolve(record.ID, "sha256:"+strings.Repeat("0", 64)); !errors.Is(err, patternstore.ErrRevisionConflict) {
			t.Fatal(err)
		}
	}
	result = v2Import(t, reopened, exported)
	if len(result.Created) != 0 || !reflect.DeepEqual(result.Existing, []string{"a-pulse", "z-orbit"}) {
		t.Fatalf("idempotent result: %+v", result)
	}
	result = v2Import(t, reopened, v2Snapshot(t))
	if result.Total != 2 || len(result.Records)+len(result.Existing)+len(result.Created) != 0 {
		t.Fatalf("empty import: %+v", result)
	}
	after, err := os.Stat(filepath.Join(dir, "patterns.json"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("no-op import republished inode: %v", err)
	}
}

func TestV2CreateOnlyConflictPreservesWholeSnapshot(t *testing.T) {
	s := v2Store(t, filepath.Join(t.TempDir(), "library"))
	initial := v2Record(t, "fixed", "环绕", lightactionv2.Contract, `{"kind":"orbit","colors":["red","blue"]}`)
	v2Import(t, s, v2Snapshot(t, initial))
	before := v2Export(t, s)
	for _, replacement := range []patternsv2.Record{
		v2Record(t, "fixed", "标题变化", lightactionv2.Contract, `{"kind":"orbit","colors":["red","blue"]}`),
		v2Record(t, "fixed", "环绕", lightactionv2.Contract, `{"kind":"orbit","colors":["#ff0000","#0000ff"]}`),
		v2Record(t, "fixed", "环绕", lightactionv2.Contract, `{"kind":"orbit","colors":["green","blue"]}`),
	} {
		r, err := s.Import(v2Snapshot(t, v2Off(t, "new"), replacement))
		if !errors.Is(err, patternstore.ErrConflict) {
			t.Fatalf("conflict: %v", err)
		}
		v2Zero(t, r)
		if !bytes.Equal(before, v2Export(t, s)) {
			t.Fatal("conflict published partial package")
		}
		if _, err := s.Get("new"); !errors.Is(err, patternstore.ErrNotFound) {
			t.Fatalf("partial record: %v", err)
		}
	}
}

func TestV2AndV1SameDirectoryRejectWithoutOverwriting(t *testing.T) {
	t.Run("v1-first", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "library")
		v1 := contractNew(t, dir)
		contractImport(t, v1, contractPackage(t, contractSteady("legacy", "旧图案", "#00ff00")))
		before := contractExport(t, v1)
		v2 := v2Store(t, dir)
		if _, err := v2.List(); !errors.Is(err, patternstore.ErrFormatMismatch) {
			t.Fatalf("wrong format read: %v", err)
		}
		r, err := v2.Import(v2Snapshot(t, v2Off(t, "new")))
		if !errors.Is(err, patternstore.ErrFormatMismatch) {
			t.Fatalf("wrong format write: %v", err)
		}
		v2Zero(t, r)
		if !bytes.Equal(contractExport(t, v1), before) {
			t.Fatal("v2 changed v1 bytes")
		}
	})
	t.Run("v2-first", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "library")
		v2 := v2Store(t, dir)
		v2Import(t, v2, v2Snapshot(t, v2Off(t, "new")))
		before := v2Export(t, v2)
		v1 := contractNew(t, dir)
		if _, err := v1.List(); !errors.Is(err, patternstore.ErrCorrupt) {
			t.Fatalf("legacy decoder unexpectedly read v2: %v", err)
		}
		r, err := v1.Import(contractPackage(t, contractSteady("legacy", "旧图案", "#00ff00")))
		if !errors.Is(err, patternstore.ErrCorrupt) {
			t.Fatalf("legacy decoder unexpectedly wrote v2: %v", err)
		}
		contractZeroResult(t, r)
		if !bytes.Equal(v2Export(t, v2), before) {
			t.Fatal("v1 changed v2 bytes")
		}
	})
}

func v2UnknownSnapshot(t *testing.T, known patternsv2.Record) ([]byte, patternsv2.Record) {
	t.Helper()
	unknown, err := os.ReadFile("../patternsv2/testdata/design/cases/unknown-recipe.record.json")
	if err != nil {
		t.Fatal(err)
	}
	i, err := patternsv2.InspectRecord(unknown)
	if err != nil {
		t.Fatal(err)
	}
	knownJSON, err := patternsv2.EncodeRecord(known)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]interface{}{"schema_version": 2, "kind": patternsv2.SnapshotKind, "records": []json.RawMessage{unknown, knownJSON}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := patternsv2.InspectSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.CanonicalJSON(), i.Record()
}

func TestV2UnknownRecipeReadExportResolveAndRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	s := v2Store(t, dir)
	known := v2Off(t, "known")
	v2Import(t, s, v2Snapshot(t, known))
	unknownBytes, unknown := v2UnknownSnapshot(t, known)
	path := filepath.Join(dir, "patterns.json")
	if err := os.WriteFile(path, unknownBytes, 0600); err != nil {
		t.Fatal(err)
	}
	listed, err := s.List()
	if err != nil || len(listed) != 2 {
		t.Fatalf("unknown list: %v %v", listed, err)
	}
	i, err := s.Get(unknown.ID)
	if err != nil || i.Support().Verified || i.Support().Reason != "unsupported_recipe" {
		t.Fatalf("unknown get: %+v %v", i.Support(), err)
	}
	if !bytes.Equal(v2Export(t, s), unknownBytes) {
		t.Fatal("unknown export changed bytes")
	}
	if got, err := s.Resolve(known.ID, known.Revision); err != nil || got.Revision != known.Revision {
		t.Fatalf("known resolution blocked by another unknown: %v", err)
	}
	if _, err := s.Resolve(unknown.ID, unknown.Revision); !errors.Is(err, patternsv2.ErrUnsupported) {
		t.Fatalf("unknown resolved: %v", err)
	}
	for _, data := range [][]byte{v2Snapshot(t), v2Snapshot(t, known), v2Snapshot(t, v2Off(t, "new"))} {
		r, err := s.Import(data)
		if !errors.Is(err, patternstore.ErrReadOnly) || errors.Is(err, patternstore.ErrCorrupt) {
			t.Fatalf("unknown write: %v", err)
		}
		v2Zero(t, r)
		if !bytes.Equal(v2Export(t, s), unknownBytes) {
			t.Fatal("read-only write changed disk")
		}
	}
	// StoreV2 must inspect each new transaction instead of caching old support.
	if err := os.WriteFile(path, v2Snapshot(t, known), 0600); err != nil {
		t.Fatal(err)
	}
	v2Import(t, s, v2Snapshot(t, v2Off(t, "new")))
	if listed, err := s.List(); err != nil || len(listed) != 2 {
		t.Fatalf("fresh snapshot not reread: %v %v", listed, err)
	}
}

func TestV2InvalidDiskSnapshotsReturnNoPartialData(t *testing.T) {
	for _, corruption := range []func([]byte) []byte{
		func(data []byte) []byte { return append(data, '\n') },
		func([]byte) []byte {
			return []byte(`{"schema_version":2,"kind":"nb1-pattern-snapshot/experimental-v2","records":[]`)
		},
		func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"revision":"sha256:`), []byte(`"revision":"sha256:0`), 1)
		},
	} {
		dir := filepath.Join(t.TempDir(), "library")
		s := v2Store(t, dir)
		v2Import(t, s, v2Snapshot(t, v2Off(t, "known")))
		bad := corruption(v2Export(t, s))
		path := filepath.Join(dir, "patterns.json")
		if err := os.WriteFile(path, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if data, err := s.List(); data != nil || !errors.Is(err, patternstore.ErrCorrupt) {
			t.Fatalf("corrupt list: %v %v", data, err)
		}
		if data, err := s.Export(); data != nil || !errors.Is(err, patternstore.ErrCorrupt) {
			t.Fatalf("corrupt export: %s %v", data, err)
		}
		if got, err := s.Get("known"); got.Record().ID != "" || !errors.Is(err, patternstore.ErrCorrupt) {
			t.Fatalf("corrupt get: %v", err)
		}
		r, err := s.Import(v2Snapshot(t, v2Off(t, "new")))
		if !errors.Is(err, patternstore.ErrCorrupt) {
			t.Fatalf("corrupt import: %v", err)
		}
		v2Zero(t, r)
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, bad) {
			t.Fatal("corrupt library was overwritten", err)
		}
	}
}

func TestV2ConcurrentImportsAndCopyIsolation(t *testing.T) {
	s := v2Store(t, filepath.Join(t.TempDir(), "library"))
	const count = 10
	inputs := make([][]byte, count)
	for n := range inputs {
		inputs[n] = v2Snapshot(t, v2Off(t, fmt.Sprintf("item-%02d", n)))
	}
	var wg sync.WaitGroup
	failures := make(chan error, count)
	for _, input := range inputs {
		wg.Add(1)
		go func(data []byte) { defer wg.Done(); _, err := s.Import(data); failures <- err }(input)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.List()
	if err != nil || len(items) != count {
		t.Fatalf("lost concurrent record: %d %v", len(items), err)
	}
	before := v2Export(t, s)
	first := items[0].Record()
	got, err := s.Resolve(first.ID, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	got.Source.Action[0] = '!'
	got.Compiled.NormalizedAction[0] = '!'
	got.Compiled.Execution.Program.Steps[0].Color = "#ffffff"
	copy := items[0].Record()
	copy.Source.Action[0] = '!'
	copy.Compiled.Execution.Program.Steps[0].Color = "#ffffff"
	result := v2Import(t, s, inputs[0])
	result.Records[0].Source.Action[0] = '!'
	result.Records[0].Compiled.Execution.Program.Steps[0].Color = "#ffffff"
	input := inputs[0]
	input[0] = '!'
	if !bytes.Equal(before, v2Export(t, s)) {
		t.Fatal("caller mutation changed store")
	}
	if items[0].Record().Compiled.Execution.Program.Steps[0].Color != "#000000" {
		t.Fatal("Inspection alias changed stored copy")
	}
}
