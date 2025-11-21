package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/config"
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

type runConfig struct {
	prompt            string
	sessionID         string
	owner             string
	projectRoot       string
	enableHooks       bool
	enableMCP         bool
	mcpServer         string
	enableSandbox     bool
	sandboxRoot       string
	allowHost         string
	cpuLimit          float64
	memLimit          uint64
	diskLimit         uint64
	enableSkills      bool
	enableSubagents   bool
	enableCommands    bool
	enablePlugins     bool
	enableTrace       bool
	traceDir          string
	slowThreshold     time.Duration
	toolLatency       time.Duration
	runTimeout        time.Duration
	middlewareTimeout time.Duration
	maxIterations     int
	rps               int
	burst             int
	concurrent        int
	hookTimeout       time.Duration
	forceSkill        string
	targetSubagent    string
	severity          string
}

func main() {
	cfg := parseConfig()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	projectRoot := cfg.projectRoot
	if projectRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			log.Fatalf("resolve working dir: %v", err)
		}
		projectRoot = wd
	}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		log.Fatalf("resolve project root: %v", err)
	}

	settingsOverride := &config.Settings{Env: map[string]string{"ADVANCED_EXAMPLE": "true"}, EnabledPlugins: map[string]bool{"advanced-sample": true}}

	mw := buildMiddlewares(cfg, logger)
	hooks := buildHooks(logger, cfg.hookTimeout)

	opts := api.Options{
		EntryPoint:        api.EntryPointCLI,
		Mode:              api.ModeContext{EntryPoint: api.EntryPointCLI},
		ProjectRoot:       absRoot,
		SettingsLoader:    &config.SettingsLoader{ProjectRoot: absRoot},
		SettingsOverrides: settingsOverride,
		Model:             newDemoModel(absRoot, cfg.owner, cfg.enableMCP, settingsOverride),
		Tools:             []tool.Tool{newObserveLogsTool(cfg.toolLatency, logger, settingsOverride)},
		Middleware:        mw.items,
		MiddlewareTimeout: cfg.middlewareTimeout,
		MaxIterations:     cfg.maxIterations,
		Timeout:           cfg.runTimeout,
		MCPServers:        buildMCPServers(cfg, logger),
	}

	if cfg.enableHooks {
		opts.TypedHooks = hooks.handlers
		opts.HookMiddleware = hooks.mw
		opts.HookTimeout = cfg.hookTimeout
	}

	if cfg.enableSkills {
		opts.Skills = buildSkills()
	}

	if cfg.enableCommands {
		opts.Commands = buildCommands()
	}

	if cfg.enableSubagents {
		opts.Subagents = buildSubagents()
	}

	if cfg.enableSandbox {
		opts.Sandbox = buildSandboxOptions(cfg, absRoot)
	}

	if cfg.enablePlugins {
		// Plugin discovery happens automatically off ProjectRoot
	} else {
		opts.SettingsOverrides.EnabledPlugins = map[string]bool{}
	}

	rt, err := api.New(ctx, opts)
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	prompt := cfg.prompt
	if cfg.enableCommands {
		prompt = prompt + "\n" + demoScript()
	}

	req := api.Request{
		Prompt:    prompt,
		SessionID: cfg.sessionID,
		Metadata: map[string]any{
			"owner": cfg.owner,
		},
		Tags:     map[string]string{"env": "prod", "severity": cfg.severity},
		Channels: []string{"cli", "slack"},
		Traits:   []string{"fast"},
	}
	if cfg.forceSkill != "" {
		req.ForceSkills = []string{cfg.forceSkill}
	}
	if cfg.targetSubagent != "" {
		req.TargetSubagent = cfg.targetSubagent
	}

	resp, err := rt.Run(ctx, req)
	if err != nil {
		log.Fatalf("run agent: %v", err)
	}

	printSummary(resp, cfg, mw, hooks)
}

