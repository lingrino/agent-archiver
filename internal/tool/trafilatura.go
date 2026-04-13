package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/anthropics/anthropic-sdk-go"
)

type trafilaturaInput struct {
	URL string `json:"url" jsonschema_description:"The URL to extract content from"`
}

type Trafilatura struct{}

func NewTrafilatura() *Trafilatura {
	return &Trafilatura{}
}

// TrafilaturaAvailable returns true if the trafilatura binary is in PATH.
func TrafilaturaAvailable() bool {
	_, err := exec.LookPath("trafilatura")
	return err == nil
}

func (t *Trafilatura) Name() string { return "trafilatura" }

func (t *Trafilatura) Description() string {
	return "Extract the main content of a web page as markdown using trafilatura. " +
		"Trafilatura is specialized in extracting the main text content from web pages, " +
		"stripping away navigation, ads, footers, and other boilerplate. It is very effective " +
		"for articles and blog posts. The output is clean markdown with the article text " +
		"and image references. Use this as a fallback if cloudflare_markdown produces " +
		"incomplete or low-quality results."
}

func (t *Trafilatura) InputSchema() anthropic.ToolInputSchemaParam {
	return GenerateSchema[trafilaturaInput]()
}

func (t *Trafilatura) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params trafilaturaInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	cmd := exec.CommandContext(ctx, "trafilatura", "-u", params.URL, "--markdown", "--images")
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("trafilatura failed: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("running trafilatura: %w", err)
	}

	result := string(output)
	if result == "" {
		return "", fmt.Errorf("trafilatura returned empty output for %s", params.URL)
	}

	return result, nil
}
