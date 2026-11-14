package util

import "testing"

func TestNormalizeQueryPreservesCaseAndUnicode(t *testing.T) {
	got := NormalizeQuery("  Fade", "to", "HSV(210, 75, 90)", "// 夜间 Desk  ")
	want := "Fade to HSV(210, 75, 90) // 夜间 Desk"
	if got != want {
		t.Fatalf("query = %q, want original spelling %q", got, want)
	}
	if got := NormalizeQuery(); got != "" {
		t.Fatalf("empty query = %q", got)
	}
}
