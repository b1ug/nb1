package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func preserveResultData(t *testing.T) {
	t.Helper()
	oldJSONPath, oldTextPath, oldOutputPath := outputJSONPath, outputTextPath, outputPath
	oldJSONData, oldTextLine := saveJSONData, saveTextLine
	t.Cleanup(func() {
		outputJSONPath, outputTextPath, outputPath = oldJSONPath, oldTextPath, oldOutputPath
		saveJSONData, saveTextLine = oldJSONData, oldTextLine
	})
}

func TestSaveResultData(t *testing.T) {
	for _, tc := range []struct {
		name string
		json bool
		text bool
	}{
		{name: "JSON only", json: true},
		{name: "text only", text: true},
		{name: "JSON and text", json: true, text: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preserveResultData(t)
			dir := t.TempDir()
			jsonPath, textPath := filepath.Join(dir, "state.json"), filepath.Join(dir, "state.txt")
			outputJSONPath, outputTextPath = "", ""
			outputPath = filepath.Join(dir, "unrelated-conversion-output")
			if tc.json {
				outputJSONPath = jsonPath
			}
			if tc.text {
				outputTextPath = textPath
			}
			want := map[string]string{"LED1": "#FF0000", "LED2": "#0000FF"}
			saveJSONData = want
			saveTextLine = []string{"LED1: #FF0000", "LED2: #0000FF"}

			if err := saveResultData(nil, nil); err != nil {
				t.Fatal(err)
			}
			if tc.json {
				data, err := os.ReadFile(jsonPath)
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]string
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("JSON = %v, want %v", got, want)
				}
			}
			if tc.text {
				data, err := os.ReadFile(textPath)
				if err != nil {
					t.Fatal(err)
				}
				if got, want := string(data), "LED1: #FF0000\nLED2: #0000FF\n"; got != want {
					t.Errorf("text = %q, want %q", got, want)
				}
			}
			if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
				t.Errorf("unrelated conversion output was used: %v", err)
			}
		})
	}
}

func TestSaveResultDataWriteError(t *testing.T) {
	for _, format := range []string{"JSON", "text", "text after JSON"} {
		t.Run(format, func(t *testing.T) {
			preserveResultData(t)
			dir := t.TempDir()
			badPath := filepath.Join(dir, "missing", "state")
			outputJSONPath, outputTextPath, outputPath = "", "", ""
			saveJSONData = map[string]string{"LED1": "#FF0000"}
			saveTextLine = []string{"LED1: #FF0000"}
			if format == "JSON" {
				outputJSONPath = badPath
			} else {
				outputTextPath = badPath
				if format == "text after JSON" {
					outputJSONPath = filepath.Join(dir, "state.json")
				}
			}

			err := saveResultData(nil, nil)
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) || pathErr.Path != badPath {
				t.Fatalf("error = %v, want write error for %q", err, badPath)
			}
			if format == "text after JSON" {
				if _, err := os.Stat(outputJSONPath); err != nil {
					t.Errorf("JSON was not written before text failed: %v", err)
				}
			}
		})
	}
}
