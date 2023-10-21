package exchange

import (
	"encoding/json"
	"io/ioutil"

	"bitbucket.org/ai69/amoy"
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
