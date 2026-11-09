package control

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func frameTestProgram() FrameProgram {
	return FrameProgram{Contract: FrameContract,
		Initial: []FrameTarget{{LED: 1, Color: "#990000"}, {LED: 2, Color: "#000099"}},
		Frames:  []Frame{{Targets: []FrameTarget{{LED: 1, Color: "#000099"}, {LED: 2, Color: "#990000"}}, FadeMS: 380, IntervalMS: 375}},
	}
}

func frameTestJSON() string {
	return `{"contract":"` + FrameContract + `","initial":[{"led":1,"color":"#ABCDEF"}],"frames":[{"targets":[{"led":1,"color":"#123456"}],"fade_ms":80,"interval_ms":75}]}`
}

func TestFrameDecodeDefaultsAndCanonicalRoundTrip(t *testing.T) {
	got, err := DecodeFrameProgram([]byte(frameTestJSON()))
	if err != nil {
		t.Fatal(err)
	}
	want := FrameProgram{Contract: FrameContract, Initial: []FrameTarget{{LED: 1, Color: "#abcdef"}},
		Frames: []Frame{{Targets: []FrameTarget{{LED: 1, Color: "#123456"}}, FadeMS: 80, IntervalMS: 75}}, Repeat: 1, OnFinish: "off"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults or raw RGB normalization changed: %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"contract":"` + FrameContract + `","initial":[{"led":1,"color":"#abcdef"}],"frames":[{"targets":[{"led":1,"color":"#123456"}],"fade_ms":80,"interval_ms":75}],"repeat":1,"on_finish":"off"}`
	if string(encoded) != wantJSON {
		t.Fatalf("normalized encoding changed: %s", encoded)
	}
	roundTrip, err := DecodeFrameProgram(encoded)
	if err != nil || !reflect.DeepEqual(roundTrip, got) {
		t.Fatalf("canonical decode differs: %#v, %v", roundTrip, err)
	}
}

func TestFrameDecodeRejectsAmbiguousAndIncompleteJSON(t *testing.T) {
	valid := frameTestJSON()
	cases := map[string]string{
		"empty": "", "null": "null", "array": "[]", "empty-object": "{}",
		"tail-object": valid + `{}`, "tail-array": valid + `[]`,
		"unknown":                     strings.Replace(valid, `"initial":`, `"surprise":1,"initial":`, 1),
		"wrong-case":                  strings.Replace(valid, `"contract":`, `"Contract":`, 1),
		"duplicate":                   strings.Replace(valid, `"initial":`, `"initial":[],"initial":`, 1),
		"escaped-duplicate":           strings.Replace(valid, `"initial":`, `"in\u0069tial":[],"initial":`, 1),
		"nested-duplicate":            strings.Replace(valid, `"fade_ms":80`, `"fade_ms":80,"fade_ms":80`, 1),
		"target-unknown":              strings.Replace(valid, `"led":1`, `"led":1,"gamma":true`, 1),
		"frame-unknown":               strings.Replace(valid, `"fade_ms":80`, `"wait_ms":0,"fade_ms":80`, 1),
		"missing-contract":            strings.Replace(valid, `"contract":"`+FrameContract+`",`, ``, 1),
		"stable-unpublished-contract": strings.Replace(valid, FrameContract, "nb1-frame-program/v1", 1),
		"missing-led":                 strings.Replace(valid, `"led":1,`, ``, 1),
		"missing-color":               strings.Replace(valid, `,"color":"#ABCDEF"`, ``, 1),
		"missing-fade":                strings.Replace(valid, `,"fade_ms":80`, ``, 1),
		"missing-interval":            strings.Replace(valid, `,"interval_ms":75`, ``, 1),
		"null-contract":               strings.Replace(valid, `"`+FrameContract+`"`, `null`, 1),
		"null-initial":                strings.Replace(valid, `[{"led":1,"color":"#ABCDEF"}]`, `null`, 1),
		"null-target":                 strings.Replace(valid, `[{"led":1,"color":"#ABCDEF"}]`, `[null]`, 1),
		"null-frames":                 strings.Replace(valid, `"frames":[`, `"frames":null,"unused":[`, 1),
		"null-fade":                   strings.Replace(valid, `"fade_ms":80`, `"fade_ms":null`, 1),
		"null-repeat":                 strings.TrimSuffix(valid, `}`) + `,"repeat":null}`,
		"explicit-zero-repeat":        strings.TrimSuffix(valid, `}`) + `,"repeat":0}`,
		"empty-finish":                strings.TrimSuffix(valid, `}`) + `,"on_finish":""}`,
		"null-finish":                 strings.TrimSuffix(valid, `}`) + `,"on_finish":null}`,
		"fraction":                    strings.Replace(valid, `"fade_ms":80`, `"fade_ms":80.0`, 1),
		"exponent":                    strings.Replace(valid, `"fade_ms":80`, `"fade_ms":8e1`, 1),
		"numeric-string":              strings.Replace(valid, `"fade_ms":80`, `"fade_ms":"80"`, 1),
		"bool-number":                 strings.Replace(valid, `"fade_ms":80`, `"fade_ms":true`, 1),
		"int-overflow":                strings.Replace(valid, `"fade_ms":80`, `"fade_ms":9223372036854775808`, 1),
		"surrogate-color":             strings.Replace(valid, `#ABCDEF`, `\ud800`, 1),
		"invalid-utf8":                valid + string([]byte{0xff}),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := DecodeFrameProgram([]byte(input))
			if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(p, FrameProgram{}) {
				t.Fatalf("accepted ambiguous frame input: %#v, %v", p, err)
			}
		})
	}
	// A required fade can explicitly be zero; missing it was rejected above.
	if _, err := DecodeFrameProgram([]byte(strings.Replace(valid, `"fade_ms":80`, `"fade_ms":0`, 1))); err != nil {
		t.Fatalf("explicit zero fade is valid: %v", err)
	}
}

