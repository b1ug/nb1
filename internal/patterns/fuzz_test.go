package patterns_test

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"

	"github.com/b1ug/nb1/internal/patterns"
)

func FuzzDecodePackage(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(authoringCue),
		[]byte(`{"schema_version":1,"patterns":[]}`),
		[]byte(`{"schema_version":1,"kind":"nb1-pattern-snapshot","records":[]}`),
		[]byte(`{"schema_version":1,"patterns":[{"id":"x","title":"\ud83d\ude00","program":{"steps":[{"color":"#AABBCC"}]}}]}`),
		[]byte(`{"schema_version":1,"patterns":[{"id":"x","title":"\ud800","effect":{"kind":"steady","color":"#000000"}}]}`),
		{0xff, 0x00},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		records, err := patterns.DecodePackage(input)
		if err != nil {
			if records != nil {
				t.Fatal("failed package exposed partial records")
			}
			return
		}
		encoded, err := patterns.EncodeSnapshot(records)
		if err != nil || len(encoded) > patterns.MaxCanonicalBytes {
			t.Fatalf("accepted input was not exportable: %v", err)
		}
		again, err := patterns.DecodePackage(encoded)
		if err != nil || !reflect.DeepEqual(records, again) {
			t.Fatalf("snapshot roundtrip changed records: %v", err)
		}
		for _, record := range records {
			canonical, err := patterns.CanonicalRecordJSON(record)
			if err != nil {
				t.Fatal(err)
			}
			preimage := append([]byte(patterns.RevisionDomain+"\n"), canonical...)
			if record.Revision != fmt.Sprintf("sha256:%x", sha256.Sum256(preimage)) {
				t.Fatal("exported record did not retain its canonical digest")
			}
		}
	})
}
