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

const cfBaseURL = "https://api.cloudflare.com/client/v4/accounts"

type cloudflareInput struct {
	URL string `json:"url" jsonschema_description:"The URL to render and extract content from"`
}

func callCF(ctx context.Context, client *http.Client, apiToken, accountID, endpoint, targetURL string) (string, error) {
	reqBody, _ := json.Marshal(map[string]string{"url": targetURL})
	apiURL := fmt.Sprintf("%s/%s/browser-rendering/%s", cfBaseURL, accountID, endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling Cloudflare %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Cloudflare %s returned HTTP %d: %s", endpoint, resp.StatusCode, string(body))
	}

	return string(body), nil
}

// CloudflareContent fetches fully rendered HTML via the Cloudflare Browser Rendering /content endpoint.
type CloudflareContent struct {
	client    *http.Client
	apiToken  string
	accountID string
}

func NewCloudflareContent(apiToken, accountID string) *CloudflareContent {
	return &CloudflareContent{
		client:    &http.Client{Timeout: 60 * time.Second},
		apiToken:  apiToken,
		accountID: accountID,
	}
}

func (t *CloudflareContent) Name() string { return "cloudflare_content" }

func (t *CloudflareContent) Description() string {
	return "Fetch the fully rendered HTML of a web page using Cloudflare Browser Rendering. " +
		"This tool uses a headless browser to execute JavaScript and render the page completely " +
		"before returning the HTML. Use this for pages that rely on JavaScript to load content, " +
		"such as single-page applications or dynamically loaded articles. Returns the full HTML " +
		"including navigation and other non-content elements."
}

func (t *CloudflareContent) InputSchema() anthropic.ToolInputSchemaParam {
	return GenerateSchema[cloudflareInput]()
}

func (t *CloudflareContent) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params cloudflareInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return callCF(ctx, t.client, t.apiToken, t.accountID, "content", params.URL)
}

// CloudflareMarkdown fetches markdown via the Cloudflare Browser Rendering /markdown endpoint.
type CloudflareMarkdown struct {
	client    *http.Client
	apiToken  string
	accountID string
}

func NewCloudflareMarkdown(apiToken, accountID string) *CloudflareMarkdown {
	return &CloudflareMarkdown{
		client:    &http.Client{Timeout: 60 * time.Second},
		apiToken:  apiToken,
		accountID: accountID,
	}
}

func (t *CloudflareMarkdown) Name() string { return "cloudflare_markdown" }

func (t *CloudflareMarkdown) Description() string {
	return "Fetch the content of a web page as markdown using Cloudflare Browser Rendering. " +
		"This tool renders the page in a headless browser and converts it to markdown format. " +
		"The output may still contain some navigation and non-content elements, but is generally " +
		"cleaner than raw HTML. This is a good first choice for extracting article content."
}

func (t *CloudflareMarkdown) InputSchema() anthropic.ToolInputSchemaParam {
	return GenerateSchema[cloudflareInput]()
}

func (t *CloudflareMarkdown) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params cloudflareInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	return callCF(ctx, t.client, t.apiToken, t.accountID, "markdown", params.URL)
}