func TestFrameDecodeByteLimit(t *testing.T) {
	valid := frameTestJSON()
	input := valid + strings.Repeat(" ", MaxFrameProgramBytes-len(valid))
	if _, err := DecodeFrameProgram([]byte(input)); err != nil {
		t.Fatalf("exact byte limit rejected: %v", err)
	}
	if _, err := DecodeFrameProgram([]byte(input + " ")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized input accepted: %v", err)
	}
}

func TestFrameNormalizeGeometryAndDurationBounds(t *testing.T) {
	cases := map[string]func(*FrameProgram){
		"contract":          func(p *FrameProgram) { p.Contract = "" },
		"empty-initial":     func(p *FrameProgram) { p.Initial = nil },
		"too-many-targets":  func(p *FrameProgram) { p.Initial = append(p.Initial, p.Initial[0]) },
		"initial-duplicate": func(p *FrameProgram) { p.Initial[1].LED = 1 },
		"frame-duplicate":   func(p *FrameProgram) { p.Frames[0].Targets[1].LED = 1 },
		"led-zero":          func(p *FrameProgram) { p.Initial[0].LED = 0 },
		"led-three":         func(p *FrameProgram) { p.Initial[0].LED = 3 },
		"led-negative":      func(p *FrameProgram) { p.Initial[0].LED = -1 },
		"target-count":      func(p *FrameProgram) { p.Frames[0].Targets = p.Frames[0].Targets[:1] },
		"target-order": func(p *FrameProgram) {
			p.Frames[0].Targets[0], p.Frames[0].Targets[1] = p.Frames[0].Targets[1], p.Frames[0].Targets[0]
		},
		"empty-frames":      func(p *FrameProgram) { p.Frames = nil },
		"initial-color":     func(p *FrameProgram) { p.Initial[0].Color = "red" },
		"frame-color":       func(p *FrameProgram) { p.Frames[0].Targets[1].Color = "#zz0000" },
		"negative-fade":     func(p *FrameProgram) { p.Frames[0].FadeMS = -10 },
		"fade-quantum":      func(p *FrameProgram) { p.Frames[0].FadeMS = 379 },
		"long-fade":         func(p *FrameProgram) { p.Frames[0].FadeMS = 600010 },
		"trailing-fade":     func(p *FrameProgram) { p.Frames[0].FadeMS = 390 },
		"short-interval":    func(p *FrameProgram) { p.Frames[0].IntervalMS = 49 },
		"negative-interval": func(p *FrameProgram) { p.Frames[0].IntervalMS = -1 },
		"long-interval":     func(p *FrameProgram) { p.Frames[0].IntervalMS = 600001 },
		"negative-repeat":   func(p *FrameProgram) { p.Repeat = -1 },
		"repeat-limit":      func(p *FrameProgram) { p.Repeat = 10001 },
		"finish":            func(p *FrameProgram) { p.OnFinish = "restart" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := frameTestProgram()
			mutate(&p)
			got, err := NormalizeFrameProgram(p)
			if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(got, FrameProgram{}) {
				t.Fatalf("invalid frame program accepted: %#v, %v", got, err)
			}
		})
	}
	// Reversed LED order is fine if every frame agrees with the declared order.
	p := frameTestProgram()
	p.Initial[0].LED, p.Initial[1].LED = 2, 1
	p.Frames[0].Targets[0].LED, p.Frames[0].Targets[1].LED = 2, 1
	if _, err := NormalizeFrameProgram(p); err != nil {
		t.Fatal(err)
	}
}

