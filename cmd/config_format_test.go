package cmd

import "testing"

func TestLegacyConfigListFormattingGolden(t *testing.T) {
	values := configKeyValuePairList{
		{Key: "label", Value: "<lamp>&灯"},
		{Key: "device", Value: "20005601"},
		{Key: "enabled", Value: true},
		{Key: "missing", Value: nil},
		{Key: "nested", Value: map[string]interface{}{"z": 2, "a": "line\nnext"}},
		{Key: "unsupported", Value: make(chan int)},
	}
	values.Sort()
	const want = "device=\"20005601\"\nenabled=true\nlabel=\"<lamp>&灯\"\nmissing=null\nnested={\"a\":\"line\\nnext\",\"z\":2}\nunsupported={}\n"
	if got := values.String(); got != want {
		t.Fatalf("legacy config list output changed:\ngot  %q\nwant %q", got, want)
	}
}
