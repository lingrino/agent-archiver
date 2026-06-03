package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/lingrino/agent-archiver/internal/agent"
	"github.com/lingrino/agent-archiver/internal/archive"
	"github.com/lingrino/agent-archiver/internal/config"
	"github.com/lingrino/agent-archiver/internal/images"
	"github.com/lingrino/agent-archiver/internal/tool"
)

// Process exit codes. These are part of the CLI contract so callers (e.g. the
// backfill script and the GitHub Action) can distinguish outcomes that should
// be retried from ones that should be logged and permanently skipped.
const (
	exitOK         = 0 // archived successfully, or skipped because it already exists
	exitError      = 1 // transient or unknown failure — safe to retry
	exitDeadLink   = 3 // URL is gone (404/410/5xx/unreachable) — log and skip
	exitIncomplete = 4 // paywalled / gated / partial content — log and skip
)

// route is how a target URL is dispatched through the pipeline.
type route int

const (
	routeWeb route = iota
	routeYouTube
	routePDF
	routeTweet
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("config: %v", err)
		return exitError
	}

	var (
		archiveDir   string
		model        string
		cleanupModel string
		verbose      bool
		skipExisting bool
	)

	flag.StringVar(&archiveDir, "archive-dir", cfg.ArchiveDir, "output directory for archived content (env: AA_ARCHIVE_DIR)")
	flag.StringVar(&model, "model", cfg.Model, "Claude model for the agent loop and per-archive summaries")
	flag.StringVar(&cleanupModel, "cleanup-model", cfg.CleanupModel, "Claude model for the final cleanup pass (env: AA_CLEANUP_MODEL)")
	flag.BoolVar(&verbose, "verbose", false, "enable verbose logging")
	flag.BoolVar(&skipExisting, "skip-existing", false, "skip (exit 0) if this URL has already been archived instead of overwriting it")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: agent-archiver [flags] <url>\n")
		flag.PrintDefaults()
		return exitError
	}
	targetURL := flag.Arg(0)

	cfg.ArchiveDir = archiveDir
	cfg.Model = model
	cfg.CleanupModel = cleanupModel
	cfg.Verbose = verbose

	if !tool.YtDlpAvailable() {
		log.Printf("yt-dlp is required but was not found in PATH")
		return exitError
	}
	if !tool.FfmpegAvailable() {
		log.Printf("ffmpeg is required but was not found in PATH")
		return exitError
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Classify the URL and compute its deterministic archive location up front,
	// so we can short-circuit work that is already done or pointing at a dead
	// link before spending any tokens.
	r := classifyRoute(ctx, targetURL)
	domain := archive.DomainFromURL(targetURL)
	slug, slugErr := routeSlug(r, targetURL)
	if slugErr != nil {
		log.Printf("could not determine archive path for %s: %v", targetURL, slugErr)
		return exitError
	}
	outputPath := filepath.Join(cfg.ArchiveDir, domain, slug, "index.md")

	if skipExisting {
		if _, statErr := os.Stat(outputPath); statErr == nil {
			log.Printf("skip: already archived at %s", outputPath)
			return exitOK
		}
	}

	// Preflight liveness check for routes that fetch arbitrary web content.
	// YouTube and Twitter go through dedicated APIs that report their own
	// not-found errors, so they are exempt.
	if r == routeWeb || r == routePDF {
		live := tool.CheckLiveness(ctx, targetURL)
		if live.Redirected && cfg.Verbose {
			log.Printf("note: %s redirected to %s", targetURL, live.FinalURL)
		}
		if !live.Alive {
			log.Printf("dead link: %s (%s) — skipping", targetURL, live.Reason)
			return exitDeadLink
		}
	}

	registry := buildRegistry(cfg)
	a := agent.New(cfg, registry)

	if cfg.Verbose {
		log.Printf("archiving %s", targetURL)
		log.Printf("available tools: %v", registry.Names())
	}

	var (
		result *archive.Archive
		code   int
	)
	switch r {
	case routeYouTube:
		result, code = archiveYouTube(ctx, cfg, a, targetURL, domain, slug)
	case routePDF:
		result, code = archivePDF(ctx, cfg, a, targetURL, domain, slug)
	case routeTweet:
		result, code = archiveTweet(ctx, cfg, a, targetURL, domain, slug)
	default:
		result, code = archiveWeb(ctx, cfg, a, targetURL)
	}
	if code != exitOK {
		return code
	}

	processedContent, processErr := images.ProcessMarkdown(ctx, result.Content, result.ImageDir(cfg.ArchiveDir), targetURL)
	if processErr != nil {
		log.Printf("image processing failed: %v", processErr)
		return exitError
	}
	result.Content = processedContent

	if writeErr := result.Write(cfg.ArchiveDir); writeErr != nil {
		log.Printf("write failed: %v", writeErr)
		return exitError
	}

	fmt.Printf("archived to %s\n", result.OutputPath(cfg.ArchiveDir))
	return exitOK
}

