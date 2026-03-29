package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/lingrino/agent-archiver/internal/agent"
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

	result, err := a.Archive(ctx, targetURL)
	if err != nil {
		log.Fatalf("archive failed: %v", err)
	}

	result.Content, err = images.ProcessMarkdown(ctx, result.Content, result.ImageDir(archiveDir))
	if err != nil {
		log.Fatalf("image processing failed: %v", err)
	}

	if err := result.Write(archiveDir); err != nil {
		log.Fatalf("write failed: %v", err)
	}

	fmt.Printf("archived to %s\n", result.OutputPath(archiveDir))
}

func buildRegistry(cfg *config.Config) *tool.Registry {
	var tools []tool.Tool

	tools = append(tools, tool.NewHTTPFetch())

	if cfg.CloudflareAPIToken != "" && cfg.CloudflareAccountID != "" {
		tools = append(tools, tool.NewCloudflareContent(cfg.CloudflareAPIToken, cfg.CloudflareAccountID))
		tools = append(tools, tool.NewCloudflareMarkdown(cfg.CloudflareAPIToken, cfg.CloudflareAccountID))
	}

	if tool.TrafilaturaAvailable() {
		tools = append(tools, tool.NewTrafilatura())
	}

	return tool.NewRegistry(tools...)
}
