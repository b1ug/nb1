package patternresolver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/b1ug/nb1/internal/patternsv2"
)

type storeFunc func(string, string) (patternsv2.Record, error)

func (f storeFunc) Resolve(id, revision string) (patternsv2.Record, error) { return f(id, revision) }

type nilStore struct{}

func (s *nilStore) Resolve(string, string) (patternsv2.Record, error) {
	panic("typed nil store was called")
}

func recordFixture(t *testing.T, kind string) patternsv2.Record {
	t.Helper()
	source := patternsv2.Source{Kind: "light_action", Contract: "nb1-light-action/v1", Action: json.RawMessage(`{"kind":"set","color":"red"}`)}
	if kind == "frame" {
		source.Contract = "nb1-light-action/experimental-v2"
		source.Action = json.RawMessage(`{"kind":"orbit","period_ms":200,"segments":4,"cycles":1}`)
	}
	r, err := patternsv2.NewRecord("fixture-"+kind, "fixture", source)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func refFor(r patternsv2.Record) patternidentity.Ref {
	return patternidentity.Ref{RecordContract: r.Contract, ID: r.ID, Revision: r.Revision}
}
func cloneRecord(t *testing.T, r patternsv2.Record) patternsv2.Record {
	t.Helper()
	v, err := patternsv2.NormalizeRecord(r)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func kindOf(t *testing.T, err error) control.PatternResolveErrorKind {
	t.Helper()
	var e *control.PatternResolveError
	if !errors.As(err, &e) || e == nil {
		t.Fatalf("missing classified error: %v", err)
	}
	return e.Kind
}
func zeroResult(t *testing.T, r control.ResolvedPattern) {
	t.Helper()
	if !reflect.DeepEqual(r, control.ResolvedPattern{}) {
		t.Fatal("error returned partial verified execution")
	}
}

// This deliberately untrusted rehash helper is restricted to the ASCII/string/
// integer fixture subset. It proves mutation rejection is not just a stale SHA.
func rehashFixture(t *testing.T, r patternsv2.Record) patternsv2.Record {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&node); err != nil {
		t.Fatal(err)
	}
	delete(node, "revision")
	payload, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	r.Revision = fmt.Sprintf("sha256:%x", sha256.Sum256(append([]byte(patternsv2.RecordContract+"\n"), payload...)))
	return r
}

func TestResolverReverifiesKnownSourcesAndCoreDigest(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			r := recordFixture(t, kind)
			ref := refFor(r)
			calls := 0
			resolver := New(storeFunc(func(id, revision string) (patternsv2.Record, error) {
				calls++
				if id != ref.ID || revision != ref.Revision {
					t.Fatal("not exact Store.Resolve input")
				}
				return r, nil
			}))
			if calls != 0 {
				t.Fatal("constructor called Store")
			}
			out, err := resolver(context.Background(), ref)
			if err != nil {
				t.Fatal(err)
			}
			want, err := patternsv2.ExecutionDigest(r)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || out.Ref != ref || out.ExecutionDigest != want {
				t.Fatalf("binding/calls=%#v %d", out, calls)
			}
			if err := out.Execution.VerifyDigest(want); err != nil {
				t.Fatal(err)
			}
			if kind == "program" {
				r.Compiled.Execution.Program.Steps[0].Color = "#ffffff"
				if out.Execution.Program.Steps[0].Color != "#ff0000" {
					t.Fatal("output aliases Store record")
				}
				out.Execution.Program.Steps[0].Color = "#000000"
			} else {
				r.Compiled.Execution.FrameProgram.Initial[0].Color = "#ffffff"
				r.Compiled.Execution.FrameProgram.Frames[0].Targets[0].Color = "#ffffff"
				if out.Execution.FrameProgram.Initial[0].Color == "#ffffff" || out.Execution.FrameProgram.Frames[0].Targets[0].Color == "#ffffff" {
					t.Fatal("nested frame output aliases Store")
				}
			}
		})
	}
}

