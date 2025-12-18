package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
)

type prepareGateProbe struct {
	started chan struct{}
	unblock chan struct{}
	once    sync.Once
}

func newPrepareGateProbe() *prepareGateProbe {
	return &prepareGateProbe{
		started: make(chan struct{}, 2),
		unblock: make(chan struct{}),
	}
}

func (p *prepareGateProbe) Handler(ctx context.Context, _ commands.Invocation) (commands.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.started <- struct{}{}
	select {
	case <-p.unblock:
		return commands.Result{}, nil
	case <-ctx.Done():
		return commands.Result{}, ctx.Err()
	}
}

func (p *prepareGateProbe) Unblock() {
	if p == nil {
		return
	}
	p.once.Do(func() { close(p.unblock) })
}

func TestRunSerializesPreparePerSession(t *testing.T) {
	probe := newPrepareGateProbe()
	t.Cleanup(probe.Unblock)

	root := newClaudeProject(t)
	rt, err := New(context.Background(), Options{
		ProjectRoot:         root,
		Model:               staticOKModel{content: "ok"},
		EnabledBuiltinTools: []string{},
		RulesEnabled:        ptrBool(false),
		Commands: []CommandRegistration{{
			Definition: commands.Definition{Name: "probe"},
			Handler:    commands.HandlerFunc(probe.Handler),
		}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	sessionID := "sess"
	firstDone := make(chan error, 1)
	go func() {
		_, err := rt.Run(context.Background(), Request{Prompt: "/probe\nfirst", SessionID: sessionID})
		firstDone <- err
	}()
	waitSignals(t, probe.started, 1)

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := rt.Run(ctx, Request{Prompt: "/probe\nsecond", SessionID: sessionID})
		secondDone <- err
	}()
	<-secondStarted

	select {
	case <-probe.started:
		t.Fatal("second Run executed prepare-stage commands before session gate")
	case <-time.After(200 * time.Millisecond):
	}

	probe.Unblock()

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Run failed: %v", err)
		}
	case <-timer.C:
		t.Fatal("timed out waiting for first Run to finish")
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second Run failed: %v", err)
		}
	case <-timer.C:
		t.Fatal("timed out waiting for second Run to finish")
	}
}
