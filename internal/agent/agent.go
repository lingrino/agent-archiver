package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/lingrino/agent-archiver/internal/archive"
	"github.com/lingrino/agent-archiver/internal/config"
	"github.com/lingrino/agent-archiver/internal/tool"
)

const maxIterations = 20

const submitToolName = "submit_extraction"

// Usage tracks cumulative token usage across all API calls.
type Usage struct {
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
}

func (u *Usage) add(msg *anthropic.Message) {
	u.InputTokens += msg.Usage.InputTokens
	u.OutputTokens += msg.Usage.OutputTokens
	u.CacheCreationInputTokens += msg.Usage.CacheCreationInputTokens
	u.CacheReadInputTokens += msg.Usage.CacheReadInputTokens
}

// Cost returns the estimated cost in USD based on model pricing.
func (u *Usage) Cost(model string) float64 {
	inputPrice, outputPrice := modelPricing(model)
	return float64(u.InputTokens)*inputPrice/1_000_000 +
		float64(u.OutputTokens)*outputPrice/1_000_000 +
		float64(u.CacheCreationInputTokens)*inputPrice*1.25/1_000_000 +
		float64(u.CacheReadInputTokens)*inputPrice*0.1/1_000_000
}

// modelPricing returns (input, output) price per million tokens.
func modelPricing(model string) (float64, float64) {
	switch model {
	case "claude-opus-4-6", "claude-opus-4-20250918":
		return 15.0, 75.0
	case "claude-sonnet-4-6", "claude-sonnet-4-20250514":
		return 3.0, 15.0
	case "claude-haiku-4-5-20251001":
		return 0.80, 4.0
	default:
		return 3.0, 15.0 // default to sonnet pricing
	}
}

type Agent struct {
	client   *anthropic.Client
	registry *tool.Registry
	model    anthropic.Model
	verbose  bool
}

func New(cfg *config.Config, registry *tool.Registry) *Agent {
	client := anthropic.NewClient()
	return &Agent{
		client:   &client,
		registry: registry,
		model:    anthropic.Model(cfg.Model),
		verbose:  cfg.Verbose,
	}
}

// Archive runs the two-phase pipeline: extraction then cleanup.
// It returns the archive and cumulative token usage across all API calls.
func (a *Agent) Archive(ctx context.Context, targetURL string) (*archive.Archive, *Usage, error) {
	var usage Usage

	// Phase 1: Extraction
	if a.verbose {
		log.Printf("phase 1: extracting content from %s", targetURL)
	}

	extractionResult, err := a.extract(ctx, targetURL, &usage)
	if err != nil {
		return nil, &usage, fmt.Errorf("extraction: %w", err)
	}

	if extractionResult.Confidence == "low" {
		return nil, &usage, fmt.Errorf("extraction confidence too low — the agent could not reliably extract content from this URL")
	}

	// Phase 2: Cleanup
	if a.verbose {
		log.Printf("phase 2: cleaning up extracted markdown")
	}

	cleanedMarkdown, err := a.cleanup(ctx, extractionResult.Markdown, &usage)
	if err != nil {
		return nil, &usage, fmt.Errorf("cleanup: %w", err)
	}

	return &archive.Archive{
		Metadata: archive.Metadata{
			Title:        extractionResult.Title,
			Author:       extractionResult.Author,
			Date:         extractionResult.Date,
			Summary:      extractionResult.Summary,
			URL:          targetURL,
			DownloadedAt: time.Now().UTC(),
		},
		Content: cleanedMarkdown,
		Domain:  archive.DomainFromURL(targetURL),
		Slug:    archive.SlugFromURL(targetURL),
	}, &usage, nil
}

type extractionResponse struct {
	Title      string `json:"title" jsonschema_description:"The article title"`
	Author     string `json:"author" jsonschema_description:"Author name if found, or empty string"`
	Date       string `json:"date" jsonschema_description:"Publication date in YYYY-MM-DD format if found, or empty string"`
	Markdown   string `json:"markdown" jsonschema_description:"The full article content as clean markdown"`
	Summary    string `json:"summary" jsonschema_description:"A concise summary paragraph of 3-8 sentences capturing the key ideas of the content"`
	Confidence string `json:"confidence" jsonschema:"enum=high,enum=medium,enum=low" jsonschema_description:"Your confidence that the full article was extracted correctly"`
}

