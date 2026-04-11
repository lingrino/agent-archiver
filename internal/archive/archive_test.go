package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSlugFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give string
		want string
	}{
		{"https://example.com/blog/my-post", "blog-my-post"},
		{"https://example.com/blog/my-post/", "blog-my-post"},
		{"https://example.com/", "index"},
		{"https://example.com", "index"},
		{"https://example.com/a/b/c/d", "a-b-c-d"},
		{"https://example.com/post with spaces", "post-with-spaces"},
		{"https://example.com/POST-Title", "post-title"},
		{"not a url", "not-a-url"},
	}

	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()

			if got := SlugFromURL(tt.give); got != tt.want {
				t.Errorf("SlugFromURL(%q) = %q, want %q", tt.give, got, tt.want)
			}
		})
	}
}

func TestDomainFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give string
		want string
	}{
		{"https://example.com/blog/post", "example.com"},
		{"https://www.example.com/blog", "www.example.com"},
		{"https://sub.domain.example.com/", "sub.domain.example.com"},
		{"not a url at all", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()

			if got := DomainFromURL(tt.give); got != tt.want {
				t.Errorf("DomainFromURL(%q) = %q, want %q", tt.give, got, tt.want)
			}
		})
	}
}

func TestArchiveWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	a := &Archive{
		Metadata: Metadata{
			Title:        "Test Article",
			Author:       "Test Author",
			Date:         "2024-01-15",
			Type:         TypeArticle,
			Summary:      "This is a test summary of the article.",
			URL:          "https://example.com/test",
			DownloadedAt: time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
		},
		Content: "# Hello World\n\nThis is a test article.\n",
		Domain:  "example.com",
		Slug:    "test",
	}

	if err := a.Write(dir); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	outPath := filepath.Join(dir, "example.com", "test", "index.md")
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	content := string(data)

	if !strings.Contains(content, "---") {
		t.Error("missing frontmatter delimiters")
	}
	if !strings.Contains(content, "title: Test Article") {
		t.Error("missing title in frontmatter")
	}
	if !strings.Contains(content, "author: Test Author") {
		t.Error("missing author in frontmatter")
	}
	if !strings.Contains(content, "type: article") {
		t.Error("missing type in frontmatter")
	}
	if !strings.Contains(content, "summary: This is a test summary of the article.") {
		t.Error("missing summary in frontmatter")
	}
	if !strings.Contains(content, "url: https://example.com/test") {
		t.Error("missing URL in frontmatter")
	}
	if !strings.Contains(content, "# Hello World") {
		t.Error("missing article content")
	}
	if !strings.HasSuffix(content, "\n") {
		t.Error("file should end with newline")
	}
}

func TestOutputPath(t *testing.T) {
	t.Parallel()

	a := &Archive{Domain: "example.com", Slug: "my-post"}
	got := a.OutputPath("/base")
	want := "/base/example.com/my-post/index.md"
	if got != want {
		t.Errorf("OutputPath() = %q, want %q", got, want)
	}
}

func TestImageDir(t *testing.T) {
	t.Parallel()

	a := &Archive{Domain: "example.com", Slug: "my-post"}
	got := a.ImageDir("/base")
	want := "/base/example.com/my-post"
	if got != want {
		t.Errorf("ImageDir() = %q, want %q", got, want)
	}
}
