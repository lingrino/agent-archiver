# agent-archiver

CLI tool that uses an LLM agent to archive web content into clean markdown.

## Build & Test

```bash
go mod tidy
go build -o agent-archiver .
go test -race -v ./...
golangci-lint run
```

## Architecture

Two-phase agent pipeline:
1. **Extraction** — agent uses tools (http_fetch, cloudflare, trafilatura) to get raw content
2. **Cleanup** — single LLM call cleans the markdown

Key packages:
- `internal/agent` — agent loop and prompts
- `internal/tool` — Tool interface, registry, and tool implementations
- `internal/archive` — on-disk archive format (frontmatter + markdown)
- `internal/images` — image downloading and path rewriting

## Environment Variables

All environment variables are prefixed with `AA_`.

- `AA_ANTHROPIC_API_KEY` (required)
- `AA_ARCHIVE_DIR` (optional, default `./archive`, also settable via `-archive-dir` flag)
- `AA_CLOUDFLARE_API_TOKEN` + `AA_CLOUDFLARE_ACCOUNT_ID` (optional, enables Cloudflare tools)
- `AA_EXA_API_KEY` (optional, enables web search for richer summaries)
- `AA_X_BEARER_TOKEN` (optional, enables Twitter/X API)
- Trafilatura: auto-detected if binary is in PATH
