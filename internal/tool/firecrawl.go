package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

const firecrawlBaseURL = "https://api.firecrawl.dev"

type firecrawlInput struct {
	URL string `json:"url" jsonschema_description:"The URL to render and extract content from"`
}

type firecrawlResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Markdown string          `json:"markdown"`
		HTML     string          `json:"html"`
		Metadata json.RawMessage `json:"metadata"`
	} `json:"data"`
	Error string `json:"error"`
}

func callFirecrawl(ctx context.Context, client *http.Client, apiKey, baseURL, format, targetURL string) (string, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"url":     targetURL,
		"formats": []string{format},
	})
	apiURL := baseURL + "/v2/scrape"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling Firecrawl scrape: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("firecrawl scrape returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var parsed firecrawlResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}
	if !parsed.Success {
		msg := parsed.Error
		if msg == "" {
			msg = string(body)
		}
		return "", fmt.Errorf("firecrawl scrape unsuccessful: %s", msg)
	}

	switch format {
	case "markdown":
		if parsed.Data.Markdown == "" {
			return "", fmt.Errorf("firecrawl returned empty markdown")
		}
		return parsed.Data.Markdown, nil
	case "html":
		if parsed.Data.HTML == "" {
			return "", fmt.Errorf("firecrawl returned empty html")
		}
		return parsed.Data.HTML, nil
	default:
		return "", fmt.Errorf("unsupported firecrawl format: %s", format)
	}
}

// FirecrawlMarkdown fetches markdown via the Firecrawl /scrape endpoint.
type FirecrawlMarkdown struct {
	client  *http.Client
	apiKey  string
	baseURL string
}

func NewFirecrawlMarkdown(apiKey string) *FirecrawlMarkdown {
	return &FirecrawlMarkdown{
		client:  &http.Client{Timeout: 90 * time.Second},
		apiKey:  apiKey,
		baseURL: firecrawlBaseURL,
	}
}

func (t *FirecrawlMarkdown) Name() string { return "firecrawl_markdown" }

func (t *FirecrawlMarkdown) Description() string {
	return "Fetch the content of a web page as markdown using Firecrawl, a paid scraping " +
		"service that handles JavaScript rendering, anti-bot measures, and complex layouts. " +
		"This is a more expensive backup option — prefer cloudflare_markdown first. Use " +
		"firecrawl_markdown when cloudflare output is incomplete, missing significant content " +
		"or images, blocked, or you want a second source to compare against. Returns clean " +
		"markdown with images and links preserved."
}

func (t *FirecrawlMarkdown) InputSchema() anthropic.ToolInputSchemaParam {
	return GenerateSchema[firecrawlInput]()
}

func (t *FirecrawlMarkdown) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params firecrawlInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return callFirecrawl(ctx, t.client, t.apiKey, t.baseURL, "markdown", params.URL)
}

// FirecrawlContent fetches processed HTML via the Firecrawl /scrape endpoint.
type FirecrawlContent struct {
	client  *http.Client
	apiKey  string
	baseURL string
}

func NewFirecrawlContent(apiKey string) *FirecrawlContent {
	return &FirecrawlContent{
		client:  &http.Client{Timeout: 90 * time.Second},
		apiKey:  apiKey,
		baseURL: firecrawlBaseURL,
	}
}

func (t *FirecrawlContent) Name() string { return "firecrawl_content" }

func (t *FirecrawlContent) Description() string {
	return "Fetch the processed HTML of a web page using Firecrawl, a paid scraping service " +
		"that handles JavaScript rendering, anti-bot measures, and complex layouts. This is a " +
		"more expensive backup option — prefer cloudflare_content first. Use firecrawl_content " +
		"when cloudflare output is incomplete, blocked, or you need a second source to extract " +
		"content from yourself. Returns HTML with boilerplate stripped."
}

func (t *FirecrawlContent) InputSchema() anthropic.ToolInputSchemaParam {
	return GenerateSchema[firecrawlInput]()
}

func (t *FirecrawlContent) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params firecrawlInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return callFirecrawl(ctx, t.client, t.apiKey, t.baseURL, "html", params.URL)
}
