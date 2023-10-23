package exchange

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"bitbucket.org/ai69/amoy"
	"github.com/1set/gut/yos"
	"github.com/b1ug/nb1/schema"
)

// SaveAsJSON saves data as JSON to the given path.
func SaveAsJSON(data interface{}, path string) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, bytes, 0644)
}

// LoadFromJSON loads data from JSON file.
func LoadFromJSON(data interface{}, path string) error {
	return amoy.LoadJSONFile(data, path)
}

// SaveAsLine saves lines of text to the given path.
func SaveAsLine(data []string, path string) error {
	return amoy.WriteFileLines(path, data)
}

// LoadFromLine loads lines of text from the given path.
func LoadFromLine(path string) ([]string, error) {
	return amoy.ReadFileLines(path)
}

// LoadPatternFile loads a pattern file from the given path.
func LoadPatternFile(fp string) (*schema.PatternSet, error) {
	if !yos.ExistFile(fp) {
		return nil, fmt.Errorf("file not exist: %s", fp)
	}
	ext := strings.ToLower(filepath.Ext(fp))

	// read input file
	var ps schema.PatternSet
	switch ext {
	case ".txt":
		// read lines
		lines, err := LoadFromLine(fp)
		if err != nil {
			return nil, err
		}
		// parse lines
		if ts, err := ParsePlayText(lines); err != nil {
			return nil, err
		} else if ts != nil {
			ps = *ts
		}
	case ".json":
		// just load
		if err := LoadFromJSON(&ps, fp); err != nil {
			return nil, err
		}
		ps.AutoFill()
	default:
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}

	// check pattern
	if err := ps.Validate(); err != nil {
		return nil, err
	}

	return &ps, nil
}
