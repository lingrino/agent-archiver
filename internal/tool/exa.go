package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

type exaSearchInput struct {
	Query string `json:"query" jsonschema_description:"The search query to look up"`
}

type ExaSearch struct {
	client  *http.Client
	apiKey  string
	baseURL string
}

func NewExaSearch(apiKey string) *ExaSearch {
	return &ExaSearch{
		client:  &http.Client{Timeout: 15 * time.Second},
		apiKey:  apiKey,
		baseURL: "https://api.exa.ai",
	}
}

func (t *ExaSearch) Name() string { return "web_search" }

func (t *ExaSearch) Description() string {
	return "Search the web for information about a topic. " +
		"Use this tool when the page being archived is not a self-contained article " +
		"(e.g., a product landing page, homepage, or tool page) and you need additional " +
		"context to write a good summary. Returns titles, URLs, and relevant highlights " +
		"from the top search results."
}

func (t *ExaSearch) InputSchema() anthropic.ToolInputSchemaParam {
	return GenerateSchema[exaSearchInput]()
}

type exaRequest struct {
	Query      string           `json:"query"`
	Type       string           `json:"type"`
	NumResults int              `json:"numResults"`
	Contents   exaContentConfig `json:"contents"`
}

type exaContentConfig struct {
	Highlights exaHighlightConfig `json:"highlights"`
}

type exaHighlightConfig struct {
	NumHighlightsPerURL int `json:"numHighlightsPerUrl"`
	MaxCharacters       int `json:"maxCharacters"`
}

type exaResponse struct {
	Results []exaResult `json:"results"`
}

type exaResult struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	PublishedDate string   `json:"publishedDate"`
	Highlights    []string `json:"highlights"`
}

func (t *ExaSearch) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params exaSearchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	reqBody := exaRequest{
		Query:      params.Query,
		Type:       "auto",
		NumResults: 5,
		Contents: exaContentConfig{
			Highlights: exaHighlightConfig{NumHighlightsPerURL: 3, MaxCharacters: 4000},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", t.apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("searching: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("exa API returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var exaResp exaResponse
	if err := json.Unmarshal(respBody, &exaResp); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	if len(exaResp.Results) == 0 {
		return "No results found.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Search results for %q:\n", params.Query)
	for i, r := range exaResp.Results {
		fmt.Fprintf(&sb, "\n%d. %s\n   URL: %s\n", i+1, r.Title, r.URL)
		if r.PublishedDate != "" {
			fmt.Fprintf(&sb, "   Published: %s\n", r.PublishedDate)
		}
		for _, h := range r.Highlights {
			fmt.Fprintf(&sb, "   > %s\n", h)
		}
	}

	return sb.String(), nil
}
