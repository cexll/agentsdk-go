# Environment Quickstart for Examples

- Copy the template: `cp .env.example .env` (the resulting `.env` stays ignored, no secrets in git).
- Fill `ANTHROPIC_API_KEY` in `.env`; adjust `AGENTSDK_MODEL` or `AGENTSDK_HTTP_ADDR` only if you need overrides.
- Load the values when running examples:
  - One-time per shell: `source .env`
  - Inline per command (no shell state): `env $(cat .env | xargs) go run .`
- What each example reads:
  - `examples/01-basic`: `ANTHROPIC_API_KEY`, optional `AGENTSDK_MODEL`.
  - `examples/02-cli`: same as 01-basic.
  - `examples/03-http`: `ANTHROPIC_API_KEY`, `AGENTSDK_HTTP_ADDR` (default :8080), optional `AGENTSDK_MODEL`.
  - `examples/04-advanced`: `ANTHROPIC_API_KEY`, optional `AGENTSDK_MODEL`.
