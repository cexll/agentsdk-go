package api

import (
	"context"
	"errors"
	"testing"
	_ "unsafe"

	"github.com/cexll/agentsdk-go/pkg/mcp"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

//go:linkname patchedNewMCPClient github.com/cexll/agentsdk-go/pkg/tool.newMCPClient
var patchedNewMCPClient func(ctx context.Context, spec string) (*mcp.ClientSession, error)

type mcpCallCounter struct {
	calls int
	err   error
}

func (c *mcpCallCounter) dial(ctx context.Context, spec string) (*mcp.ClientSession, error) {
	c.calls++
	return nil, c.err
}

func TestRegisterMCPServersNotBlockedByBuiltinWhitelist(t *testing.T) {
	orig := patchedNewMCPClient
	counter := &mcpCallCounter{err: errors.New("dial error")}
	patchedNewMCPClient = counter.dial
	defer func() { patchedNewMCPClient = orig }()

	reg := tool.NewRegistry()
	// Builtins disabled; MCP should still attempt registration.
	if _, err := registerTools(reg, Options{ProjectRoot: t.TempDir(), EnabledBuiltinTools: []string{}}, nil, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
	}

	err := registerMCPServers(context.Background(), reg, nil, []string{"stdio://dummy"})
	if err == nil || !errors.Is(err, counter.err) {
		t.Fatalf("expected dial error propagated, got %v", err)
	}
	if counter.calls != 1 {
		t.Fatalf("expected MCP dial invoked once, got %d", counter.calls)
	}
}