func TestFramePreviewInitializationRepeatAndTail(t *testing.T) {
	p := frameTestProgram()
	p.Frames = make([]Frame, 8)
	for i := range p.Frames {
		p.Frames[i] = Frame{Targets: []FrameTarget{{LED: 1, Color: "#aa0000"}, {LED: 2, Color: "#0000bb"}}, FadeMS: 380, IntervalMS: 375}
	}
	p.Repeat = 3
	got, err := PreviewFrameProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalFrames != 24 || got.StoredTargetWrites != 18 || got.PlannedTargetWrites != 50 || got.CycleDurationMS != 3000 || got.NominalInitialMS != 20 || got.NominalTailMS != 25 || got.NominalDurationMS != 9045 {
		t.Fatalf("initial/tail was repeated or writes were omitted: %#v", got)
	}
	if got.Execution.Available || got.Execution.Reason != "frame_executor_not_integrated" || got.TimingBasis != "host-nominal-excludes-native-io" {
		t.Fatalf("pure preview claims hardware execution: %#v", got)
	}
	wantCaps := []string{"set", "raw-rgb", FrameExecutorCapability}
	if !reflect.DeepEqual(got.RequiredCapabilities, wantCaps) {
		t.Fatalf("missing new executor requirement: %v", got.RequiredCapabilities)
	}
	p.OnFinish = "hold"
	hold, err := PreviewFrameProgram(p)
	if err != nil || !reflect.DeepEqual(hold.RequiredCapabilities, append(wantCaps, "hold")) {
		t.Fatalf("hold requirement lost: %#v %v", hold, err)
	}
	for _, name := range Capabilities().SupportedInputs {
		if strings.Contains(name, "frame") {
			t.Fatal("experimental parser was advertised as an enabled input")
		}
	}
}

func TestFramePreviewBounds(t *testing.T) {
	p := frameTestProgram()
	p.Frames[0].FadeMS, p.Frames[0].IntervalMS = 50, 50
	p.Repeat = MaxFrameProgramExecutedFrames
	atLimit, err := PreviewFrameProgram(p)
	if err != nil || atLimit.TotalFrames != 1024 || atLimit.PlannedTargetWrites != MaxFrameProgramTargetWrites || atLimit.StoredTargetWrites != 4 {
		t.Fatalf("executed limit rejected or stored cost repeated: %#v, %v", atLimit, err)
	}
	p.Repeat++
	if _, err := PreviewFrameProgram(p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("executed frame limit bypassed: %v", err)
	}
	p = frameTestProgram()
	p.Frames = make([]Frame, MaxFrameProgramFrames+1)
	if _, err := NormalizeFrameProgram(p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("stored frame limit bypassed: %v", err)
	}
	// An exact 24h budget includes initialization; no fade tail for zero fade.
	p = FrameProgram{Contract: FrameContract, Initial: []FrameTarget{{LED: 1, Color: "#000000"}}, Repeat: 1, OnFinish: "off", Frames: make([]Frame, 144)}
	for i := range p.Frames {
		p.Frames[i] = Frame{Targets: []FrameTarget{{LED: 1, Color: "#000000"}}, IntervalMS: 600000}
	}
	p.Frames[143].IntervalMS = 599990
	atDay, err := PreviewFrameProgram(p)
	if err != nil || atDay.NominalDurationMS != MaxFrameNominalDurationMS || atDay.NominalTailMS != 0 {
		t.Fatalf("exact day bound changed: %#v %v", atDay, err)
	}
	p.Frames[143].IntervalMS++
	if _, err := PreviewFrameProgram(p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("day bound missed initialization: %v", err)
	}
}