func TestResolverRejectsPoisonedInterfaceEvenAfterRehash(t *testing.T) {
	for _, name := range []string{"wrong_id", "wrong_contract", "wrong_returned_revision", "stale_sha", "source_rehashed", "normalized_rehashed", "execution_rehashed", "execution_contract_rehashed", "uppercase_rgb_rehashed", "union", "oversized_source", "invalid_json_source", "unknown_source", "unknown_recipe", "known_wrong_recipe"} {
		t.Run(name, func(t *testing.T) {
			kind := "program"
			if strings.Contains(name, "recipe") {
				kind = "frame"
			}
			r := recordFixture(t, kind)
			originalRef := refFor(r)
			if rehashFixture(t, r).Revision != r.Revision {
				t.Fatal("untrusted rehash helper differs from valid P1 fixture")
			}
			switch name {
			case "wrong_id":
				r.ID = "different"
			case "wrong_contract":
				r.Contract = "wrong/record"
			case "wrong_returned_revision":
				r.Revision = "sha256:" + strings.Repeat("f", 64)
			case "stale_sha":
				r.Title = "changed without SHA"
			case "source_rehashed":
				r.Source.Action = json.RawMessage(`{"kind":"set","color":"blue"}`)
			case "normalized_rehashed":
				r.Compiled.NormalizedAction = json.RawMessage(`{"kind":"set","color":"#0000ff","led":0}`)
			case "execution_rehashed":
				r.Compiled.Execution.Program.Steps[0].Color = "#0000ff"
			case "execution_contract_rehashed":
				r.Compiled.Execution.Contract = "unknown/execution"
			case "uppercase_rgb_rehashed":
				r.Compiled.Execution.Program.Steps[0].Color = "#FF0000"
			case "union":
				r.Compiled.Execution.FrameProgram = &control.FrameProgram{}
			case "oversized_source":
				r.Source.Action = json.RawMessage(strings.Repeat(" ", 65537))
			case "invalid_json_source":
				r.Source.Action = json.RawMessage(`{"kind":"set","kind":"off"}`)
			case "unknown_source":
				r.Source.Contract = "unrecognized/source"
			case "unknown_recipe":
				r.Compiled.RecipeID = "orbit-future/experimental-v99"
			case "known_wrong_recipe":
				r.Compiled.RecipeID = "orbit-paired-linear-8/experimental-v1"
			}
			ref := originalRef
			if strings.Contains(name, "rehashed") || strings.Contains(name, "recipe") || name == "unknown_source" {
				r = rehashFixture(t, r)
				ref = refFor(r)
			}
			out, err := New(storeFunc(func(string, string) (patternsv2.Record, error) { return r, nil }))(context.Background(), ref)
			if err == nil {
				t.Fatal("poisoned interface record accepted")
			}
			zeroResult(t, out)
			want := control.PatternResolveIntegrity
			if name == "unknown_source" || name == "unknown_recipe" {
				want = control.PatternResolveUnsupported
			}
			if got := kindOf(t, err); got != want {
				t.Fatalf("classification=%s want=%s: %v", got, want, err)
			}
			if name == "wrong_id" || name == "wrong_contract" || name == "wrong_returned_revision" {
				if !errors.Is(err, ErrReference) {
					t.Fatal("reference mismatch cause lost")
				}
			}
			if name == "source_rehashed" || name == "execution_rehashed" {
				if !errors.Is(err, patternsv2.ErrInvalid) {
					t.Fatal("semantic source rejection lost")
				}
			}
		})
	}
}

