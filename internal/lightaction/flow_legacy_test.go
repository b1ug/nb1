package lightaction

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
)

// Frozen before the experimental extension, from baseline 5aaccf6. The whole
// Result JSON is bound, including field omission/order and normalized defaults.
func TestFlowPreservesLegacyActionBytes(t *testing.T) {
	fixtures := map[string]string{
		`{"kind":"set","color":"red"}`:                     "1a0afc42a769c66e88d1499cc591ddfb8426361d84d2153c776c95dfef45d935",
		`{"kind":"off"}`:                                   "44cf689264921877596d8082f59d54bae95b83ec976023d3c0b857583e83dd0f",
		`{"kind":"fade","color":"#abc","duration_ms":100}`: "0d641957ccb85f56763415a05cdb6f7726539baa59baf82cfc2bf1eca6dbcaeb",
		`{"kind":"blink","color":"lime"}`:                  "ee408efb80edd3d835695d80d6b9cb9fc91271c3bb32ac79bc2a2d7414deaca9",
		`{"kind":"breathe","color":"white"}`:               "af2e62237b923d0eb80956435bf97ff517f284164f58e8377eefffc3340b7c8c",
		`{"kind":"pulse","color":"gold"}`:                  "6d1a396fd65f08005916769755e0cd39d4f5362ab15732bec3f507c7187b691c",
		`{"kind":"alternate"}`:                             "328e0c14d7a8888707155303a9341268e8ba6b40222c65aa6fbd44c1bcdb5c97",
		`{"kind":"rainbow"}`:                               "90e1cb66b858ea033883fbcda5a970ea5c030000eb34c194da54e277b4856241",
		`{"kind":"alternate","period_ms":41,"leds":[2,1],"colors":["#abc","blue"]}`: "4e888d09863034a52f90bae3b6acb75ebd9c973eccdc3a3cd053098eb9613066",
	}
	for input, want := range fixtures {
		result, err := CompileJSON([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(encoded)); got != want {
			t.Fatalf("old result changed for %s: got %s want %s", input, got, want)
		}
	}
}
