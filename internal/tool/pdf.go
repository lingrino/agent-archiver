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
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	reductoBaseURL = "https://platform.reducto.ai"
	pdfMaxBytes    = 200 * 1024 * 1024 // 200 MB cap on downloaded PDF size
)

// IsPDFURL returns true if the URL path ends with .pdf. Pure / synchronous.
func IsPDFURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".pdf")
}

// pdfDetectClient is the HTTP client used for content-type probes. Exposed as a
// package var so tests can override it; production callers use the default.
var pdfDetectClient = &http.Client{Timeout: 15 * time.Second}

// IsPDF returns true if the URL is recognizably a PDF — either by .pdf path
// extension (cheap) or by HEAD request Content-Type (one network round-trip).
// Used to catch URLs like https://arxiv.org/pdf/1706.03762 where the path lacks
// the extension but the server reports application/pdf.
func IsPDF(ctx context.Context, rawURL string) bool {
	if IsPDFURL(rawURL) {
		return true
	}
	return isPDFContentType(ctx, rawURL)
}

func isPDFContentType(ctx context.Context, rawURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "agent-archiver/1.0")

	resp, err := pdfDetectClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	ct := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	// Match "application/pdf" and variants like "application/pdf; charset=binary".
	return strings.HasPrefix(ct, "application/pdf")
}

// PDFFigure is a figure extracted by Reducto with an associated image URL.
type PDFFigure struct {
	URL     string // presigned image URL from Reducto
	Caption string // figure caption text from the parser, may be empty
	Page    int    // 1-based page number when available
}

// PDFResult holds the result of processing a PDF via Reducto.
type PDFResult struct {
	Markdown string
	Figures  []PDFFigure
	PDFPath  string // local path to the saved PDF document
}

// Reducto archives PDFs by downloading the source file and extracting markdown
// + figure image URLs via Reducto's /parse endpoint.
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
// the parsed markdown plus any figure image URLs.
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
		log.Printf("  parsing PDF via Reducto")
	}
	md, figures, err := r.parse(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("calling Reducto: %w", err)
	}
	if r.verbose {
		log.Printf("  parsed: %d chars markdown, %d figures with images", len(md), len(figures))
	}

	return &PDFResult{
		Markdown: md,
		Figures:  figures,
		PDFPath:  pdfPath,
	}, nil
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

	// Read one byte beyond the limit so we can detect a truncated download:
	// if Copy stops at exactly pdfMaxBytes, the source was at-or-over the cap.
	n, err := io.Copy(out, io.LimitReader(resp.Body, pdfMaxBytes+1))
	if err != nil {
		_ = os.Remove(outPath)
		return fmt.Errorf("writing file: %w", err)
	}
	if n > pdfMaxBytes {
		_ = os.Remove(outPath)
		return fmt.Errorf("PDF exceeds %d MB size limit", pdfMaxBytes/(1024*1024))
	}
	return nil
}

type reductoParseRequest struct {
	Input      string                 `json:"input"`
	Retrieval  reductoRetrieval       `json:"retrieval"`
	Formatting reductoFormatting      `json:"formatting"`
	Settings   reductoParsingSettings `json:"settings"`
}

type reductoRetrieval struct {
	Chunking reductoChunking `json:"chunking"`
}

type reductoChunking struct {
	ChunkMode string `json:"chunk_mode"`
}

type reductoFormatting struct {
	Include []string `json:"include"`
}

type reductoParsingSettings struct {
	ReturnImages []string `json:"return_images"`
}

type reductoParseResponse struct {
	JobID  string             `json:"job_id"`
	Result reductoParseResult `json:"result"`
}

type reductoParseResult struct {
	Type   string         `json:"type"`
	URL    string         `json:"url"`
	Chunks []reductoChunk `json:"chunks"`
}

type reductoChunk struct {
	Content string         `json:"content"`
	Blocks  []reductoBlock `json:"blocks"`
}

type reductoBlock struct {
	Type     string      `json:"type"`
	Content  string      `json:"content"`
	ImageURL string      `json:"image_url"`
	Bbox     reductoBbox `json:"bbox"`
}

type reductoBbox struct {
	Page int `json:"page"`
}

func (r *Reducto) parse(ctx context.Context, rawURL string) (string, []PDFFigure, error) {
	reqBody, _ := json.Marshal(reductoParseRequest{
		Input:      rawURL,
		Retrieval:  reductoRetrieval{Chunking: reductoChunking{ChunkMode: "disabled"}},
		Formatting: reductoFormatting{Include: []string{"hyperlinks"}},
		Settings:   reductoParsingSettings{ReturnImages: []string{"figure"}},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/parse", bytes.NewReader(reqBody))
	if err != nil {
		return "", nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("calling Reducto: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 200*1024*1024))
	if err != nil {
		return "", nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("reducto returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var parsed reductoParseResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", nil, fmt.Errorf("parsing response: %w", err)
	}

	if parsed.Result.Type == "url" && parsed.Result.URL != "" {
		return r.fetchURLResult(ctx, parsed.Result.URL)
	}

	return collectChunks(parsed.Result.Chunks)
}

func (r *Reducto) fetchURLResult(ctx context.Context, resultURL string) (string, []PDFFigure, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resultURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("fetching url result: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024*1024))
	if err != nil {
		return "", nil, fmt.Errorf("reading url result: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("url result returned HTTP %d", resp.StatusCode)
	}

	var direct reductoParseResult
	directErr := json.Unmarshal(body, &direct)
	if directErr == nil && len(direct.Chunks) > 0 {
		return collectChunks(direct.Chunks)
	}

	var wrapped reductoParseResponse
	wrappedErr := json.Unmarshal(body, &wrapped)
	if wrappedErr == nil && len(wrapped.Result.Chunks) > 0 {
		return collectChunks(wrapped.Result.Chunks)
	}

	return "", nil, fmt.Errorf("could not parse url-type result (direct: %v, wrapped: %v)", directErr, wrappedErr)
}

func collectChunks(chunks []reductoChunk) (string, []PDFFigure, error) {
	if len(chunks) == 0 {
		return "", nil, fmt.Errorf("reducto returned no chunks")
	}
	var sb strings.Builder
	var figures []PDFFigure
	for i, c := range chunks {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(c.Content)
		for _, b := range c.Blocks {
			if !strings.EqualFold(b.Type, "Figure") || b.ImageURL == "" {
				continue
			}
			figures = append(figures, PDFFigure{
				URL:     b.ImageURL,
				Caption: strings.TrimSpace(b.Content),
				Page:    b.Bbox.Page,
			})
		}
	}
	md := strings.TrimSpace(sb.String())
	if md == "" {
		return "", nil, fmt.Errorf("reducto returned empty content")
	}
	return md, figures, nil
}

// PDFSlug returns a clean slug for a PDF URL based on the filename stem (the
// last path segment with .pdf stripped). Falls back to "document" if the URL
// has no usable filename.
func PDFSlug(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "document"
	}
	stem := path.Base(u.Path)
	if i := strings.LastIndex(stem, "."); i > 0 {
		if strings.EqualFold(stem[i:], ".pdf") {
			stem = stem[:i]
		}
	}
	stem = strings.ToLower(stem)
	stem = pdfSlugCleaner.ReplaceAllString(stem, "-")
	stem = strings.Trim(stem, "-")
	if stem == "" || stem == "." || stem == "/" {
		return "document"
	}
	if len(stem) > 80 {
		stem = strings.TrimRight(stem[:80], "-")
	}
	return stem
}

var pdfSlugCleaner = regexp.MustCompile(`[^a-z0-9]+`)
