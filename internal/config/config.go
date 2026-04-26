package config

import (
	"fmt"
	"os"
)

type Config struct {
	AnthropicAPIKey     string
	CloudflareAPIToken  string
	CloudflareAccountID string
	FirecrawlAPIKey     string
	ExaAPIKey           string
	XBearerToken        string
	ElevenLabsAPIKey    string
	ArchiveDir          string
	Model               string
	Verbose             bool
}

func Load() (*Config, error) {
	required := map[string]string{
		"AA_ANTHROPIC_API_KEY":     os.Getenv("AA_ANTHROPIC_API_KEY"),
		"AA_CLOUDFLARE_API_TOKEN":  os.Getenv("AA_CLOUDFLARE_API_TOKEN"),
		"AA_CLOUDFLARE_ACCOUNT_ID": os.Getenv("AA_CLOUDFLARE_ACCOUNT_ID"),
		"AA_FIRECRAWL_API_KEY":     os.Getenv("AA_FIRECRAWL_API_KEY"),
		"AA_EXA_API_KEY":           os.Getenv("AA_EXA_API_KEY"),
		"AA_X_BEARER_TOKEN":        os.Getenv("AA_X_BEARER_TOKEN"),
		"AA_ELEVENLABS_API_KEY":    os.Getenv("AA_ELEVENLABS_API_KEY"),
	}

	var missing []string
	for name, val := range required {
		if val == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}

	archiveDir := "./archive"
	if v := os.Getenv("AA_ARCHIVE_DIR"); v != "" {
		archiveDir = v
	}

	return &Config{
		AnthropicAPIKey:     required["AA_ANTHROPIC_API_KEY"],
		CloudflareAPIToken:  required["AA_CLOUDFLARE_API_TOKEN"],
		CloudflareAccountID: required["AA_CLOUDFLARE_ACCOUNT_ID"],
		FirecrawlAPIKey:     required["AA_FIRECRAWL_API_KEY"],
		ExaAPIKey:           required["AA_EXA_API_KEY"],
		XBearerToken:        required["AA_X_BEARER_TOKEN"],
		ElevenLabsAPIKey:    required["AA_ELEVENLABS_API_KEY"],
		ArchiveDir:          archiveDir,
		Model:               "claude-sonnet-4-6",
	}, nil
}