func TestResolverNilConfigurationAndContextNeverCallsStore(t *testing.T) {
	r := recordFixture(t, "program")
	ref := refFor(r)
	var pointer *nilStore
	var function storeFunc
	for _, store := range []Store{nil, pointer, function} {
		out, err := New(store)(context.Background(), ref)
		zeroResult(t, out)
		if !errors.Is(err, ErrConfiguration) || kindOf(t, err) != control.PatternResolveConfiguration {
			t.Fatal(err)
		}
	}
	calls := 0
	resolve := New(storeFunc(func(string, string) (patternsv2.Record, error) { calls++; return r, nil }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := resolve(ctx, ref)
	zeroResult(t, out)
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("cancelled context called Store")
	}
	bad := ref
	bad.Revision = "bad"
	out, err = resolve(context.Background(), bad)
	zeroResult(t, out)
	if kindOf(t, err) != control.PatternResolveIntegrity || !errors.Is(err, patternidentity.ErrInvalid) || calls != 0 {
		t.Fatal("bad ref called Store")
	}
}

func TestResolverChecksCancellationAfterStoreAndPreservesIntegrity(t *testing.T) {
	for _, name := range []string{"valid", "availability", "integrity"} {
		t.Run(name, func(t *testing.T) {
			r := recordFixture(t, "program")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			resolve := New(storeFunc(func(string, string) (patternsv2.Record, error) {
				calls++
				cancel()
				if name == "availability" {
					return patternsv2.Record{}, patternstore.ErrBusy
				}
				if name == "integrity" {
					return patternsv2.Record{}, patternstore.ErrUnsafe
				}
				return r, nil
			}))
			out, err := resolve(ctx, refFor(r))
			zeroResult(t, out)
			if calls != 1 {
				t.Fatal(calls)
			}
			if name == "integrity" {
				if !errors.Is(err, patternstore.ErrUnsafe) || kindOf(t, err) != control.PatternResolveIntegrity {
					t.Fatal("integrity diagnostic was replaced by cancellation")
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatal("context was ignored")
			}
		})
	}
}

type multiError []error

func (e multiError) Error() string   { return "fixture multiple errors" }
func (e multiError) Unwrap() []error { return []error(e) }

type temporaryError struct{}

func (temporaryError) Error() string   { return "claims temporary" }
func (temporaryError) Temporary() bool { return true }
func (temporaryError) Timeout() bool   { return true }

func TestResolverErrorWhitelistAndCausePreservation(t *testing.T) {
	unknown := errors.New("unclassified store failure")
	path := func(op string, errno error) error {
		return &os.PathError{Op: op, Path: "private-fixture/patterns.json", Err: errno}
	}
	for _, c := range []struct {
		name string
		err  error
		kind control.PatternResolveErrorKind
	}{
		{"missing", patternstore.ErrNotFound, control.PatternResolveAvailability},
		{"busy", fmt.Errorf("wrapped: %w", patternstore.ErrBusy), control.PatternResolveAvailability},
		{"read_eintr", path("read", syscall.EINTR), control.PatternResolveAvailability},
		{"read_eagain", path("read", syscall.EAGAIN), control.PatternResolveAvailability},
		{"read_wouldblock", path("read", syscall.EWOULDBLOCK), control.PatternResolveAvailability},
		{"read_timeout", path("read", syscall.ETIMEDOUT), control.PatternResolveAvailability},
		{"missing_open", path("open", os.ErrNotExist), control.PatternResolveAvailability},
		{"missing_read", path("read", syscall.ENOENT), control.PatternResolveAvailability},
		{"missing_close", path("close", syscall.ENOENT), control.PatternResolveUnknown},
		{"missing_rename", path("rename", syscall.ENOENT), control.PatternResolveUnknown},
		{"missing_remove", path("remove", syscall.ENOENT), control.PatternResolveUnknown},
		{"missing_sync", path("sync", syscall.ENOENT), control.PatternResolveUnknown},
		{"bare_enoent", syscall.ENOENT, control.PatternResolveUnknown},
		{"bare_notexist", os.ErrNotExist, control.PatternResolveUnknown},
		{"nested_open_under_read", path("read", path("open", syscall.EINTR)), control.PatternResolveUnknown},
		{"plain_errno", syscall.EINTR, control.PatternResolveUnknown},
		{"open_eintr", path("open", syscall.EINTR), control.PatternResolveUnknown},
		{"close_eintr", path("close", syscall.EINTR), control.PatternResolveUnknown},
		{"read_eio", path("read", syscall.EIO), control.PatternResolveUnknown},
		{"temporary_claim", temporaryError{}, control.PatternResolveUnknown},
		{"unknown", unknown, control.PatternResolveUnknown},
		{"corrupt", patternstore.ErrCorrupt, control.PatternResolveIntegrity},
		{"unsafe", patternstore.ErrUnsafe, control.PatternResolveIntegrity},
		{"wrong_revision", patternstore.ErrRevisionConflict, control.PatternResolveIntegrity},
		{"unsupported", patternsv2.ErrUnsupported, control.PatternResolveUnsupported},
		{"format_config", patternstore.ErrFormatMismatch, control.PatternResolveConfiguration},
		{"permission_config", path("open", os.ErrPermission), control.PatternResolveConfiguration},
		{"context", context.Canceled, control.PatternResolveUnknown},
		{"fake_availability", &control.PatternResolveError{Kind: control.PatternResolveAvailability, Err: unknown}, control.PatternResolveUnknown},
		{"unknown_over_busy", &control.PatternResolveError{Kind: control.PatternResolveUnknown, Err: patternstore.ErrBusy}, control.PatternResolveUnknown},
		{"unsafe_under_availability", &control.PatternResolveError{Kind: control.PatternResolveAvailability, Err: fmt.Errorf("bad: %w", patternstore.ErrUnsafe)}, control.PatternResolveIntegrity},
		{"mixed_busy_unknown", multiError{patternstore.ErrBusy, unknown}, control.PatternResolveUnknown},
		{"mixed_busy_corrupt", multiError{patternstore.ErrBusy, patternstore.ErrCorrupt}, control.PatternResolveIntegrity},
		{"two_known_availability", multiError{patternstore.ErrBusy, patternstore.ErrNotFound}, control.PatternResolveAvailability},
	} {
		t.Run(c.name, func(t *testing.T) {
			classified := classifyStoreError(c.err)
			if kindOf(t, classified) != c.kind {
				t.Fatalf("kind=%s want=%s", classified.Kind, c.kind)
			}
			// Direct causes (including noncomparable multi-errors) stay available by
			// Unwrap; ordinary Is/As behavior is separately checked on single chains.
			if classified.Unwrap() == nil {
				t.Fatal("cause discarded")
			}
			if c.name == "fake_availability" && (!errors.Is(classified, unknown) || classified.Kind != control.PatternResolveUnknown) {
				t.Fatal("unknown outer kind/cause lost")
			}
			if c.name == "read_eagain" {
				var p *os.PathError
				if !errors.As(classified, &p) || p.Op != "read" || !errors.Is(classified, syscall.EAGAIN) {
					t.Fatal("Is/As cause not preserved")
				}
			}
		})
	}
}

func TestResolverRealPrivateStoreAndReopen(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "library")
			s, err := patternstore.NewV2(dir)
			if err != nil {
				t.Fatal(err)
			}
			r := recordFixture(t, kind)
			snapshot, err := patternsv2.EncodeSnapshot([]patternsv2.Record{r})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Import(snapshot); err != nil {
				t.Fatal(err)
			}
			reopened, err := patternstore.NewV2(dir)
			if err != nil {
				t.Fatal(err)
			}
			out, err := New(reopened)(context.Background(), refFor(r))
			if err != nil {
				t.Fatal(err)
			}
			if out.Ref != refFor(r) {
				t.Fatal("real store identity changed")
			}
			if err := out.Execution.VerifyDigest(out.ExecutionDigest); err != nil {
				t.Fatal(err)
			}
			before, err := reopened.Export()
			if err != nil {
				t.Fatal("Resolve retained library lock")
			}
			if !bytes.Equal(before, snapshot) {
				t.Fatal("resolver rewrote library")
			}
		})
	}
}