func parseConfig() runConfig {
	var cfg runConfig
	var memMB, diskMB int
	flag.StringVar(&cfg.prompt, "prompt", "生成一份安全巡检摘要并标注下一步", "user prompt for the demo")
	flag.StringVar(&cfg.sessionID, "session-id", "advanced-demo", "session identifier")
	flag.StringVar(&cfg.owner, "owner", "advanced-example", "logical owner for logging")
	flag.StringVar(&cfg.projectRoot, "project-root", "", "project root; defaults to current directory")
	flag.BoolVar(&cfg.enableHooks, "enable-hooks", true, "enable hooks executor")
	flag.BoolVar(&cfg.enableMCP, "enable-mcp", false, "register MCP time server via uvx")
	flag.StringVar(&cfg.mcpServer, "mcp-server", "", "custom MCP server spec")
	flag.BoolVar(&cfg.enableSandbox, "enable-sandbox", true, "attach sandbox manager")
	flag.StringVar(&cfg.sandboxRoot, "sandbox-root", "", "sandbox root (default project root)")
	flag.StringVar(&cfg.allowHost, "allow-host", "example.com", "allowed host for sandbox demo")
	flag.Float64Var(&cfg.cpuLimit, "cpu-limit", 50, "maximum CPU percent")
	flag.IntVar(&memMB, "mem-mb", 128, "memory limit in MB")
	flag.IntVar(&diskMB, "disk-mb", 16, "disk limit in MB")
	flag.BoolVar(&cfg.enableSkills, "enable-skills", true, "enable skills registry")
	flag.BoolVar(&cfg.enableSubagents, "enable-subagents", true, "enable subagent dispatcher")
	flag.BoolVar(&cfg.enableCommands, "enable-commands", true, "enable slash commands parser")
	flag.BoolVar(&cfg.enablePlugins, "enable-plugins", true, "discover .claude-plugin")
	flag.BoolVar(&cfg.enableTrace, "enable-trace", true, "record trace middleware output")
	flag.StringVar(&cfg.traceDir, "trace-dir", "trace-out", "trace output directory")
	flag.DurationVar(&cfg.slowThreshold, "slow-threshold", 250*time.Millisecond, "slow request threshold")
	flag.DurationVar(&cfg.toolLatency, "tool-latency", 150*time.Millisecond, "simulated tool latency")
	flag.DurationVar(&cfg.runTimeout, "timeout", 5*time.Second, "agent timeout")
	flag.DurationVar(&cfg.middlewareTimeout, "middleware-timeout", 2*time.Second, "per-hook timeout")
	flag.DurationVar(&cfg.hookTimeout, "hook-timeout", 500*time.Millisecond, "per-hook timeout")
	flag.IntVar(&cfg.maxIterations, "max-iterations", 3, "max agent iterations")
	flag.IntVar(&cfg.rps, "rps", 5, "token bucket refill rate per second")
	flag.IntVar(&cfg.burst, "burst", 10, "token bucket burst size")
	flag.IntVar(&cfg.concurrent, "concurrent", 2, "maximum concurrent agent runs")
	flag.StringVar(&cfg.forceSkill, "force-skill", "add_note", "force a skill to run")
	flag.StringVar(&cfg.targetSubagent, "target-subagent", "", "force a subagent name (empty=auto)")
	flag.StringVar(&cfg.severity, "severity", "high", "tag: severity level")
	flag.Parse()

	cfg.memLimit = uint64(memMB) * 1024 * 1024
	cfg.diskLimit = uint64(diskMB) * 1024 * 1024
	return cfg
}

func printSummary(resp *api.Response, cfg runConfig, mw middlewareBundle, hooks hookBundle) {
	fmt.Println("\n===== Final Output =====")
	if resp == nil || resp.Result == nil {
		fmt.Println("(no result)")
	} else {
		fmt.Println(resp.Result.Output)
		if len(resp.Result.ToolCalls) > 0 {
			fmt.Println("\nTool Calls:")
			for _, call := range resp.Result.ToolCalls {
				fmt.Printf("- %s -> %v\n", call.Name, call.Arguments)
			}
		}
	}

	if cfg.enableCommands && len(resp.CommandResults) > 0 {
		fmt.Println("\nCommands executed:")
		for _, res := range resp.CommandResults {
			fmt.Printf("/%s -> %v\n", res.Definition.Name, res.Result.Output)
		}
	}

	if cfg.enableSkills && len(resp.SkillResults) > 0 {
		fmt.Println("\nSkills executed:")
		for _, res := range resp.SkillResults {
			status := "ok"
			if res.Err != nil {
				status = res.Err.Error()
			}
			fmt.Printf("- %s -> %v (status=%s)\n", res.Definition.Name, res.Result.Output, status)
		}
	}

	if cfg.enableSubagents && resp.Subagent != nil {
		fmt.Println("\nSubagent:")
		fmt.Printf("- %s -> %v\n", resp.Subagent.Subagent, resp.Subagent.Output)
	}

	if cfg.enableHooks {
		fmt.Println("\nHook events:")
		counts := hooks.tracker.snapshot()
		if len(counts) == 0 {
			fmt.Println("(no hook events recorded)")
		}
		for evt, count := range counts {
			fmt.Printf("- %s: %d\n", evt, count)
		}
	}

	if cfg.enableSandbox {
		fmt.Println("\nSandbox:")
		fmt.Printf("roots=%v allow=%v domains=%v limits=%+v\n", resp.SandboxSnapshot.Roots, resp.SandboxSnapshot.AllowedPaths, resp.SandboxSnapshot.AllowedDomains, resp.SandboxSnapshot.ResourceLimits)
	}

	if cfg.enablePlugins {
		if plugs := pluginSummary(resp); len(plugs) > 0 {
			fmt.Println("\nPlugins:")
			for _, line := range plugs {
				fmt.Println("- " + line)
			}
		}
	}

	if cfg.enableTrace {
		fmt.Printf("\nTrace middleware wrote to: %s\n", mw.traceDir)
	}

	if mw.monitor != nil {
		total, slow, max, last := mw.monitor.Snapshot()
		fmt.Printf("\nMetrics: total=%d slow=%d max_latency=%s last_latency=%s\n", total, slow, max, last)
	}

	if len(resp.HookEvents) > 0 {
		fmt.Println("\nRecorded hook events (from runtime recorder):")
		for _, evt := range resp.HookEvents {
			fmt.Printf("- %s\n", evt.Type)
		}
	}

	fmt.Println("\nSettings env:")
	if resp.Settings != nil {
		for k, v := range resp.Settings.Env {
			fmt.Printf("- %s=%s\n", k, v)
		}
	}

	fmt.Println("\nCommand parse preview:")
	if cfg.enableCommands {
		invocations, err := commands.Parse(demoScript())
		if err == nil {
			for _, line := range dumpInvocations(invocations) {
				fmt.Println("- " + line)
			}
		}
	}
}
