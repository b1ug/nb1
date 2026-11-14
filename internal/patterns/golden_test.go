package patterns_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/b1ug/nb1/internal/patterns"
)

// These bytes/digests came from an independent Python encoder, which did not
// read or invoke this Go codec. Decode hex instead of hashing a text file's LF
// bytes, so Git checkout newline conversion cannot alter the goldens' meaning.
func TestIndependentPythonCanonicalBytesAndRevisions(t *testing.T) {
	var golden struct {
		Version         int    `json:"schema_version"`
		Domain          string `json:"record_domain"`
		CompileContract string `json:"compile_contract"`
		CaseCount       int    `json:"case_count"`
		Cases           []struct {
			Name            string          `json:"name"`
			AuthoringJSON   string          `json:"authoring_json"`
			Record          patterns.Record `json:"record"`
			SnapshotPackage json.RawMessage `json:"snapshot_package"`
			CanonicalJSON   string          `json:"canonical_json"`
			CanonicalHex    string          `json:"canonical_hex"`
			PreimageHex     string          `json:"preimage_hex"`
			Revision        string          `json:"revision"`
		} `json:"cases"`
		Relations []struct {
			Name                 string `json:"name"`
			Left                 string `json:"left"`
			Right                string `json:"right"`
			RevisionEqual        bool   `json:"revision_equal"`
			CompiledProgramEqual bool   `json:"compiled_program_equal"`
		} `json:"relations"`
	}
	data, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	if golden.Version != 1 || golden.Domain != patterns.RevisionDomain || golden.CompileContract != patterns.CompileContract || golden.CaseCount != 16 || len(golden.Cases) != golden.CaseCount || len(golden.Relations) != 9 {
		t.Fatal("independent golden metadata changed")
	}
	actual := make(map[string]patterns.Record)
	for _, test := range golden.Cases {
		t.Run(test.Name, func(t *testing.T) {
			want, err := hex.DecodeString(test.CanonicalHex)
			if err != nil || !bytes.Equal(want, []byte(test.CanonicalJSON)) {
				t.Fatal("independent canonical text/hex differs", err)
			}
			preimage, err := hex.DecodeString(test.PreimageHex)
			if err != nil || !bytes.Equal(preimage, append([]byte(patterns.RevisionDomain+"\n"), want...)) || test.Revision != fmt.Sprintf("sha256:%x", sha256.Sum256(preimage)) {
				t.Fatal("independent Python preimage/hash is inconsistent", err)
			}
			values, err := patterns.DecodePackage([]byte(test.AuthoringJSON))
			if err != nil || len(values) != 1 || !reflect.DeepEqual(values[0], test.Record) || values[0].Revision != test.Revision {
				t.Fatalf("Go record differs from independent Python: %#v %v", values, err)
			}
			actual[test.Name] = values[0]
			canonical, err := patterns.CanonicalRecordJSON(values[0])
			if err != nil || !bytes.Equal(canonical, want) {
				t.Fatalf("canonical bytes differ\nactual: %x\nwant:   %x\nerror: %v", canonical, want, err)
			}
			frozen, err := patterns.DecodePackage(test.SnapshotPackage)
			if err != nil || !reflect.DeepEqual(values, frozen) {
				t.Fatal("independent frozen snapshot did not validate", err)
			}
			exported, err := patterns.EncodeSnapshot(values)
			if err != nil {
				t.Fatal(err)
			}
			wantSnapshot := append([]byte(`{"schema_version":1,"kind":"nb1-pattern-snapshot","records":[`), want[:len(want)-1]...)
			wantSnapshot = append(wantSnapshot, []byte(`,"revision":"`+test.Revision+`"}]}`)...)
			if !bytes.Equal(exported, wantSnapshot) {
				t.Fatalf("frozen snapshot export not canonical: %s", exported)
			}
		})
	}
	for _, relation := range golden.Relations {
		t.Run(relation.Name, func(t *testing.T) {
			left, leftOK := actual[relation.Left]
			right, rightOK := actual[relation.Right]
			if !leftOK || !rightOK || (left.Revision == right.Revision) != relation.RevisionEqual || reflect.DeepEqual(left.CompiledProgram, right.CompiledProgram) != relation.CompiledProgramEqual {
				t.Fatalf("identity relation changed: %#v", relation)
			}
		})
	}
}
