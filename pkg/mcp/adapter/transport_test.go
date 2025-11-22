package adapter

import (
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBuildTransportStdioVariants(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		spec     string
		expected []string
	}{
		{name: "ExplicitPrefix", spec: "stdio://echo hello", expected: []string{"echo", "hello"}},
		{name: "DefaultCommand", spec: "./server --flag value", expected: []string{"./server", "--flag", "value"}},
		{name: "UppercasePrefix", spec: "STDIO://python main.py", expected: []string{"python", "main.py"}},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr, err := buildTransport(nil, tc.spec)
			if err != nil {
				t.Fatalf("buildTransport returned error: %v", err)
			}
			cmdTr, ok := tr.(*mcpsdk.CommandTransport)
			if !ok {
				t.Fatalf("transport is %T, want *CommandTransport", tr)
			}
			if len(cmdTr.Command.Args) != len(tc.expected) {
				t.Fatalf("command args mismatch: got %v want %v", cmdTr.Command.Args, tc.expected)
			}
			for i, arg := range tc.expected {
				if cmdTr.Command.Args[i] != arg {
					t.Fatalf("arg[%d] mismatch: got %q want %q", i, cmdTr.Command.Args[i], arg)
				}
			}
		})
	}
}

func TestBuildTransportSSEVariants(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		spec string
		want string
	}{
		{name: "HTTPDefault", spec: "http://mcp.example/api", want: "http://mcp.example/api"},
		{name: "HTTPSUppercase", spec: "HTTPS://Example.com/api?trace=1", want: "https://Example.com/api?trace=1"},
		{name: "SSEShorthandAddsScheme", spec: "sse://mcp.example/tools", want: "https://mcp.example/tools"},
		{name: "SSEHint", spec: "http+sse://mcp.example/tools", want: "http://mcp.example/tools"},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr, err := buildTransport(nil, tc.spec)
			if err != nil {
				t.Fatalf("buildTransport returned error: %v", err)
			}
			sseTr, ok := tr.(*mcpsdk.SSEClientTransport)
			if !ok {
				t.Fatalf("transport is %T, want *SSEClientTransport", tr)
			}
			if sseTr.Endpoint != tc.want {
				t.Fatalf("unexpected endpoint: got %q want %q", sseTr.Endpoint, tc.want)
			}
		})
	}
}

func TestBuildTransportHTTPHints(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		spec string
		want string
	}{
		{name: "StreamHint", spec: "http+stream://api.example/mcp", want: "http://api.example/mcp"},
		{name: "JSONHintUppercase", spec: "HTTPS+JSON://api.example/mcp?mode=stream", want: "https://api.example/mcp?mode=stream"},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr, err := buildTransport(nil, tc.spec)
			if err != nil {
				t.Fatalf("buildTransport returned error: %v", err)
			}
			httpTr, ok := tr.(*mcpsdk.StreamableClientTransport)
			if !ok {
				t.Fatalf("transport is %T, want *StreamableClientTransport", tr)
			}
			if httpTr.Endpoint != tc.want {
				t.Fatalf("unexpected endpoint: got %q want %q", httpTr.Endpoint, tc.want)
			}
		})
	}
}

func TestBuildTransportInvalidSpecs(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{name: "HTTPMissingHost", spec: "http://", wantErr: "missing host"},
		{name: "SSEMissingHost", spec: "sse://", wantErr: "endpoint is empty"},
		{name: "SSEUnsupportedScheme", spec: "sse://ftp://example.com", wantErr: "unsupported scheme"},
		{name: "HTTPHintMissingHost", spec: "http+stream://", wantErr: "missing host"},
		{name: "HTTPHintUnsupported", spec: "http+foo://api.example/mcp", wantErr: "unsupported HTTP transport hint"},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := buildTransport(nil, tc.spec); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
