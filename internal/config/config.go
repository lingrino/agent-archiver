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
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable is required")
	}

	return &Config{
		AnthropicAPIKey:     apiKey,
		CloudflareAPIToken:  os.Getenv("CLOUDFLARE_API_TOKEN"),
		CloudflareAccountID: os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		ExaAPIKey:           os.Getenv("EXA_API_KEY"),
		XBearerToken:        os.Getenv("X_BEARER_TOKEN"),
		ArchiveDir:          "./archive",
		Model:               "claude-sonnet-4-6",
	}, nil
}
