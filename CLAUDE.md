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
1. **Extraction** — agent uses tools (http_fetch, cloudflare) to get raw content
2. **Cleanup** — single LLM call cleans the markdown

YouTube, Twitter, and PDF URLs bypass the agent pipeline with dedicated handlers. PDFs are always parsed via Reducto.

Key packages:
- `internal/agent` — agent loop and prompts
- `internal/tool` — Tool interface, registry, and tool implementations
- `internal/archive` — on-disk archive format (frontmatter + markdown)
- `internal/images` — image downloading and path rewriting

## Environment Variables

All environment variables are prefixed with `AA_`.

- `AA_ANTHROPIC_API_KEY` (required)
- `AA_ARCHIVE_DIR` (optional, default `./archive`, also settable via `-archive-dir` flag)
- `AA_CLOUDFLARE_API_TOKEN` + `AA_CLOUDFLARE_ACCOUNT_ID` (required)
- `AA_FIRECRAWL_API_KEY` (required) — backup scraper the agent uses when Cloudflare results are incomplete
- `AA_REDUCTO_API_KEY` (required) — used to parse PDFs into markdown
- `AA_CLEANUP_MODEL` (optional, default `claude-opus-4-7`, also `-cleanup-model`) — model for the final cleanup pass (web + PDF). The agent loop and summaries use the regular `-model` (default Sonnet 4.6).
- `AA_EXA_API_KEY` (required)
- `AA_X_BEARER_TOKEN` (required)
- `AA_ELEVENLABS_API_KEY` (required)
- yt-dlp + ffmpeg: required, must be in PATH
