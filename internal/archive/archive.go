package archive

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Metadata struct {
	Title        string    `yaml:"title"`
	Author       string    `yaml:"author,omitempty"`
	Date         string    `yaml:"date,omitempty"`
	Summary      string    `yaml:"summary,omitempty"`
	URL          string    `yaml:"url"`
	DownloadedAt time.Time `yaml:"downloaded_at"`
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

// SlugFromURL generates a URL-safe slug from a URL path.
func SlugFromURL(rawURL string) string {
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
