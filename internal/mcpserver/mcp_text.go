package mcpserver

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// MCP's English instructions and descriptions are embedded in released binaries.
//
//go:embed mcp-text.json
var mcpTextJSON []byte

type mcpText struct {
	Instructions       string            `json:"instructions"`
	StdioInstructions  string            `json:"stdio_instructions"`
	HTTPInstructions   string            `json:"http_instructions"`
	ToolDescriptions   map[string]string `json:"tool_descriptions"`
	SchemaDescriptions map[string]string `json:"schema_descriptions"`
}

var (
	protocolText, protocolTextErr = loadMCPText()
	specifications                = makeToolSpecifications(protocolText)
)

func loadMCPText() (mcpText, error) {
	var text mcpText
	if err := json.Unmarshal(mcpTextJSON, &text); err != nil {
		return mcpText{}, fmt.Errorf("decode embedded MCP text: %w", err)
	}
	if strings.Count(text.Instructions, "{{transport}}") != 1 {
		return mcpText{}, fmt.Errorf("embedded MCP instructions must include one transport placeholder")
	}
	if strings.Count(text.StdioInstructions, "{{default_endpoint}}") != 1 || strings.TrimSpace(text.HTTPInstructions) == "" {
		return mcpText{}, fmt.Errorf("embedded MCP text must include HTTP instructions and one stdio default endpoint placeholder")
	}
	text.StdioInstructions = strings.Replace(text.StdioInstructions, "{{default_endpoint}}", defaultEndpoint, 1)
	return text, nil
}

func embeddedSchemaDescription(key string) string { return protocolText.SchemaDescriptions[key] }

func serverInstructions(transportInstructions string) (string, error) {
	if protocolTextErr != nil {
		return "", protocolTextErr
	}
	return strings.Replace(protocolText.Instructions, "{{transport}}", strings.TrimSpace(transportInstructions), 1), nil
}
