package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	reductoBaseURL = "https://platform.reducto.ai"
	pdfMaxBytes    = 200 * 1024 * 1024 // 200 MB cap on downloaded PDF size
)

// pdfExtractSystemPrompt instructs Reducto's deep_extract agent to faithfully
// convert the document into clean markdown. It mirrors the formatting rules used
// by the cleanup pass for web extractions.
const pdfExtractSystemPrompt = `You are extracting the full content of a PDF document into clean, faithful markdown. Place the entire document content in the "markdown" field as a single string.

Formatting guidelines:
- Use proper markdown heading levels for the document's structural hierarchy (start at # for the title)
- Preserve paragraphs, lists (ordered and unordered), block quotes, and tables
- Format tables as markdown tables when they fit; for complex tables, preserve their structure as faithfully as possible
- Preserve code blocks with language annotations when the language is obvious
- Preserve links, bold, italic, and other inline formatting where present
- Render mathematical equations using LaTeX-style markdown ($...$ for inline, $$...$$ for display) and preserve symbols, subscripts, and superscripts exactly
- Preserve footnotes and endnotes; reference them with markdown footnote syntax where possible
- Use clean, readable markdown formatting throughout, with at most one blank line between paragraphs

Do NOT:
- Change the meaning, wording, or order of any content
- Change capitalization, casing, or spelling of any text — preserve the original exactly, including heading and title case
- Summarize, shorten, paraphrase, or omit any substantive content — extract the FULL document text
- Add any commentary, content, or text that was not in the original document
- Repeat running page headers, page footers, or page numbers that recur on every page (include them once or omit them)
- Include watermarks, navigation, or other non-content artifacts

Iterate until the markdown faithfully represents the entire document content with consistent, clean formatting.`

// IsPDFURL returns true if the URL path ends with .pdf.
func IsPDFURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".pdf")
}

// PDFResult holds the result of processing a PDF via Reducto.
type PDFResult struct {
	Markdown string
}

// Reducto archives PDFs by downloading the source file and extracting markdown
// via Reducto's /extract endpoint with deep_extract enabled.
type Reducto struct {
	client  *http.Client
	apiKey  string
	baseURL string
	verbose bool
}

func NewReducto(apiKey string) *Reducto {
	return &Reducto{
		client:  &http.Client{Timeout: 30 * time.Minute},
		apiKey:  apiKey,
		baseURL: reductoBaseURL,
	}
}

// SetVerbose enables verbose logging.
func (r *Reducto) SetVerbose(v bool) { r.verbose = v }

// Fetch downloads the PDF at rawURL into archiveDir/document.pdf and returns
// the markdown extracted via the Reducto /extract endpoint with deep_extract enabled.
func (r *Reducto) Fetch(ctx context.Context, rawURL, archiveDir string) (*PDFResult, error) {
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating archive dir: %w", err)
	}

	if r.verbose {
		log.Printf("  downloading PDF from %s", rawURL)
	}
	pdfPath := filepath.Join(archiveDir, "document.pdf")
	if err := r.downloadPDF(ctx, rawURL, pdfPath); err != nil {
		return nil, fmt.Errorf("downloading PDF: %w", err)
	}
	if r.verbose {
		log.Printf("  PDF saved to %s", pdfPath)
	}

	if r.verbose {
		log.Printf("  extracting markdown via Reducto deep_extract")
	}
	md, err := r.extract(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("calling Reducto: %w", err)
	}

	return &PDFResult{Markdown: md}, nil
}

func (r *Reducto) downloadPDF(ctx context.Context, rawURL, outPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "agent-archiver/1.0")

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, io.LimitReader(resp.Body, pdfMaxBytes)); err != nil {
		_ = os.Remove(outPath)
		return fmt.Errorf("writing file: %w", err)
	}
	return nil
}

type reductoExtractRequest struct {
	Input        string                 `json:"input"`
	Instructions reductoInstructions    `json:"instructions"`
	Settings     reductoExtractSettings `json:"settings"`
}

type reductoInstructions struct {
	Schema       map[string]any `json:"schema"`
	SystemPrompt string         `json:"system_prompt"`
}

type reductoExtractSettings struct {
	DeepExtract bool `json:"deep_extract"`
}

type reductoExtractResponse struct {
	JobID  string          `json:"job_id"`
	Result json.RawMessage `json:"result"`
}

// extractedMarkdown matches the schema we send: a single markdown field.
type extractedMarkdown struct {
	Markdown string `json:"markdown"`
}

func (r *Reducto) extract(ctx context.Context, rawURL string) (string, error) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"markdown": map[string]any{
				"type":        "string",
				"description": "The complete document content rendered as clean, faithful markdown.",
			},
		},
		"required": []string{"markdown"},
	}

	reqBody, _ := json.Marshal(reductoExtractRequest{
		Input: rawURL,
		Instructions: reductoInstructions{
			Schema:       schema,
			SystemPrompt: pdfExtractSystemPrompt,
		},
		Settings: reductoExtractSettings{DeepExtract: true},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/extract", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling Reducto: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 200*1024*1024))
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("reducto returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var parsed reductoExtractResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	return parseExtractResult(parsed.Result)
}

// parseExtractResult handles both single-object and array result shapes from /extract.
func parseExtractResult(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("reducto returned empty result")
	}

	var single extractedMarkdown
	if err := json.Unmarshal(raw, &single); err == nil && single.Markdown != "" {
		return strings.TrimSpace(single.Markdown), nil
	}

	var multi []extractedMarkdown
	if err := json.Unmarshal(raw, &multi); err == nil && len(multi) > 0 {
		var sb strings.Builder
		for i, item := range multi {
			if item.Markdown == "" {
				continue
			}
			if i > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(item.Markdown)
		}
		out := strings.TrimSpace(sb.String())
		if out != "" {
			return out, nil
		}
	}

	return "", fmt.Errorf("reducto result did not contain markdown content")
}

// StripPDFExtension returns rawURL with a trailing .pdf (case-insensitive) removed
// from its path, so callers can derive a slug without the extension.
func StripPDFExtension(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if strings.HasSuffix(strings.ToLower(u.Path), ".pdf") {
		u.Path = u.Path[:len(u.Path)-4]
	}
	return u.String()
}
