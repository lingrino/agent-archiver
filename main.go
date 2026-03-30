package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/lingrino/agent-archiver/internal/agent"
	"github.com/lingrino/agent-archiver/internal/archive"
	"github.com/lingrino/agent-archiver/internal/config"
	"github.com/lingrino/agent-archiver/internal/images"
	"github.com/lingrino/agent-archiver/internal/tool"
)

func main() {
	var (
		archiveDir string
		model      string
		verbose    bool
	)

	flag.StringVar(&archiveDir, "archive-dir", "./archive", "output directory for archived content")
	flag.StringVar(&model, "model", "claude-sonnet-4-6", "Claude model to use")
	flag.BoolVar(&verbose, "verbose", false, "enable verbose logging")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: agent-archiver [flags] <url>\n")
		flag.PrintDefaults()
		os.Exit(1)
	}
	targetURL := flag.Arg(0)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.ArchiveDir = archiveDir
	cfg.Model = model
	cfg.Verbose = verbose

	registry := buildRegistry(cfg)

	a := agent.New(cfg, registry)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if cfg.Verbose {
		log.Printf("archiving %s", targetURL)
		log.Printf("available tools: %v", registry.Names())
	}

	var result *archive.Archive

	// Twitter/X URLs are handled directly — no agent loop needed since the
	// API returns structured data. This avoids hallucination from the LLM
	// trying to "extract an article" from a short tweet.
	if tool.IsTweetURL(targetURL) && cfg.XBearerToken != "" {
		if cfg.Verbose {
			log.Printf("detected Twitter/X URL, fetching directly via API")
		}
		tw := tool.NewTwitter(cfg.XBearerToken)
		tw.SetVerbose(cfg.Verbose)
		tweetResult, fetchErr := tw.Fetch(ctx, targetURL)
		if fetchErr != nil {
			log.Fatalf("twitter fetch failed: %v", fetchErr)
		}

		if cfg.Verbose {
			log.Printf("generating summary via LLM")
		}
		summary, summaryErr := a.Summarize(ctx, tweetResult.Markdown)
		if summaryErr != nil {
			log.Fatalf("summary generation failed: %v", summaryErr)
		}

		result = &archive.Archive{
			Metadata: archive.Metadata{
				Title:        tweetResult.Title,
				Author:       tweetResult.Author,
				Date:         tweetResult.Date,
				Type:         archive.ContentType(tweetResult.Type),
				Summary:      summary,
				URL:          targetURL,
				DownloadedAt: time.Now().UTC(),
			},
			Content: tweetResult.Markdown,
			Domain:  archive.DomainFromURL(targetURL),
			Slug:    archive.SlugFromURL(targetURL),
		}
	} else {
		var usage *agent.Usage
		var archiveErr error
		result, usage, archiveErr = a.Archive(ctx, targetURL)
		if archiveErr != nil {
			log.Fatalf("archive failed: %v", archiveErr)
		}

		if cfg.Verbose {
			log.Printf("tokens: %d input, %d output", usage.InputTokens, usage.OutputTokens)
			if usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
				log.Printf("cache: %d read, %d creation", usage.CacheReadInputTokens, usage.CacheCreationInputTokens)
			}
			log.Printf("estimated cost: $%.4f", usage.Cost(model))
		}
	}

	processedContent, processErr := images.ProcessMarkdown(ctx, result.Content, result.ImageDir(archiveDir), targetURL)
	if processErr != nil {
		log.Fatalf("image processing failed: %v", processErr)
	}
	result.Content = processedContent

	if writeErr := result.Write(archiveDir); writeErr != nil {
		log.Fatalf("write failed: %v", writeErr)
	}

	fmt.Printf("archived to %s\n", result.OutputPath(archiveDir))
}

func buildRegistry(cfg *config.Config) *tool.Registry {
	var tools []tool.Tool

	tools = append(tools, tool.NewHTTPFetch())
	tools = append(tools, tool.NewGitHubReadme())

	if cfg.CloudflareAPIToken != "" && cfg.CloudflareAccountID != "" {
		tools = append(tools, tool.NewCloudflareContent(cfg.CloudflareAPIToken, cfg.CloudflareAccountID))
		tools = append(tools, tool.NewCloudflareMarkdown(cfg.CloudflareAPIToken, cfg.CloudflareAccountID))
	}

	if cfg.ExaAPIKey != "" {
		tools = append(tools, tool.NewExaSearch(cfg.ExaAPIKey))
	}

	if cfg.XBearerToken != "" {
		tools = append(tools, tool.NewTwitter(cfg.XBearerToken))
	}

	if tool.TrafilaturaAvailable() {
		tools = append(tools, tool.NewTrafilatura())
	}

	return tool.NewRegistry(tools...)
}
