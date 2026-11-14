package control_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/lightactionv2"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternsv2"
)

func patternExecutionFromP1(t *testing.T, r patternsv2.Record) control.PatternExecution {
	t.Helper()
	e := r.Compiled.Execution
	contract := control.CompileContract
	if e.Kind == "frame_program" {
		contract = control.FrameContract
	}
	if e.Contract != contract {
		t.Fatal("adapter cannot discard a mismatched P1 execution contract")
	}
	return control.PatternExecution{Kind: e.Kind, Program: e.Program, FrameProgram: e.FrameProgram}
}

func assertPatternP1Bytes(t *testing.T, r patternsv2.Record) string {
	t.Helper()
	// Extract the exact canonical subtree as bytes. Never decode numeric values
	// into float64 and never use struct field order as the reference encoder.
	wire, err := patternsv2.EncodeRecord(r)
	if err != nil {
		t.Fatal(err)
	}
	var record, compiled map[string]json.RawMessage
	if err := json.Unmarshal(wire, &record); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(record["compiled"], &compiled); err != nil {
		t.Fatal(err)
	}
	e := patternExecutionFromP1(t, r)
	canonical, err := e.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, compiled["execution"]) {
		t.Fatalf("typed canonical differs byte-for-byte from P1: %v", err)
	}
	digest, err := e.Digest()
	if err != nil {
		t.Fatal(err)
	}
	p1, err := patternsv2.ExecutionDigest(r)
	if err != nil {
		t.Fatal(err)
	}
	i, err := patternsv2.InspectRecord(wire)
	if err != nil || digest != p1 || digest != i.ExecutionDigest() || e.VerifyDigest(p1) != nil {
		t.Fatal("cross-layer digest mismatch", digest, p1, err)
	}
	return digest
}

func TestPatternExecutionDynamicP1Sources(t *testing.T) {
	for _, action := range []string{
		`{"kind":"set","color":"red"}`, `{"kind":"off"}`, `{"kind":"fade","color":"blue","duration_ms":10,"led":1}`,
		`{"kind":"blink","color":"lime","cycles":2}`, `{"kind":"breathe","color":"white"}`, `{"kind":"pulse","color":"lime"}`,
		`{"kind":"alternate","colors":["red","blue"]}`, `{"kind":"rainbow"}`,
		`{"kind":"fade","color":"blue","duration_ms":600000,"led":2,"on_finish":"hold"}`,
	} {
		t.Run(action, func(t *testing.T) {
			r, err := patternsv2.NewRecord("source", "高级来源", patternsv2.Source{Kind: "light_action", Contract: lightaction.Contract, Action: []byte(action)})
			if err != nil {
				t.Fatal(err)
			}
			assertPatternP1Bytes(t, r)
		})
	}
	for _, segments := range []int{4, 8} {
		for _, leds := range []string{"[1,2]", "[2,1]"} {
			for _, period := range []int{3000, 3001} {
				for _, finish := range []string{"off", "hold"} {
					action := fmt.Sprintf(`{"kind":"orbit","segments":%d,"leds":%s,"period_ms":%d,"cycles":2,"on_finish":%q}`, segments, leds, period, finish)
					t.Run(action, func(t *testing.T) {
						r, err := patternsv2.NewRecord("orbit", "环绕", patternsv2.Source{Kind: "light_action", Contract: lightactionv2.Contract, Action: []byte(action)})
						if err != nil {
							t.Fatal(err)
						}
						assertPatternP1Bytes(t, r)
					})
				}
			}
		}
	}
	var previous patternsv2.Record
	var sameDigest string
	for _, color := range []string{`"red"`, `"#ff0000"`, `{"space":"rgb8","r":255,"g":0,"b":0}`, `{"space":"hsv","h_deg":0.000000,"s_pct":100,"v_pct":100}`} {
		r, err := patternsv2.NewRecord("color", "相同执行", patternsv2.Source{Kind: "light_action", Contract: lightaction.Contract, Action: []byte(`{"kind":"set","color":` + color + `}`)})
		if err != nil {
			t.Fatal(err)
		}
		d := assertPatternP1Bytes(t, r)
		if sameDigest != "" && (d != sameDigest || previous.Revision == r.Revision) {
			t.Fatal("author notation identity was collapsed or execution changed")
		}
		previous, sameDigest = r, d
	}
	r, err := patternsv2.NewRecord("hsv", "小数", patternsv2.Source{Kind: "light_action", Contract: lightaction.Contract, Action: []byte(`{"kind":"set","color":{"space":"hsv","h_deg":240.000000,"s_pct":100,"v_pct":50.500000}}`)})
	if err != nil {
		t.Fatal(err)
	}
	assertPatternP1Bytes(t, r)
	for _, name := range []string{"orbit-red-blue", "orbit-hex-equivalent", "pulse-lime"} {
		data, err := os.ReadFile(filepath.Join("..", "patternsv2", "testdata", "design", "records", name+".record.json"))
		if err != nil {
			t.Fatal(err)
		}
		r, err := patternsv2.DecodeRecord(data)
		if err != nil {
			t.Fatal(err)
		}
		assertPatternP1Bytes(t, r)
	}
}