// submitExtractionTool returns the tool definition for structured output.
func submitExtractionTool() anthropic.ToolUnionParam {
	schema := tool.GenerateSchema[extractionResponse]()
	return anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name: submitToolName,
			Description: anthropic.String(
				"Submit the final extracted article content and metadata. " +
					"Call this tool once you have successfully extracted the article. " +
					"Include the full article text as clean markdown, along with any metadata you found.",
			),
			InputSchema: schema,
		},
	}
}

func (a *Agent) extract(ctx context.Context, targetURL string, usage *Usage) (*extractionResponse, error) {
	userMessage := fmt.Sprintf("Extract the full article content from this URL: %s", targetURL)

	// Combine real tools with the structured output tool
	tools := append(a.registry.AnthropicTools(), submitExtractionTool())

	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(userMessage)),
	}

	for i := 0; i < maxIterations; i++ {
		if a.verbose {
			log.Printf("  extraction loop iteration %d", i+1)
		}

		msg, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     a.model,
			MaxTokens: 16384,
			System: []anthropic.TextBlockParam{
				{Text: extractionSystemPrompt},
			},
			Messages: messages,
			Tools:    tools,
		})
		if err != nil {
			return nil, fmt.Errorf("calling anthropic API: %w", err)
		}
		usage.add(msg)

		if msg.StopReason == anthropic.StopReasonEndTurn {
			return nil, fmt.Errorf("agent ended without calling %s — no structured result returned", submitToolName)
		}

		if msg.StopReason != anthropic.StopReasonToolUse {
			return nil, fmt.Errorf("unexpected stop reason: %s", msg.StopReason)
		}

		messages = append(messages, msg.ToParam())

		// Process tool calls — look for submit_extraction, execute real tools
		var toolResults []anthropic.ContentBlockParamUnion
		for _, block := range msg.Content {
			if block.Type != "tool_use" {
				continue
			}

			// If the agent called our structured output tool, parse and return
			if block.Name == submitToolName {
				var result extractionResponse
				if err := json.Unmarshal(block.Input, &result); err != nil {
					return nil, fmt.Errorf("parsing structured extraction result: %w", err)
				}
				if result.Markdown == "" {
					return nil, fmt.Errorf("agent returned empty markdown content")
				}
				return &result, nil
			}

			// Otherwise, execute the real tool
			if a.verbose {
				log.Printf("  calling tool: %s", block.Name)
			}

			result, execErr := a.registry.Execute(ctx, block.Name, block.Input)
			if execErr != nil {
				if a.verbose {
					log.Printf("  tool %s error: %v", block.Name, execErr)
				}
				toolResults = append(toolResults, anthropic.NewToolResultBlock(block.ID, execErr.Error(), true))
			} else {
				if len(result) > 200000 {
					result = result[:200000] + "\n\n[content truncated due to size]"
				}
				toolResults = append(toolResults, anthropic.NewToolResultBlock(block.ID, result, false))
			}
		}

		messages = append(messages, anthropic.NewUserMessage(toolResults...))
	}

	return nil, fmt.Errorf("extraction loop exceeded %d iterations", maxIterations)
}

// Summarize generates a concise summary of the given content.
func (a *Agent) Summarize(ctx context.Context, content string) (string, error) {
	msg, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     a.model,
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: summarizeSystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(content)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("calling summarize: %w", err)
	}

	for _, block := range msg.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("summarize returned no text response")
}

func (a *Agent) cleanup(ctx context.Context, markdown string, usage *Usage) (string, error) {
	msg, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     a.model,
		MaxTokens: 16384,
		System: []anthropic.TextBlockParam{
			{Text: cleanupSystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(markdown)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("calling cleanup: %w", err)
	}
	usage.add(msg)

	for _, block := range msg.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("cleanup returned no text response")
}
