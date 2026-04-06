package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/lingrino/agent-archiver/internal/agent"
	"github.com/lingrino/agent-archiver/internal/archive"
	"github.com/lingrino/agent-archiver/internal/config"
	"github.com/lingrino/agent-archiver/internal/images"
	"github.com/lingrino/agent-archiver/internal/tool"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	var (
		archiveDir string
		model      string
		verbose    bool
	)

	flag.StringVar(&archiveDir, "archive-dir", cfg.ArchiveDir, "output directory for archived content (env: AA_ARCHIVE_DIR)")
	flag.StringVar(&model, "model", cfg.Model, "Claude model to use")
	flag.BoolVar(&verbose, "verbose", false, "enable verbose logging")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: agent-archiver [flags] <url>\n")
		flag.PrintDefaults()
		os.Exit(1)
	}
	targetURL := flag.Arg(0)

	cfg.ArchiveDir = archiveDir
	cfg.Model = model
	cfg.Verbose = verbose

	if !tool.TrafilaturaAvailable() {
		log.Fatalf("trafilatura is required but was not found in PATH")
	}
	if !tool.YtDlpAvailable() {
		log.Fatalf("yt-dlp is required but was not found in PATH")
	}
	if !tool.FfmpegAvailable() {
		log.Fatalf("ffmpeg is required but was not found in PATH")
	}

	registry := buildRegistry(cfg)

	a := agent.New(cfg, registry)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if cfg.Verbose {
		log.Printf("archiving %s", targetURL)
		log.Printf("available tools: %v", registry.Names())
	}

	var result *archive.Archive

	// YouTube URLs are handled directly — download video, transcribe via
	// ElevenLabs, identify speakers, and format as markdown transcript.
	if tool.IsYouTubeURL(targetURL) {
		if cfg.Verbose {
			log.Printf("detected YouTube URL, processing via yt-dlp + ElevenLabs")
		}

		videoID, _ := tool.ParseYouTubeVideoID(targetURL)
		domain := archive.DomainFromURL(targetURL)
		slug := videoID
		videoArchiveDir := filepath.Join(archiveDir, domain, slug)

		anthClient := anthropic.NewClient(option.WithAPIKey(cfg.AnthropicAPIKey))
		yt := tool.NewYouTube(cfg.ElevenLabsAPIKey, cfg.ExaAPIKey, &anthClient, anthropic.Model(cfg.Model))
		yt.SetVerbose(cfg.Verbose)

		ytResult, fetchErr := yt.Fetch(ctx, targetURL, videoArchiveDir)
		if fetchErr != nil {
			log.Fatalf("youtube fetch failed: %v", fetchErr)
		}

		if cfg.Verbose {
			log.Printf("generating summary via LLM")
		}
		summary, summaryErr := a.Summarize(ctx, ytResult.Markdown)
		if summaryErr != nil {
			log.Fatalf("summary generation failed: %v", summaryErr)
		}

		result = &archive.Archive{
			Metadata: archive.Metadata{
				Title:        ytResult.Title,
				Author:       ytResult.Author,
				Date:         ytResult.Date,
				Type:         archive.ContentType(ytResult.Type),
				Summary:      summary,
				URL:          targetURL,
				DownloadedAt: time.Now().UTC(),
			},
			Content: ytResult.Markdown,
			Domain:  domain,
			Slug:    slug,
		}
	} else if tool.IsTweetURL(targetURL) {
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
	return tool.NewRegistry(
		tool.NewHTTPFetch(),
		tool.NewGitHubReadme(),
		tool.NewCloudflareContent(cfg.CloudflareAPIToken, cfg.CloudflareAccountID),
		tool.NewCloudflareMarkdown(cfg.CloudflareAPIToken, cfg.CloudflareAccountID),
		tool.NewExaSearch(cfg.ExaAPIKey),
		tool.NewTwitter(cfg.XBearerToken),
		tool.NewTrafilatura(),
	)
}
