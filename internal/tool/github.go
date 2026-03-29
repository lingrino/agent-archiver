package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

type githubReadmeInput struct {
	URL string `json:"url" jsonschema_description:"The GitHub repository URL (e.g. https://github.com/owner/repo)"`
}

// GitHubReadme fetches the README from a GitHub repository.
type GitHubReadme struct {
	client *http.Client
}

func NewGitHubReadme() *GitHubReadme {
	return &GitHubReadme{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *GitHubReadme) Name() string { return "github_readme" }

func (t *GitHubReadme) Description() string {
	return "Fetch the README file from a GitHub repository as markdown. " +
		"Use this tool when the URL points to a GitHub repository (github.com/owner/repo). " +
		"This is much faster and more accurate than fetching the rendered HTML page. " +
		"Returns the raw README markdown content directly."
}

func (t *GitHubReadme) InputSchema() anthropic.ToolInputSchemaParam {
	return GenerateSchema[githubReadmeInput]()
}

func (t *GitHubReadme) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params githubReadmeInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	owner, repo, err := parseGitHubURL(params.URL)
	if err != nil {
		return "", err
	}

	// Try common README filenames on the main branch, then master
	branches := []string{"main", "master"}
	filenames := []string{"README.md", "readme.md", "README.rst", "README"}

	for _, branch := range branches {
		for _, filename := range filenames {
			rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, branch, filename)
			content, fetchErr := t.fetchRaw(ctx, rawURL)
			if fetchErr == nil {
				return content, nil
			}
		}
	}

	return "", fmt.Errorf("could not find README for %s/%s", owner, repo)
}

func (t *GitHubReadme) fetchRaw(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; agent-archiver/1.0)")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return "", err
	}

	return string(body), nil
}

// parseGitHubURL extracts owner and repo from a GitHub URL.
// Handles formats like:
//   - https://github.com/owner/repo
//   - https://github.com/owner/repo/tree/main/...
//   - github.com/owner/repo
func parseGitHubURL(rawURL string) (owner, repo string, err error) {
	// Strip scheme
	u := rawURL
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")

	// Must start with github.com
	if !strings.HasPrefix(u, "github.com/") {
		return "", "", fmt.Errorf("not a GitHub URL: %s", rawURL)
	}

	parts := strings.Split(strings.TrimPrefix(u, "github.com/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("could not parse owner/repo from: %s", rawURL)
	}

	return parts[0], parts[1], nil
}

// IsGitHubURL returns true if the URL points to a GitHub repository.
func IsGitHubURL(rawURL string) bool {
	_, _, err := parseGitHubURL(rawURL)
	return err == nil
}
