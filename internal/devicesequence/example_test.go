package devicesequence

import "fmt"

func ExampleCompileJSON() {
	result, err := CompileJSON([]byte(`{"contract":"nb1-device-sequence/draft-1","color_space":"device-rgb8","lines":[{"led":"all","color":"red","duration_ms":500}]}`))
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Sequence.Lines[0].Color, result.Sequence.Lines[0].DurationMS)
	// Output: #ff0000 500
}

func ExampleInspectSnapshot() {
	result, err := InspectSnapshot(Snapshot{RequestedRange: Range{0, 1}, Complete: true, Rows: []RowObservation{{Position: 0, Layout: GIDStandardLayout, ReadAttempted: true, ReturnedLength: 8, Response: []byte{1, 'R', 0, 0, 0, 0, 0, 0}}}})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Snapshot.Complete, result.Sequence == nil, result.DecodingStatus)
	// Output: true true unrepresentable
}
