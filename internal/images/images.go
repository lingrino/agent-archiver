package images

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	// Matches <img src="url"> HTML images
	htmlImageRe = regexp.MustCompile(`<img[^>]+src=["']([^"']+)["']`)
)

// ProcessMarkdown finds all image URLs in markdown, downloads them to imageDir,
// and rewrites the markdown to use relative paths.
func ProcessMarkdown(ctx context.Context, markdown string, imageDir string) (string, error) {
	urls := extractImageURLs(markdown)
	if len(urls) == 0 {
		return markdown, nil
	}

	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return "", fmt.Errorf("creating image directory: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}

	// Download images concurrently with a limit
	type result struct {
		originalURL string
		filename    string
		err         error
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		sem     = make(chan struct{}, 5) // concurrency limit
		results []result
	)

	seen := make(map[string]bool)
	for _, imgURL := range urls {
		if seen[imgURL] {
			continue
		}
		seen[imgURL] = true

		wg.Add(1)
		go func(imgURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			filename, err := downloadImage(ctx, client, imgURL, imageDir)
			mu.Lock()
			results = append(results, result{originalURL: imgURL, filename: filename, err: err})
			mu.Unlock()
		}(imgURL)
	}
	wg.Wait()

	// Build replacement map and rewrite markdown
	replacements := make(map[string]string)
	for _, r := range results {
		if r.err != nil {
			log.Printf("warning: failed to download image %s: %v", r.originalURL, r.err)
			continue
		}
		replacements[r.originalURL] = "./" + r.filename
	}

	return rewriteImagePaths(markdown, replacements), nil
}

func extractImageURLs(markdown string) []string {
	var urls []string
	seen := make(map[string]bool)

	// Parse ![alt](url) with balanced parentheses support
	for _, u := range extractMarkdownImageURLs(markdown) {
		if isDownloadableURL(u) && !seen[u] {
			urls = append(urls, u)
			seen[u] = true
		}
	}

	for _, match := range htmlImageRe.FindAllStringSubmatch(markdown, -1) {
		u := match[1]
		if isDownloadableURL(u) && !seen[u] {
			urls = append(urls, u)
			seen[u] = true
		}
	}

	return urls
}

// extractMarkdownImageURLs parses ![alt](url) patterns handling balanced parentheses in URLs.
func extractMarkdownImageURLs(markdown string) []string {
	var urls []string
	i := 0
	for i < len(markdown) {
		// Look for ![
		idx := strings.Index(markdown[i:], "![")
		if idx == -1 {
			break
		}
		i += idx + 2

		// Skip past alt text to ]
		bracketDepth := 1
		for i < len(markdown) && bracketDepth > 0 {
			switch markdown[i] {
			case '[':
				bracketDepth++
			case ']':
				bracketDepth--
			}
			i++
		}

		// Expect (
		if i >= len(markdown) || markdown[i] != '(' {
			continue
		}
		i++ // skip (

		// Read URL with balanced parentheses
		urlStart := i
		parenDepth := 1
		for i < len(markdown) && parenDepth > 0 {
			switch markdown[i] {
			case '(':
				parenDepth++
			case ')':
				parenDepth--
			}
			if parenDepth > 0 {
				i++
			}
		}

		if parenDepth == 0 {
			u := strings.TrimSpace(markdown[urlStart:i])
			// Strip optional title in quotes: url "title"
			if qIdx := strings.LastIndex(u, "\""); qIdx > 0 {
				if qStart := strings.LastIndex(u[:qIdx], "\""); qStart > 0 {
					u = strings.TrimSpace(u[:qStart])
				}
			}
			urls = append(urls, u)
			i++ // skip closing )
		}
	}
	return urls
}

func isDownloadableURL(u string) bool {
	if strings.HasPrefix(u, "data:") {
		return false
	}
	if strings.HasPrefix(u, "./") || strings.HasPrefix(u, "../") {
		return false
	}
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

func downloadImage(ctx context.Context, client *http.Client, imgURL string, imageDir string) (string, error) {
	// Try the URL as-is first
	filename, actualURL, err := tryDownload(ctx, client, imgURL, imageDir)
	if err == nil {
		return filename, nil
	}

	// On 404, try alternate URLs — the extraction may have resolved a
	// root-relative path (e.g. /cms/...) against the page URL, producing
	// a bad path like /article-slug/cms/... Try stripping path segments
	// from the front to find the real resource.
	u, parseErr := url.Parse(imgURL)
	if parseErr != nil {
		return "", err // return original error
	}

	segments := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	for i := 1; i < len(segments); i++ {
		altURL := *u
		altURL.Path = "/" + strings.Join(segments[i:], "/")
		candidate := altURL.String()
		if candidate == actualURL {
			continue
		}
		filename, _, dlErr := tryDownload(ctx, client, candidate, imageDir)
		if dlErr == nil {
			return filename, nil
		}
	}

	return "", err // return original error
}

func tryDownload(ctx context.Context, client *http.Client, imgURL string, imageDir string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return "", imgURL, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; agent-archiver/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return "", imgURL, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", imgURL, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	filename := filenameFromURL(imgURL)

	outPath := filepath.Join(imageDir, filename)
	f, err := os.Create(outPath)
	if err != nil {
		return "", imgURL, err
	}
	defer f.Close()

	if _, err := io.Copy(f, io.LimitReader(resp.Body, 50*1024*1024)); err != nil {
		os.Remove(outPath)
		return "", imgURL, err
	}

	return filename, imgURL, nil
}

func filenameFromURL(imgURL string) string {
	h := sha256.Sum256([]byte(imgURL))
	hash := fmt.Sprintf("%x", h[:8])

	// Preserve the original file extension
	u, err := url.Parse(imgURL)
	if err == nil {
		if ext := path.Ext(u.Path); ext != "" {
			return hash + ext
		}
	}

	return hash + ".png"
}

func rewriteImagePaths(markdown string, replacements map[string]string) string {
	result := markdown

	for original, replacement := range replacements {
		result = strings.ReplaceAll(result, original, replacement)
	}

	return result
}