func TestFrameNormalizeAndPreviewNeverAliasCaller(t *testing.T) {
	p := frameTestProgram()
	p.Initial[0].Color = "#AA0000"
	p.Frames[0].Targets[0].Color = "#BB0000"
	normal, err := NormalizeFrameProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewFrameProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	if p.Repeat != 0 || p.OnFinish != "" || p.Initial[0].Color != "#AA0000" || p.Frames[0].Targets[0].Color != "#BB0000" {
		t.Fatal("normalization mutated the input")
	}
	normal.Initial[0].Color = "#ffffff"
	normal.Frames[0].Targets[0].Color = "#eeeeee"
	preview.Program.Initial[0].LED = 2
	preview.Program.Frames[0].Targets[0].Color = "#dddddd"
	if p.Initial[0].LED != 1 || p.Initial[0].Color != "#AA0000" || p.Frames[0].Targets[0].Color != "#BB0000" {
		t.Fatal("nested mutable arrays were shared with caller")
	}
	copy := cloneFrameProgram(p)
	copy.Initial[0].Color = "#000000"
	copy.Frames[0].Targets[0].Color = "#000000"
	if p.Initial[0].Color != "#AA0000" || p.Frames[0].Targets[0].Color != "#BB0000" {
		t.Fatal("clone shares nested targets")
	}
}

func TestFrameCheckedArithmetic(t *testing.T) {
	if got, err := checkedFrameAdd(frameMaxInt64-1, 1); err != nil || got != frameMaxInt64 {
		t.Fatalf("exact addition: %d %v", got, err)
	}
	if got, err := checkedFrameMultiply(frameMaxInt64, 1); err != nil || got != frameMaxInt64 {
		t.Fatalf("exact multiplication: %d %v", got, err)
	}
	for _, pair := range [][2]int64{{frameMaxInt64, 1}, {-1, 0}, {0, -1}} {
		if _, err := checkedFrameAdd(pair[0], pair[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unchecked sum: %v", pair)
		}
	}
	for _, pair := range [][2]int64{{frameMaxInt64, 2}, {-1, 0}, {0, -1}} {
		if _, err := checkedFrameMultiply(pair[0], pair[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unchecked product: %v", pair)
		}
	}
}

func TestFrameParallelPreviewDoesNotMutateSharedInput(t *testing.T) {
	p := frameTestProgram()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			preview, err := PreviewFrameProgram(p)
			if err != nil {
				t.Error(err)
				return
			}
			preview.Program.Frames[0].Targets[0].Color = "#ffffff"
		}()
	}
	wg.Wait()
	if p.Frames[0].Targets[0].Color != "#000099" {
		t.Fatal("concurrent preview mutated source")
	}
}

func FuzzFrameDecodeRoundTrip(f *testing.F) {
	f.Add([]byte(frameTestJSON()))
	f.Add([]byte(`{"contract":null}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := DecodeFrameProgram(data)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("unexpected error domain: %v", err)
			}
			return
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		q, err := DecodeFrameProgram(encoded)
		if err != nil || !reflect.DeepEqual(p, q) {
			t.Fatalf("normalized program cannot round-trip: %v", err)
		}
		preview, err := PreviewFrameProgram(p)
		if err != nil || preview.Execution.Available || preview.TotalFrames < 1 || preview.PlannedTargetWrites > MaxFrameProgramTargetWrites {
			t.Fatalf("accepted parser and preview disagree: %#v %v", preview, err)
		}
	})
}
