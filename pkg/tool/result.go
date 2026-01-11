package tool

// OutputRef describes where tool output has been persisted when it is too large
// (or otherwise undesirable) to embed directly in ToolResult.Output.
type OutputRef struct {
	Path      string `json:"path,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ToolResult captures the outcome of a tool invocation.
type ToolResult struct {
	Success bool
	Output  string
	OutputRef *OutputRef
	Data    interface{}
	Error   error
}
