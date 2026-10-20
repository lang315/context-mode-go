package mcpserver

import (
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TextResult wraps text as a single text content block. Invalid UTF-8 from
// subprocess output is replaced with U+FFFD, as Node's string decoding did.
func TextResult(text string, isError bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: strings.ToValidUTF8(text, "�")}},
		IsError: isError,
	}
}

// ErrorResult is TextResult with IsError set.
func ErrorResult(format string, a ...any) *mcp.CallToolResult {
	return TextResult(fmt.Sprintf(format, a...), true)
}

// ArgError reports invalid arguments as a tool error the model can read.
func ArgError(err error) *mcp.CallToolResult {
	return ErrorResult("Invalid arguments: %v", err)
}

// KB formats a byte count like TS `(n / 1024).toFixed(1) + "KB"`.
func KB(n int) string {
	return fmt.Sprintf("%.1fKB", float64(n)/1024)
}
