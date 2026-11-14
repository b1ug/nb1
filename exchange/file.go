package exchange

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

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
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, data)
}

// SaveAsLine saves lines of text to the given path.
func SaveAsLine(data []string, path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	return writeAndCloseLines(file, data)
}

func writeAndCloseLines(file io.WriteCloser, data []string) error {
	writeErr := writeLines(file, data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// LoadFromLine loads lines of text from the given path.
func LoadFromLine(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readLines(file)
}

func readLines(input io.Reader) ([]string, error) {
	var lines []string
	reader := bufio.NewReader(input)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return lines, err
		}
		if len(line) > 0 {
			if strings.HasSuffix(line, "\n") {
				line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			}
			lines = append(lines, line)
		}
		if err == io.EOF {
			return lines, nil
		}
	}
}

func writeLines(output io.Writer, lines []string) error {
	writer := bufio.NewWriter(output)
	for _, line := range lines {
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return writer.Flush()
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
