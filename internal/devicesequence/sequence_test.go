package devicesequence

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func document(lines string) []byte {
	return []byte(`{"contract":"nb1-device-sequence/draft-1","color_space":"device-rgb8","lines":[` + lines + `]}`)
}

const redRow = `{"led":"all","color":"red","duration_ms":500}`
const blueRow = `{"led":2,"color":"#00f","duration_ms":500}`

func pointer(n int) *int { return &n }
func reference() Capabilities {
	return Capabilities{Model: "mk2", Firmware: 204, ReferenceFirmware: 204, Applicability: FirmwareMatched, RAMLines: pointer(32), FlashLines: pointer(16), UsableFlashLines: pointer(16), Sources: []string{"https://github.com/todbot/blink1/blob/f4bf437fa016b2975675fecc2ad18fd04c955fa8/hardware/firmware_mk2/main.c"}}
}
func hasIssue(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestCompileColorInputsPreserveSource(t *testing.T) {
	for _, color := range []string{`"red"`, `"#f00"`, `"#ff0000"`, `{"space":"rgb8","r":255,"g":0,"b":0}`, `{"space":"hsv","h_deg":0,"s_pct":100,"v_pct":100}`} {
		t.Run(color, func(t *testing.T) {
			input := document(`{"led":"all","color":` + color + `,"duration_ms":500}`)
			original := append([]byte(nil), input...)
			got, err := CompileJSON(input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Sequence.Lines[0] != (Line{LED: 0, Color: "#ff0000", DurationMS: 500}) {
				t.Fatalf("%+v", got)
			}
			if !bytes.Equal(got.OriginalJSON, original) {
				t.Fatal("source was normalized in place")
			}
			input[0] = '!'
			if !bytes.Equal(got.OriginalJSON, original) {
				t.Fatal("source storage aliases caller")
			}
			canonical, _ := json.Marshal(got.Sequence)
			if !strings.Contains(string(canonical), `"led":"all"`) {
				t.Fatal(string(canonical))
			}
		})
	}
	a, _ := CompileJSON(document(redRow))
	b, _ := CompileJSON(document(`{"duration_ms":500,"color":"#f00","led":"all"}`))
	if a.Digest != b.Digest {
		t.Fatal("equivalent normalized rows have different digests")
	}
}

func TestRejectInvalidAuthorDocuments(t *testing.T) {
	tests := map[string][]byte{
		"zero duration":            document(`{"led":1,"color":"red","duration_ms":0}`),
		"quantized":                document(`{"led":1,"color":"red","duration_ms":505}`),
		"fraction":                 document(`{"led":1,"color":"red","duration_ms":500.0}`),
		"exponent":                 document(`{"led":1,"color":"red","duration_ms":5e2}`),
		"quoted number":            document(`{"led":1,"color":"red","duration_ms":"500"}`),
		"negative":                 document(`{"led":1,"color":"red","duration_ms":-500}`),
		"wire overflow":            document(`{"led":1,"color":"red","duration_ms":655360}`),
		"duplicate":                document(`{"led":1,"l\u0065d":2,"color":"red","duration_ms":500}`),
		"invalid led":              document(`{"led":0,"color":"red","duration_ms":500}`),
		"flags":                    document(`{"led":129,"color":"red","duration_ms":500}`),
		"targets":                  document(`{"led":[1,2],"color":"red","duration_ms":500}`),
		"null":                     document(`null`),
		"null color":               document(`{"led":1,"color":null,"duration_ms":500}`),
		"duplicate color field":    document(`{"led":1,"color":{"space":"rgb8","r":0,"r":255,"g":0,"b":0},"duration_ms":500}`),
		"unpaired unicode":         document(`{"led":1,"color":"\ud800","duration_ms":500}`),
		"surrogate key":            document(`{"led":1,"color":"red","duration_ms":500,"\ud800":1}`),
		"fade instead of duration": document(`{"led":1,"color":"red","fade_ms":500}`),
		"separate interval":        document(`{"led":1,"color":"red","duration_ms":500,"interval_ms":1000}`),
		"empty":                    document(""),
		"too many":                 document(strings.Repeat(redRow+",", 32) + redRow),
		"wrong contract":           bytes.Replace(document(redRow), []byte(Contract), []byte("nb1-light-action/v1"), 1),
		"wrong space":              bytes.Replace(document(redRow), []byte(ColorSpace), []byte("linear-rgb"), 1),
		"repeat in content":        []byte(`{"contract":"nb1-device-sequence/draft-1","color_space":"device-rgb8","lines":[` + redRow + `],"repeat":1}`),
		"trailing":                 append(document(redRow), []byte(` {}`)...),
		"utf8":                     append(document(redRow), 0xff),
		"size":                     []byte(strings.Repeat(" ", MaxBytes+1)),
		"depth":                    document(strings.Repeat("[", 17) + "0" + strings.Repeat("]", 17)),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := CompileJSON(data)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected typed rejection, got %v", err)
			}
			var detail *InputError
			if !errors.As(err, &detail) || detail.Path == "" {
				t.Fatal(err)
			}
		})
	}
}

