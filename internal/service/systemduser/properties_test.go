package systemduser

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestMachinePropertyParserRejectsUnknownAndContradictoryInput(t *testing.T) {
	// Fixture is literal machine output, not localized human status text.
	good := "Id=nb1.service\nLoadState=not-found\nActiveState=inactive\nSubState=dead\nFragmentPath=\nSourcePath=\nDropInPaths=\nTransient=no\nNeedDaemonReload=no\nUnitFileState=\nJob=\n"
	p, err := parseProperties(good)
	if err != nil || !absentProperties(p) {
		t.Fatalf("valid no-job machine output: %+v %v", p, err)
	}
	for _, text := range []string{strings.Replace(good, "Transient=no", "Transient=maybe", 1), strings.Replace(good, "Job=\n", "Job=01\n", 1), strings.Replace(good, "Id=nb1.service", "Id=foreign.service", 1), good + "Id=nb1.service\n", strings.Replace(good, "Job=\n", "Job=garbage\n", 1), "● nb1.service - human status\n", strings.Replace(good, "LoadState=not-found", "LoadState=not-found\r", 1)} {
		if _, err := parseProperties(text); err == nil {
			t.Fatalf("accepted ambiguous machine payload: %q", text)
		}
	}
	p.ActiveState = "active"
	if absentProperties(p) {
		t.Fatal("active unit considered absent")
	}
}

func TestBoundedOutputDoesNotBlockProducer(t *testing.T) {
	var output boundedOutput
	data := []byte(strings.Repeat("x", maxOutput+1024))
	n, err := output.Write(data)
	if err != nil || n != len(data) || !output.truncated || len(output.data) != maxOutput {
		t.Fatalf("unbounded/short capture: %d %v", n, err)
	}
	if _, err := output.Write([]byte("tail")); err != nil {
		t.Fatal(err)
	}
}

func TestProductionConstructorIsUnsupportedOutsideLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("real Linux user-manager construction is integration scope")
	}
	if _, err := New(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("non-Linux manager activated: %v", err)
	}
}
