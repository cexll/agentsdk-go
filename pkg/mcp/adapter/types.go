package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolDescriptor mirrors the legacy MCP tool descriptor used by agentsdk-go.
type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"input_schema"`
}

// ToolCallResult mirrors the legacy MCP tool call result schema.
type ToolCallResult struct {
	Content json.RawMessage `json:"content"`
}

// Error wraps structured MCP errors to preserve the legacy JSON-RPC contract.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if len(e.Data) == 0 {
		return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("mcp error %d: %s (%s)", e.Code, e.Message, string(e.Data))
}

// toToolDescriptor converts an official SDK tool to the legacy representation.
func toToolDescriptor(tool *mcpsdk.Tool) ToolDescriptor {
	if tool == nil {
		return ToolDescriptor{}
	}
	schemaJSON, err := json.Marshal(tool.InputSchema)
	if err != nil {
		schemaJSON = nil
	}
	return ToolDescriptor{
		Name:        tool.Name,
		Description: tool.Description,
		Schema:      schemaJSON,
	}
}

// toToolCallResult converts an official SDK CallToolResult to the legacy value.
func toToolCallResult(result *mcpsdk.CallToolResult) *ToolCallResult {
	if result == nil {
		return &ToolCallResult{}
	}
	contentJSON, err := json.Marshal(result.Content)
	if err != nil {
		contentJSON = nil
	}
	return &ToolCallResult{Content: contentJSON}
}

// convertError normalizes SDK-specific errors into the adapter Error type when possible.
func convertError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*Error); ok {
		return err
	}
	if mapped := findWireError(err); mapped != nil {
		return mapped
	}
	return err
}

// wireErrorPkgPath identifies the SDK's internal jsonrpc2 error type via reflection.
const wireErrorPkgPath = "github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"

func findWireError(err error) *Error {
	stack := []error{err}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == nil {
			continue
		}
		if mapped := tryConvertWireError(current); mapped != nil {
			return mapped
		}
		if unwrapped := errors.Unwrap(current); unwrapped != nil {
			stack = append(stack, unwrapped)
		}
		if multi, ok := current.(interface{ Unwrap() []error }); ok {
			stack = append(stack, multi.Unwrap()...)
		}
	}
	return nil
}

func tryConvertWireError(err error) *Error {
	if err == nil {
		return nil
	}
	val := reflect.ValueOf(err)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return nil
	}
	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return nil
	}
	typ := elem.Type()
	if typ.PkgPath() != wireErrorPkgPath || typ.Name() != "WireError" {
		return nil
	}
	mapped := &Error{}
	if field := elem.FieldByName("Code"); field.IsValid() && field.CanInt() {
		mapped.Code = int(field.Int())
	}
	if field := elem.FieldByName("Message"); field.IsValid() && field.Kind() == reflect.String {
		mapped.Message = field.String()
	}
	if field := elem.FieldByName("Data"); field.IsValid() && field.Kind() == reflect.Slice && field.CanInterface() {
		if data, ok := field.Interface().(json.RawMessage); ok && len(data) > 0 {
			mapped.Data = append(mapped.Data, data...)
		}
	}
	return mapped
}
