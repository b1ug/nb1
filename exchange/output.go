package exchange

import (
	"encoding/json"
	"io/ioutil"
)

// SaveAsJSON saves data as JSON to the given path.
func SaveAsJSON(data interface{}, path string) error {
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, bytes, 0644)
}
