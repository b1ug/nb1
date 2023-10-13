package util

import (
	"fmt"
	"os"

	"bitbucket.org/ai69/amoy"
	cc "bitbucket.org/ai69/colorcode"
)

// PrintJSON prints JSON data with syntax highlighting.
func PrintJSON(data interface{}) {
	fmt.Println(cc.JSON(amoy.ToJSON(data)))
}

// PrintLabelJSON prints a label and JSON data with syntax highlighting.
func PrintLabelJSON(label string, data interface{}) {
	fmt.Println(amoy.StyleLabeledLineBreak("JSON: " + label))
	fmt.Println(cc.JSON(amoy.ToJSON(data)))
}

// StderrPrintln likes fmt.Println but use stderr as the output.
func StderrPrintln(a ...interface{}) (n int, err error) {
	return fmt.Fprintln(os.Stderr, a...)
}

// StderrPrintf likes fmt.Printf but use stderr as the output.
func StderrPrintf(format string, a ...interface{}) (n int, err error) {
	return fmt.Fprintf(os.Stderr, format, a...)
}
