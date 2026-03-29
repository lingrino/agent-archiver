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

- `ANTHROPIC_API_KEY` (required)
- `CLOUDFLARE_API_TOKEN` + `CLOUDFLARE_ACCOUNT_ID` (optional, enables Cloudflare tools)
- Trafilatura: auto-detected if binary is in PATH
