package control

import (
	"reflect"
	"testing"
)

func TestPreviewKnownProgramsAndCompilerLimits(t *testing.T) {
	cycles, period := 2, 800
	effect := Effect{Kind: "pulse", Color: "#00A000", Cycles: &cycles, PeriodMS: &period}
	r, err := Preview(nil, &effect)
	expected := Program{Steps: []Step{{Color: "#000000"}, {Color: "#00a000", FadeMS: 380}, {Color: "#000000", FadeMS: 390}}, Repeat: 2, OnFinish: "off"}
	if err != nil || !reflect.DeepEqual(r.Program, expected) || r.NominalDurationMS != 1600 || r.TotalSteps != 6 || ValidatePreviewResult(r, "effect", nil) != nil {
		t.Fatal(r, err)
	}
	fade := 100
	r, err = Preview(nil, &Effect{Kind: "steady", Color: "#ABCDEF", FadeMS: &fade})
	if err != nil || r.NominalDurationMS != 110 || r.Program.OnFinish != "hold" || !reflect.DeepEqual(r.RequiredCapabilities, []string{"set", "program", "raw-rgb", "hold"}) {
		t.Fatal(r, err)
	}
	raw := Program{Steps: []Step{{LED: 2, Color: "#ABCDEF"}}}
	r, err = Preview(&raw, nil)
	if err != nil || r.Program.Repeat != 1 || r.Program.OnFinish != "off" || r.Program.Steps[0].Color != "#abcdef" || raw.Steps[0].Color != "#ABCDEF" || raw.Repeat != 0 {
		t.Fatal(r, raw, err)
	}
	r.Program.Steps[0].Color = "#000000"
	if raw.Steps[0].Color != "#ABCDEF" {
		t.Fatal("preview aliases caller steps")
	}
	if _, err = Preview(nil, nil); err == nil {
		t.Fatal("empty input")
	}
	if _, err = Preview(&raw, &effect); err == nil {
		t.Fatal("mixed input")
	}
	bad := Effect{Kind: "pulse", Color: "#ffffff", Cycles: &cycles, PeriodMS: &period, FadeMS: &fade}
	if _, err = Preview(nil, &bad); err == nil {
		t.Fatal("invalid effect")
	}
	exact := Program{Steps: []Step{{Color: "#000000", HoldMS: 8630}}, Repeat: 10000}
	if r, err = Preview(&exact, nil); err != nil || r.NominalDurationMS != 86400000 {
		t.Fatal(r, err)
	}
	exact.Steps[0].HoldMS++
	if _, err = Preview(&exact, nil); err == nil {
		t.Fatal("24h budget was not shared")
	}
}

func TestPreviewSnapshotIsOnlyCapabilityEvidence(t *testing.T) {
	p := Program{Steps: []Step{{Color: "#000000"}}}
	r, _ := Preview(&p, nil)
	serial := "demo"
	cases := []struct {
		name, connection string
		caps             []string
		status, reason   string
	}{
		{"empty", "", []string{"program"}, "unknown", "not_configured"},
		{"offline", "offline", []string{"set", "program", "raw-rgb"}, "unknown", "not_online"},
		{"connecting", "connecting", nil, "unknown", "not_online"},
		{"no_caps", "online", nil, "unknown", "capabilities_unavailable"},
		{"missing", "online", []string{"set", "program"}, "unsupported", "capabilities_missing"},
		{"online", "online", []string{"set", "program", "raw-rgb"}, "supported", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snapshot := DeviceState{DeviceInfo: DeviceInfo{Serial: serial, Capabilities: c.caps}, Connection: c.connection, ActiveJob: "busy-job", QueueDepth: 8}
			out := WithDeviceSnapshot(r, serial, &snapshot)
			if out.Validation.Device != c.status || out.Validation.Reason != c.reason || ValidatePreviewResult(out, "program", &serial) != nil {
				t.Fatal(out)
			}
			if len(out.Device.Capabilities) > 0 {
				out.Device.Capabilities[0] = "mutated"
				if snapshot.Capabilities[0] == "mutated" {
					t.Fatal("capability aliases snapshot")
				}
			}
			out.Program.Steps[0].Color = "#ff0000"
			if r.Program.Steps[0].Color != "#000000" {
				t.Fatal("program aliases previous preview")
			}
		})
	}
	missing := WithDeviceSnapshot(r, serial, nil)
	if missing.Validation.Reason != "not_configured" || ValidatePreviewResult(missing, "program", &serial) != nil {
		t.Fatal(missing)
	}
	wrong := serial + "-other"
	if ValidatePreviewResult(missing, "program", &wrong) == nil || ValidatePreviewResult(missing, "program", nil) == nil {
		t.Fatal("target mismatch accepted")
	}
	for _, mutate := range []func(*PreviewResult){func(v *PreviewResult) { v.CompileContract = "unknown" }, func(v *PreviewResult) { v.TotalSteps++ }, func(v *PreviewResult) { v.NominalDurationMS++ }, func(v *PreviewResult) { v.Program.Repeat = 0 }, func(v *PreviewResult) { v.Validation.Device = "supported" }} {
		v, _ := Preview(&p, nil)
		mutate(&v)
		if ValidatePreviewResult(v, "program", nil) == nil {
			t.Fatal("invalid response accepted", v)
		}
	}
}

func TestCapabilityCatalogDoesNotAliasAndSharesBoundary(t *testing.T) {
	first := Capabilities()
	second := Capabilities()
	if ValidateCapabilityCatalog(first) != nil {
		t.Fatal(first)
	}
	first.SupportedInputs[0] = "mutated"
	first.LEDSelectors[0] = 5
	if second.SupportedInputs[0] != "program" || second.LEDSelectors[0] != 0 {
		t.Fatal("catalog aliases caller")
	}
	extended := Capabilities()
	extended.PatternLibrary = true
	extended.SupportedInputs = append(extended.SupportedInputs, "pattern_ref")
	if ValidateCapabilityCatalog(extended) != nil {
		t.Fatal("known compiler future feature flags should remain readable")
	}
	bad := Capabilities()
	bad.ProgramLimits.MaxSteps++
	if ValidateCapabilityCatalog(bad) == nil {
		t.Fatal("wrong compiler limits accepted")
	}
	for _, s := range []string{"", "x/y", "x y", "é"} {
		if ValidDeviceSerial(s) {
			t.Fatal(s)
		}
	}
	if !ValidDeviceSerial("20005601") || !ValidDeviceSerial("mk2_demo-1") {
		t.Fatal("serial")
	}
}