// classifyRoute determines how a URL is dispatched. Twitter is checked before
// PDF so tweet URLs don't incur the network round-trip IsPDF performs.
func classifyRoute(ctx context.Context, targetURL string) route {
	switch {
	case tool.IsYouTubeURL(targetURL):
		return routeYouTube
	case tool.IsTweetURL(targetURL):
		return routeTweet
	case tool.IsPDF(ctx, targetURL):
		return routePDF
	default:
		return routeWeb
	}
}

// routeSlug returns the archive slug for a URL given its route. It must match
// the slug the handler ultimately writes so the skip-existing path is accurate.
func routeSlug(r route, targetURL string) (string, error) {
	switch r {
	case routeYouTube:
		return tool.ParseYouTubeVideoID(targetURL)
	case routePDF:
		return tool.PDFSlug(targetURL), nil
	default: // tweet and web
		return archive.SlugFromURL(targetURL), nil
	}
}

// YouTube URLs are handled directly — download video, transcribe via
// ElevenLabs, identify speakers, and format as a markdown transcript.
func archiveYouTube(ctx context.Context, cfg *config.Config, a *agent.Agent, targetURL, domain, slug string) (*archive.Archive, int) {
	if cfg.Verbose {
		log.Printf("detected YouTube URL, processing via yt-dlp + ElevenLabs")
	}

	videoArchiveDir := filepath.Join(cfg.ArchiveDir, domain, slug)

	anthClient := agent.NewAnthropicClient(cfg.AnthropicAPIKey)
	yt := tool.NewYouTube(cfg.ElevenLabsAPIKey, cfg.ExaAPIKey, &anthClient, anthropic.Model(cfg.Model))
	yt.SetVerbose(cfg.Verbose)

	ytResult, fetchErr := yt.Fetch(ctx, targetURL, videoArchiveDir)
	if fetchErr != nil {
		log.Printf("youtube fetch failed: %v", fetchErr)
		return nil, exitError
	}

	if cfg.Verbose {
		log.Printf("generating summary via LLM")
	}
	summary, summaryErr := a.Summarize(ctx, ytResult.Markdown)
	if summaryErr != nil {
		log.Printf("summary generation failed: %v", summaryErr)
		return nil, exitError
	}

	return &archive.Archive{
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
	}, exitOK
}

