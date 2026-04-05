package config

import (
	"fmt"
	"os"
)

type Config struct {
	AnthropicAPIKey     string
	CloudflareAPIToken  string
	CloudflareAccountID string
	ExaAPIKey           string
	XBearerToken        string
	ArchiveDir          string
	Model               string
	Verbose             bool
}

func Load() (*Config, error) {
	apiKey := os.Getenv("AA_ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("AA_ANTHROPIC_API_KEY environment variable is required")
	}

	archiveDir := "./archive"
	if v := os.Getenv("AA_ARCHIVE_DIR"); v != "" {
		archiveDir = v
	}

	return &Config{
		AnthropicAPIKey:     apiKey,
		CloudflareAPIToken:  os.Getenv("AA_CLOUDFLARE_API_TOKEN"),
		CloudflareAccountID: os.Getenv("AA_CLOUDFLARE_ACCOUNT_ID"),
		ExaAPIKey:           os.Getenv("AA_EXA_API_KEY"),
		XBearerToken:        os.Getenv("AA_X_BEARER_TOKEN"),
		ArchiveDir:          archiveDir,
		Model:               "claude-sonnet-4-6",
	}, nil
}
