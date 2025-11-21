# 02-cli: Interactive REPL

Run a minimal CLI loop that keeps session history and reads optional sandbox/tool config.

Requirements:
- `ANTHROPIC_API_KEY` must be set (e.g., `export ANTHROPIC_API_KEY=sk-...`).
- Optional: `SESSION_ID` seeds `--session-id`; defaults to `demo-session`.

Launch:
```bash
go run ./examples/02-cli \
  --session-id my-session \
  --settings-path .claude/settings.json
```

Tips:
- Type `exit` to quit; only assistant replies are printed.
- `--settings-path` can point to any valid `.claude/settings.json` if you want tool/sandbox settings. Omit it to rely on the SDK's default resolution.