// PDF URLs bypass the agent — download the file and parse it via Reducto, then
// run a single LLM cleanup pass against the original PDF as ground truth.
func archivePDF(ctx context.Context, cfg *config.Config, a *agent.Agent, targetURL, domain, slug string) (*archive.Archive, int) {
	if cfg.Verbose {
		log.Printf("detected PDF URL, downloading and parsing via Reducto")
	}

	pdfArchiveDir := filepath.Join(cfg.ArchiveDir, domain, slug)

	red := tool.NewReducto(cfg.ReductoAPIKey)
	red.SetVerbose(cfg.Verbose)

	pdfResult, fetchErr := red.Fetch(ctx, targetURL, pdfArchiveDir)
	if fetchErr != nil {
		log.Printf("pdf fetch failed: %v", fetchErr)
		return nil, exitError
	}

	if cfg.Verbose {
		log.Printf("running LLM cleanup pass with original PDF as ground truth")
	}
	cleaned, usage, cleanupErr := a.CleanupPDF(ctx, pdfResult.Markdown, pdfResult.Figures, pdfResult.PDFPath)
	if cleanupErr != nil {
		log.Printf("pdf cleanup failed: %v", cleanupErr)
		return nil, exitError
	}

	if cfg.Verbose {
		logUsage(usage, cfg.CleanupModel)
	}

	return &archive.Archive{
		Metadata: archive.Metadata{
			Title:        cleaned.Title,
			Author:       cleaned.Author,
			Date:         cleaned.Date,
			Type:         archive.TypePaper,
			Summary:      cleaned.Summary,
			URL:          targetURL,
			DownloadedAt: time.Now().UTC(),
		},
		Content: cleaned.Markdown,
		Domain:  domain,
		Slug:    slug,
	}, exitOK
}

// Twitter/X URLs are fetched directly via the API.
func archiveTweet(ctx context.Context, cfg *config.Config, a *agent.Agent, targetURL, domain, slug string) (*archive.Archive, int) {
	if cfg.Verbose {
		log.Printf("detected Twitter/X URL, fetching directly via API")
	}

	tw := tool.NewTwitter(cfg.XBearerToken)
	tw.SetVerbose(cfg.Verbose)
	tweetResult, fetchErr := tw.Fetch(ctx, targetURL)
	if fetchErr != nil {
		log.Printf("twitter fetch failed: %v", fetchErr)
		return nil, exitError
	}

	if cfg.Verbose {
		log.Printf("generating summary via LLM")
	}
	summary, summaryErr := a.Summarize(ctx, tweetResult.Markdown)
	if summaryErr != nil {
		log.Printf("summary generation failed: %v", summaryErr)
		return nil, exitError
	}

	return &archive.Archive{
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
		Domain:  domain,
		Slug:    slug,
	}, exitOK
}

// Generic web content goes through the two-phase agent pipeline.
func archiveWeb(ctx context.Context, cfg *config.Config, a *agent.Agent, targetURL string) (*archive.Archive, int) {
	result, usage, archiveErr := a.Archive(ctx, targetURL)
	if archiveErr != nil {
		if errors.Is(archiveErr, agent.ErrIncomplete) {
			log.Printf("incomplete content for %s: %v — skipping", targetURL, archiveErr)
			return nil, exitIncomplete
		}
		log.Printf("archive failed: %v", archiveErr)
		return nil, exitError
	}

	if cfg.Verbose {
		logUsage(usage, cfg.Model)
	}

	return result, exitOK
}

func logUsage(usage *agent.Usage, model string) {
	log.Printf("tokens: %d input, %d output", usage.InputTokens, usage.OutputTokens)
	if usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		log.Printf("cache: %d read, %d creation", usage.CacheReadInputTokens, usage.CacheCreationInputTokens)
	}
	log.Printf("estimated cost: $%.4f", usage.Cost(model))
}

func buildRegistry(cfg *config.Config) *tool.Registry {
	return tool.NewRegistry(
		tool.NewHTTPFetch(),
		tool.NewGitHubReadme(),
		tool.NewCloudflareContent(cfg.CloudflareAPIToken, cfg.CloudflareAccountID),
		tool.NewCloudflareMarkdown(cfg.CloudflareAPIToken, cfg.CloudflareAccountID),
		tool.NewFirecrawlMarkdown(cfg.FirecrawlAPIKey),
		tool.NewFirecrawlContent(cfg.FirecrawlAPIKey),
		tool.NewExaSearch(cfg.ExaAPIKey),
		tool.NewTwitter(cfg.XBearerToken),
	)
}
