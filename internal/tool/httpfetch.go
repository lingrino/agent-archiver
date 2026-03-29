package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

type httpFetchInput struct {
	URL string `json:"url" jsonschema_description:"The URL to fetch"`
}

type HTTPFetch struct {
	client *http.Client
}

func NewHTTPFetch() *HTTPFetch {
	return &HTTPFetch{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *HTTPFetch) Name() string { return "http_fetch" }

func (t *HTTPFetch) Description() string {
	return "Fetch the raw HTML content of a web page using a simple HTTP GET request. " +
		"This is the most basic extraction tool. It returns the full HTML source of the page " +
		"without executing JavaScript. Use this as a starting point or fallback when other " +
		"tools are unavailable. The response may contain navigation, ads, and other non-content " +
		"elements that will need to be filtered out."
}

func (t *HTTPFetch) InputSchema() anthropic.ToolInputSchemaParam {
	return GenerateSchema[httpFetchInput]()
}

func (t *HTTPFetch) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params httpFetchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, params.URL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; agent-archiver/1.0)")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", params.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, params.URL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024)) // 5MB limit
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	return string(body), nil
}