func TestResolverUnknownOnDiskRemainsReadableButNotExecutable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	s, err := patternstore.NewV2(dir)
	if err != nil {
		t.Fatal(err)
	}
	known := recordFixture(t, "program")
	data, err := patternsv2.EncodeSnapshot([]patternsv2.Record{known})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Import(data); err != nil {
		t.Fatal(err)
	}
	unknownBytes, err := os.ReadFile("../patternsv2/testdata/design/cases/unknown-recipe.record.json")
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := patternsv2.InspectRecord(unknownBytes)
	if err != nil {
		t.Fatal(err)
	}
	// A pre-existing future-version snapshot is readonly by contract. This
	// private fixture writes canonical bytes directly, never bypasses Import on
	// a user library or claims that unknown data is an allowed new import.
	knownJSON, err := patternsv2.EncodeRecord(known)
	if err != nil {
		t.Fatal(err)
	}
	frozen := append([]byte(`{"kind":"`+patternsv2.SnapshotKind+`","records":[`), knownJSON...)
	frozen = append(frozen, ',')
	frozen = append(frozen, inspection.CanonicalJSON()...)
	frozen = append(frozen, []byte(`],"schema_version":2}`)...)
	if _, err := patternsv2.InspectSnapshot(frozen); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "patterns.json"), frozen, 0600); err != nil {
		t.Fatal(err)
	}
	read, err := s.Get(inspection.Record().ID)
	if err != nil || read.Support().Verified {
		t.Fatal("future record is not readable as unknown")
	}
	if _, err := s.Export(); err != nil {
		t.Fatal(err)
	}
	if knownResult, err := New(s)(context.Background(), refFor(known)); err != nil || knownResult.Ref != refFor(known) {
		t.Fatalf("another unknown record blocked known exact resolution: %v", err)
	}
	out, err := New(s)(context.Background(), refFor(inspection.Record()))
	zeroResult(t, out)
	if !errors.Is(err, patternsv2.ErrUnsupported) || kindOf(t, err) != control.PatternResolveUnsupported {
		t.Fatal("unknown inspection became executable")
	}
}

func TestResolverDoesNotCreateMissingStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	s, err := patternstore.NewV2(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := recordFixture(t, "program")
	out, err := New(s)(context.Background(), refFor(r))
	zeroResult(t, out)
	if kindOf(t, err) != control.PatternResolveAvailability || !errors.Is(err, patternstore.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("read resolver created library")
	}
}

func TestResolverOnlyCallsMinimalResolve(t *testing.T) {
	r := recordFixture(t, "program")
	spy := &minimalSpy{r: r}
	resolve := New(spy)
	if atomic.LoadInt32(&spy.calls) != 0 {
		t.Fatal("constructor accessed store")
	}
	if _, err := resolve(context.Background(), refFor(r)); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&spy.calls) != 1 {
		t.Fatal("more than one Resolve per callback")
	}
}

type minimalSpy struct {
	r     patternsv2.Record
	calls int32
}

func (s *minimalSpy) Resolve(string, string) (patternsv2.Record, error) {
	atomic.AddInt32(&s.calls, 1)
	return s.r, nil
}
func (*minimalSpy) Path() string                              { panic("resolver called Path") }
func (*minimalSpy) List() ([]patternsv2.Inspection, error)    { panic("resolver called List") }
func (*minimalSpy) Get(string) (patternsv2.Inspection, error) { panic("resolver called Get") }
func (*minimalSpy) Export() ([]byte, error)                   { panic("resolver called Export") }
