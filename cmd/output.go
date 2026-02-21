package cmd

import (
	"encoding/json"
	"fmt"
	"os"
)

// OutputText prints formatted text to stdout
func OutputText(format string, a ...interface{}) {
	fmt.Printf(format, a...)
}

// OutputJSON prints data as JSON to stdout
func OutputJSON(data interface{}) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// Output handles text or JSON output based on format flag
func Output(data interface{}, textFormat string, textArgs ...interface{}) error {
	if outputFormat == "json" {
		return OutputJSON(data)
	}
	OutputText(textFormat, textArgs...)
	return nil
}

// OutputError prints error message to stderr
func OutputError(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
}

// OutputList prints a list with bullets
func OutputList(items []string) {
	for _, item := range items {
		fmt.Printf("  • %s\n", item)
	}
}