func typedPatternProgram() control.PatternExecution {
	return control.PatternExecution{Kind: "program", Program: &control.Program{Steps: []control.Step{{LED: 0, Color: "#000000"}}, Repeat: 1, OnFinish: "off"}}
}
func typedPatternFrame() control.PatternExecution {
	return control.PatternExecution{Kind: "frame_program", FrameProgram: &control.FrameProgram{Contract: control.FrameContract,
		Initial: []control.FrameTarget{{LED: 2, Color: "#000000"}}, Frames: []control.Frame{{Targets: []control.FrameTarget{{LED: 2, Color: "#010203"}}, FadeMS: 0, IntervalMS: 50}}, Repeat: 1, OnFinish: "off"}}
}

func TestPatternExecutionStrictFrozenAndNoMutation(t *testing.T) {
	for _, change := range []func(*control.PatternExecution){
		func(e *control.PatternExecution) { e.Kind = "frame" },
		func(e *control.PatternExecution) { e.Program = nil },
		func(e *control.PatternExecution) { e.FrameProgram = typedPatternFrame().FrameProgram },
		func(e *control.PatternExecution) { e.Program.Repeat = 0 },
		func(e *control.PatternExecution) { e.Program.OnFinish = "" },
		func(e *control.PatternExecution) { e.Program.Steps[0].Color = "#ABCDEF" },
		func(e *control.PatternExecution) { e.Program.Steps[0].Color = "red" },
		func(e *control.PatternExecution) { e.Program.Steps[0].FadeMS = 1 },
		func(e *control.PatternExecution) { e.Program.Steps[0].HoldMS = -1 },
		func(e *control.PatternExecution) { e.Program.Steps = make([]control.Step, 1025) },
	} {
		e := typedPatternProgram()
		change(&e)
		assertPatternRejected(t, e)
	}
	for _, change := range []func(*control.FrameProgram){
		func(p *control.FrameProgram) { p.Repeat = 0 }, func(p *control.FrameProgram) { p.OnFinish = "" },
		func(p *control.FrameProgram) { p.Contract = "" }, func(p *control.FrameProgram) { p.Initial[0].Color = "#ABCDEF" },
		func(p *control.FrameProgram) { p.Frames[0].Targets[0].Color = "#ABCDEF" },
		func(p *control.FrameProgram) { p.Frames[0].FadeMS = 55 }, func(p *control.FrameProgram) { p.Frames[0].FadeMS = 60 },
		func(p *control.FrameProgram) { p.Frames[0].Targets[0].LED = 1 }, func(p *control.FrameProgram) { p.Repeat = 1025 },
	} {
		e := typedPatternFrame()
		change(e.FrameProgram)
		assertPatternRejected(t, e)
	}
	assertPatternRejected(t, control.PatternExecution{})
	for _, e := range []control.PatternExecution{typedPatternProgram(), typedPatternFrame()} {
		validated, err := control.ValidatePatternExecution(e)
		if err != nil {
			t.Fatal(err)
		}
		original, _ := validated.CanonicalJSON()
		returned, _ := e.CanonicalJSON()
		returned[0] = '!'
		if e.Program != nil {
			e.Program.Steps[0].Color = "#ffffff"
		} else {
			e.FrameProgram.Initial[0].Color = "#ffffff"
			e.FrameProgram.Frames[0].Targets[0].Color = "#ffffff"
		}
		after, _ := validated.CanonicalJSON()
		if !bytes.Equal(original, after) {
			t.Fatal("validation or canonical bytes alias caller data")
		}
	}
}

