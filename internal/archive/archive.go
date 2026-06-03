package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ContentType represents the type of content being archived.
type ContentType string

const (
	TypeArticle       ContentType = "article"
	TypeVideo         ContentType = "video"
	TypeTweet         ContentType = "tweet"
	TypeDocumentation ContentType = "documentation"
	TypeDiscussion    ContentType = "discussion"
	TypePaper         ContentType = "paper"
	TypePage          ContentType = "page"
)

type Metadata struct {
	Title        string      `yaml:"title"`
	Author       string      `yaml:"author,omitempty"`
	Date         string      `yaml:"date,omitempty"`
	Type         ContentType `yaml:"type"`
	Summary      string      `yaml:"summary,omitempty"`
	URL          string      `yaml:"url"`
	DownloadedAt time.Time   `yaml:"downloaded_at"`
}

type Archive struct {
	Metadata Metadata
	Content  string
	Domain   string
	Slug     string
}

// OutputPath returns the path to the markdown file for this archive.
func (a *Archive) OutputPath(baseDir string) string {
	return filepath.Join(baseDir, a.Domain, a.Slug, "index.md")
}

// ImageDir returns the directory where images for this archive should be stored.
func (a *Archive) ImageDir(baseDir string) string {
	return filepath.Join(baseDir, a.Domain, a.Slug)
}

// Write writes the archive to disk as a markdown file with YAML frontmatter.
func (a *Archive) Write(baseDir string) error {
	outPath := a.OutputPath(baseDir)

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	frontmatter, err := yaml.Marshal(a.Metadata)
	if err != nil {
		return fmt.Errorf("marshaling frontmatter: %w", err)
	}

	var buf strings.Builder
	buf.WriteString("---\n")
	buf.Write(frontmatter)
	buf.WriteString("---\n\n")
	buf.WriteString(a.Content)

	// Ensure file ends with a newline
	if !strings.HasSuffix(a.Content, "\n") {
		buf.WriteString("\n")
	}

	if err := os.WriteFile(outPath, []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	return nil
}

var nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)

// SlugFromURL generates a URL-safe slug from a URL, suffixed with a short hash
// of the full URL. The hash guarantees that distinct URLs never collide onto
// the same archive directory (different query strings, repeated trailing path
// segments across a domain, etc.) while keeping the result deterministic so
// repeat runs of the same URL resolve to the same directory.
func SlugFromURL(rawURL string) string {
	return slugBase(rawURL) + "-" + shortURLHash(rawURL)
}

func slugBase(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "page"
	}

	path := strings.TrimSuffix(u.Path, "/")
	path = strings.TrimPrefix(path, "/")

	if path == "" {
		return "index"
	}

	// Use the last path segment(s), replacing separators
	slug := strings.ToLower(path)
	slug = strings.ReplaceAll(slug, "/", "-")
	slug = nonAlphanumeric.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")

	if slug == "" {
		return "page"
	}

	// Truncate long slugs
	if len(slug) > 80 {
		slug = slug[:80]
		slug = strings.TrimRight(slug, "-")
	}

	return slug
}

// shortURLHash returns the first 8 hex characters of the SHA-256 of the URL.
func shortURLHash(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])[:8]
}

// DomainFromURL extracts the domain from a URL.
func DomainFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "unknown"
	}
	host := u.Hostname()
	if host == "" {
		return "unknown"
	}
	return host
}