func TestWireDurationBoundaryDoesNotBecomeHardwareQualification(t *testing.T) {
	input := document(`{"led":1,"color":"#010203","duration_ms":655350}`)
	got, e := PlanPlacementJSON(input, RAM, 31, reference())
	if e != nil || got.Rows[0].DurationTicks != 65535 || got.Rows[0].RGB8 != ([3]byte{1, 2, 3}) || !hasIssue(got.Issues, "firmware_duration_unverified") || got.Deployable {
		t.Fatalf("%+v %v", got, e)
	}
}

func TestPlacementSeparatesMemoryAndPhysicalQualification(t *testing.T) {
	input := document(strings.Repeat(redRow+",", 20) + blueRow)
	ram, err := PlanPlacementJSON(input, RAM, 0, reference())
	if err != nil {
		t.Fatal(err)
	}
	flash, err := PlanPlacementJSON(input, Flash, 0, reference())
	if err != nil {
		t.Fatal(err)
	}
	if ram.CapacityStatus != "fits" || flash.CapacityStatus != "exceeded" || ram.RequiredLines != 21 || *ram.AvailableLines != 32 || *flash.AvailableLines != 16 {
		t.Fatalf("ram=%+v flash=%+v", ram, flash)
	}
	if ram.Deployable || flash.Deployable || !hasIssue(flash.Issues, "persistence_plan_required") || !hasIssue(ram.Issues, "operation_unverified") {
		t.Fatal("encoding promoted into execution permission")
	}
	if ram.Rows[0] != (EncodedRow{0, 0, [3]byte{255, 0, 0}, 50}) || ram.Rows[20] != (EncodedRow{20, 2, [3]byte{0, 0, 255}, 50}) {
		t.Fatal(ram.Rows)
	}
	if ram.SequenceDigest != flash.SequenceDigest {
		t.Fatal("storage choice changed content identity")
	}
	unknown := reference()
	unknown.UsableFlashLines = nil
	unknown.Applicability = Unresolved
	unknown.Firmware = 206
	p, err := PlanPlacementJSON(document(redRow), Flash, 0, unknown)
	if err != nil {
		t.Fatal(err)
	}
	if p.AvailableLines != nil || p.CapacityStatus != Unknown {
		t.Fatal("physical Flash was guessed as usable", p)
	}
	unknown.RAMLines = nil
	unknown.Applicability = Unknown
	p, err = PlanPlacementJSON(document(redRow), RAM, 0, unknown)
	if err != nil || p.CapacityStatus != Unknown || p.Deployable {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestPlacementBoundsDoNotWrapFirmwareAddresses(t *testing.T) {
	for _, start := range []int{-1, 32, 1 << 30} {
		t.Run(strconv.Itoa(start), func(t *testing.T) {
			p, e := PlanPlacementJSON(document(redRow), RAM, start, reference())
			if e != nil || p.Range != nil || len(p.Rows) != 0 || !hasIssue(p.Issues, "range_out_of_bounds") {
				t.Fatalf("%+v %v", p, e)
			}
		})
	}
	p, e := PlanPlacementJSON(document(redRow+","+blueRow), RAM, 31, reference())
	if e != nil || p.Range != nil || len(p.Rows) != 0 {
		t.Fatalf("%+v %v", p, e)
	}
	p, e = PlanPlacementJSON(document(redRow), RAM, 31, reference())
	if e != nil || p.Range == nil || p.Rows[0].Position != 31 {
		t.Fatalf("%+v %v", p, e)
	}
	c := reference()
	c.MaxDurationMS = pointer(100)
	p, e = PlanPlacementJSON(document(redRow), RAM, 0, c)
	if e != nil || !hasIssue(p.Issues, "firmware_duration_exceeded") || p.Deployable {
		t.Fatalf("%+v %v", p, e)
	}
	if _, e = PlanPlacementJSON(document(redRow), "auto", 0, c); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}

func TestUploadLineZeroDoesNotPretendItCanPlayAlone(t *testing.T) {
	p, e := PlanPlacementJSON(document(redRow), RAM, 0, reference())
	if e != nil || p.CapacityStatus != "fits" || hasIssue(p.Issues, "single_line_zero_range_ambiguous") {
		t.Fatalf("%+v %v", p, e)
	}
	play, e := PlanPlayback(Range{0, 1}, Repeat{"count", pointer(1)}, reference())
	if e != nil || play.Representable || play.InclusiveEnd != nil || !hasIssue(play.Issues, "single_line_zero_range_ambiguous") {
		t.Fatalf("%+v %v", play, e)
	}
	for _, count := range []int{1, 255} {
		play, e = PlanPlayback(Range{1, 1}, Repeat{"count", pointer(count)}, reference())
		if e != nil || !play.Representable || *play.InclusiveEnd != 1 || int(*play.WireCount) != count || play.Deployable {
			t.Fatalf("%+v %v", play, e)
		}
	}
	play, e = PlanPlayback(Range{0, 32}, Repeat{Mode: "forever"}, reference())
	if e != nil || !play.Representable || *play.InclusiveEnd != 31 || *play.WireCount != 0 {
		t.Fatalf("%+v %v", play, e)
	}
	for _, r := range []Repeat{{"count", nil}, {"count", pointer(0)}, {"count", pointer(256)}, {"forever", pointer(1)}, {"auto", nil}} {
		play, e = PlanPlayback(Range{1, 1}, r, reference())
		if e != nil || play.Representable || play.WireCount != nil {
			t.Fatalf("%+v %v", play, e)
		}
	}
}

func TestProfilesAndReturnedOwnership(t *testing.T) {
	for _, change := range []func(*Capabilities){func(c *Capabilities) { c.RAMLines = pointer(33) }, func(c *Capabilities) { c.UsableFlashLines = pointer(17) }, func(c *Capabilities) { c.FlashLines = nil }, func(c *Capabilities) { c.MaxDurationMS = pointer(655360) }, func(c *Capabilities) { c.Applicability = "yes" }, func(c *Capabilities) { c.ReferenceFirmware = 205 }, func(c *Capabilities) { c.Sources = nil }} {
		c := reference()
		change(&c)
		if _, e := PlanPlacementJSON(document(redRow), RAM, 0, c); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
	c := reference()
	p, e := PlanPlacementJSON(document(redRow), RAM, 0, c)
	if e != nil {
		t.Fatal(e)
	}
	*p.AvailableLines = 1
	*p.PhysicalFlashLines = 1
	if *c.RAMLines != 32 || *c.FlashLines != 16 {
		t.Fatal("profile storage aliases result")
	}
	r := Repeat{"count", pointer(3)}
	q, e := PlanPlayback(Range{1, 1}, r, c)
	if e != nil {
		t.Fatal(e)
	}
	*q.Repeat.Count = 7
	if *r.Count != 3 {
		t.Fatal("repeat aliases caller")
	}
}