func assertPatternRejected(t *testing.T, e control.PatternExecution) {
	t.Helper()
	before, _ := json.Marshal(e)
	v, err := control.ValidatePatternExecution(e)
	if !errors.Is(err, control.ErrInvalid) || v.Kind != "" {
		t.Fatal("noncanonical frozen value accepted", err)
	}
	if b, err := e.CanonicalJSON(); !errors.Is(err, control.ErrInvalid) || len(b) != 0 {
		t.Fatal("invalid canonical output", err)
	}
	if d, err := e.Digest(); !errors.Is(err, control.ErrInvalid) || d != "" {
		t.Fatal("invalid digest output", err)
	}
	if err := e.VerifyDigest("sha256:" + strings.Repeat("0", 64)); !errors.Is(err, control.ErrInvalid) {
		t.Fatal("invalid input accepted by verify", err)
	}
	after, _ := json.Marshal(e)
	if !bytes.Equal(before, after) {
		t.Fatal("validation changed the caller's frozen data")
	}
}

func TestPatternExecutionIndependentTypedVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/pattern-execution/reference-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name      string `json:"name"`
		Execution struct {
			Kind     string          `json:"kind"`
			Contract string          `json:"contract"`
			Program  json.RawMessage `json:"program"`
		} `json:"execution"`
		Canonical string `json:"canonical"`
		Digest    string `json:"digest"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			e := control.PatternExecution{Kind: v.Execution.Kind}
			if e.Kind == "program" {
				e.Program = new(control.Program)
				err = json.Unmarshal(v.Execution.Program, e.Program)
			} else {
				e.FrameProgram = new(control.FrameProgram)
				err = json.Unmarshal(v.Execution.Program, e.FrameProgram)
			}
			if err != nil {
				t.Fatal(err)
			}
			b, err := e.CanonicalJSON()
			d, digestErr := e.Digest()
			if err != nil || digestErr != nil || string(b) != v.Canonical || d != v.Digest || e.VerifyDigest(v.Digest) != nil {
				t.Fatal("independent typed vector mismatch", err, digestErr, d)
			}
			if boundary, err := control.ValidatePatternExecution(e); err == nil {
				switch v.Name {
				case "program-exact-day":
					boundary.Program.Steps[0].HoldMS++
				case "program-max-repeat":
					boundary.Program.Repeat++
				case "program-stored1024-repeat2":
					boundary.Program.Steps = append(boundary.Program.Steps, boundary.Program.Steps[0])
				case "frame-exact-day144":
					boundary.FrameProgram.Frames[len(boundary.FrameProgram.Frames)-1].IntervalMS++
				case "frame-one-stored-repeat1024":
					boundary.FrameProgram.Repeat++
				case "frame-stored1024-repeat1":
					boundary.FrameProgram.Frames = append(boundary.FrameProgram.Frames, boundary.FrameProgram.Frames[0])
				default:
					boundary.Kind = "invalid"
				}
				assertPatternRejected(t, boundary)
			}
			wrong := fmt.Sprintf("sha256:%x", sha256.Sum256(append([]byte(patternidentity.ExecutionDomain+"\n\n"), b...)))
			trailing := fmt.Sprintf("sha256:%x", sha256.Sum256(append(append([]byte(patternidentity.ExecutionDomain+"\n"), b...), '\n')))
			recordDomain := fmt.Sprintf("sha256:%x", sha256.Sum256(append([]byte(patternidentity.RecordContract+"\n"), b...)))
			for _, bad := range []string{wrong, trailing, recordDomain, "latest", v.Digest + "\n", "sha256:" + strings.Repeat("0", 64)} {
				if !errors.Is(e.VerifyDigest(bad), control.ErrInvalid) {
					t.Fatal("bad expected digest accepted")
				}
			}
			// Every public method must revalidate the current mutable input.
			if e.Program != nil {
				e.Program.Repeat = 0
			} else {
				e.FrameProgram.Repeat = 0
			}
			assertPatternRejected(t, e)
		})
	}
}

func TestPatternExecutionEveryFieldAndArrayOrderBindDigest(t *testing.T) {
	program := typedPatternProgram()
	program.Program.Steps = append(program.Program.Steps, control.Step{LED: 2, Color: "#010203", FadeMS: 10, HoldMS: 1})
	frame := control.PatternExecution{Kind: "frame_program", FrameProgram: &control.FrameProgram{
		Contract: control.FrameContract, Initial: []control.FrameTarget{{LED: 1, Color: "#000000"}, {LED: 2, Color: "#010203"}},
		Frames: []control.Frame{
			{Targets: []control.FrameTarget{{LED: 1, Color: "#020304"}, {LED: 2, Color: "#030405"}}, FadeMS: 0, IntervalMS: 50},
			{Targets: []control.FrameTarget{{LED: 1, Color: "#040506"}, {LED: 2, Color: "#050607"}}, FadeMS: 10, IntervalMS: 60},
		}, Repeat: 1, OnFinish: "off",
	}}
	for _, group := range []struct {
		base    control.PatternExecution
		changes map[string]func(control.PatternExecution)
	}{
		{program, map[string]func(control.PatternExecution){
			"program-color":  func(e control.PatternExecution) { e.Program.Steps[0].Color = "#ff0000" },
			"program-led":    func(e control.PatternExecution) { e.Program.Steps[0].LED = 1 },
			"program-fade":   func(e control.PatternExecution) { e.Program.Steps[0].FadeMS = 10 },
			"program-hold":   func(e control.PatternExecution) { e.Program.Steps[0].HoldMS = 1 },
			"program-repeat": func(e control.PatternExecution) { e.Program.Repeat = 2 },
			"program-finish": func(e control.PatternExecution) { e.Program.OnFinish = "hold" },
			"program-order": func(e control.PatternExecution) {
				e.Program.Steps[0], e.Program.Steps[1] = e.Program.Steps[1], e.Program.Steps[0]
			},
		}},
		{frame, map[string]func(control.PatternExecution){
			"frame-contract":      func(e control.PatternExecution) { e.FrameProgram.Contract = "future/v3" },
			"frame-initial-color": func(e control.PatternExecution) { e.FrameProgram.Initial[0].Color = "#ff0000" },
			"frame-target-color":  func(e control.PatternExecution) { e.FrameProgram.Frames[0].Targets[0].Color = "#ff0000" },
			"frame-initial-led":   func(e control.PatternExecution) { e.FrameProgram.Initial[0].LED = 2 },
			"frame-target-led":    func(e control.PatternExecution) { e.FrameProgram.Frames[0].Targets[0].LED = 2 },
			"frame-fade":          func(e control.PatternExecution) { e.FrameProgram.Frames[0].FadeMS = 10 },
			"frame-interval":      func(e control.PatternExecution) { e.FrameProgram.Frames[0].IntervalMS = 51 },
			"frame-repeat":        func(e control.PatternExecution) { e.FrameProgram.Repeat = 2 },
			"frame-finish":        func(e control.PatternExecution) { e.FrameProgram.OnFinish = "hold" },
			"frame-order": func(e control.PatternExecution) {
				p := e.FrameProgram
				p.Frames[0], p.Frames[1] = p.Frames[1], p.Frames[0]
			},
			"target-order": func(e control.PatternExecution) {
				p := e.FrameProgram
				p.Initial[0], p.Initial[1] = p.Initial[1], p.Initial[0]
				for i := range p.Frames {
					p.Frames[i].Targets[0], p.Frames[i].Targets[1] = p.Frames[i].Targets[1], p.Frames[i].Targets[0]
				}
			},
		}},
	} {
		original, err := group.base.Digest()
		if err != nil {
			t.Fatal(err)
		}
		for name, change := range group.changes {
			t.Run(name, func(t *testing.T) {
				e, err := control.ValidatePatternExecution(group.base)
				if err != nil {
					t.Fatal(err)
				}
				change(e)
				d, err := e.Digest()
				if err == nil && d == original {
					t.Fatal("changed execution did not change digest")
				}
				if err != nil && !errors.Is(err, control.ErrInvalid) {
					t.Fatal(err)
				}
				if !errors.Is(e.VerifyDigest(original), control.ErrInvalid) {
					t.Fatal("changed execution matched original digest")
				}
			})
		}
	}
}

func TestPatternExecutionConcurrentIndependentResults(t *testing.T) {
	for _, e := range []control.PatternExecution{typedPatternProgram(), typedPatternFrame()} {
		want, err := e.Digest()
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for n := 0; n < 8; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				copy, err := control.ValidatePatternExecution(e)
				if err != nil {
					t.Error(err)
					return
				}
				if d, _ := copy.Digest(); d != want || copy.VerifyDigest(want) != nil {
					t.Error("concurrent digest drift")
				}
				if copy.Program != nil {
					copy.Program.Steps[0].Color = "#ffffff"
				} else {
					copy.FrameProgram.Initial[0].Color = "#ffffff"
					copy.FrameProgram.Frames[0].Targets[0].Color = "#ffffff"
				}
			}()
		}
		wg.Wait()
		if after, _ := e.Digest(); after != want {
			t.Fatal("concurrent copies modified input")
		}
	}
}
