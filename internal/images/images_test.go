package images

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractImageURLs(t *testing.T) {
	markdown := `# Test Article

Here is an image: ![alt text](https://example.com/image.png)

And another: ![](https://example.com/photo.jpg)

A data URI should be skipped: ![](data:image/png;base64,abc123)

A relative path should be skipped: ![](./local.png)

An HTML image: <img src="https://example.com/html-image.webp" alt="test">

A duplicate: ![dup](https://example.com/image.png)

URL with parens: ![chart](https://example.com/Diagram%20(2).png/_jcr_content/renditions/Diagram%20(2).webp)
`

	urls := extractImageURLs(markdown)

	want := []string{
		"https://example.com/image.png",
		"https://example.com/photo.jpg",
		"https://example.com/Diagram%20(2).png/_jcr_content/renditions/Diagram%20(2).webp",
		"https://example.com/html-image.webp",
	}

	if len(urls) != len(want) {
		t.Fatalf("extractImageURLs() returned %d URLs, want %d: %v", len(urls), len(want), urls)
	}

	for i, u := range urls {
		if u != want[i] {
			t.Errorf("url[%d] = %q, want %q", i, u, want[i])
		}
	}
}

func TestIsDownloadableURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://example.com/img.png", true},
		{"http://example.com/img.png", true},
		{"data:image/png;base64,abc", false},
		{"./local.png", false},
		{"../local.png", false},
		{"/absolute/path.png", false},
	}

	for _, tt := range tests {
		if got := isDownloadableURL(tt.url); got != tt.want {
			t.Errorf("isDownloadableURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestFilenameFromURL(t *testing.T) {
	// Should always be a hash with the original extension
	name := filenameFromURL("https://example.com/images/photo.jpg")
	if !strings.HasSuffix(name, ".jpg") {
		t.Errorf("expected .jpg extension, got %q", name)
	}
	if strings.Contains(name, " ") || strings.Contains(name, "(") {
		t.Errorf("filename should not contain spaces or parens, got %q", name)
	}

	// Same URL should produce same filename
	name2 := filenameFromURL("https://example.com/images/photo.jpg")
	if name != name2 {
		t.Error("same URL should produce same filename")
	}

	// Different URL should produce different filename
	name3 := filenameFromURL("https://example.com/images/other.png")
	if name == name3 {
		t.Error("different URLs should produce different filenames")
	}
	if !strings.HasSuffix(name3, ".png") {
		t.Errorf("expected .png extension, got %q", name3)
	}
}

func TestProcessMarkdown(t *testing.T) {
	// Create a test HTTP server serving a small image
	imageData := []byte{0x89, 0x50, 0x4e, 0x47} // PNG magic bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageData)
	}))
	defer srv.Close()

	imageDir := t.TempDir()
	markdown := "# Test\n\n![alt](" + srv.URL + "/test-image.png)\n"

	result, err := ProcessMarkdown(context.Background(), markdown, imageDir)
	if err != nil {
		t.Fatalf("ProcessMarkdown() error: %v", err)
	}

	// Check that the URL was rewritten to a relative path
	if strings.Contains(result, srv.URL) {
		t.Error("result still contains the original URL")
	}
	if !strings.Contains(result, "./") {
		t.Errorf("result should contain relative path, got: %s", result)
	}
	if !strings.Contains(result, ".png") {
		t.Errorf("result should preserve .png extension, got: %s", result)
	}

	// Check that the image file was downloaded
	files, _ := filepath.Glob(filepath.Join(imageDir, "*"))
	if len(files) == 0 {
		t.Error("no image files downloaded")
	}
}

func TestProcessMarkdownNoImages(t *testing.T) {
	markdown := "# Test\n\nNo images here.\n"
	result, err := ProcessMarkdown(context.Background(), markdown, t.TempDir())
	if err != nil {
		t.Fatalf("ProcessMarkdown() error: %v", err)
	}
	if result != markdown {
		t.Errorf("result should be unchanged, got: %s", result)
	}
}

func TestProcessMarkdownFailedDownload(t *testing.T) {
	// Server that returns 404
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	imageDir := t.TempDir()
	markdown := "![alt](" + srv.URL + "/missing.png)\n"

	result, err := ProcessMarkdown(context.Background(), markdown, imageDir)
	if err != nil {
		t.Fatalf("ProcessMarkdown() error: %v", err)
	}

	// Original URL should remain since download failed
	if !strings.Contains(result, srv.URL) {
		t.Error("failed download should leave original URL in place")
	}

	// No files should be created
	files, _ := os.ReadDir(imageDir)
	if len(files) != 0 {
		t.Errorf("expected no files after failed download, got %d", len(files))
	}
}

func TestRewriteImagePaths(t *testing.T) {
	markdown := "![alt](https://example.com/img.png) and ![alt2](https://example.com/img2.jpg)"
	replacements := map[string]string{
		"https://example.com/img.png":  "./img.png",
		"https://example.com/img2.jpg": "./img2.jpg",
	}

	result := rewriteImagePaths(markdown, replacements)

	if strings.Contains(result, "https://example.com") {
		t.Error("result still contains original URLs")
	}
	if !strings.Contains(result, "./img.png") || !strings.Contains(result, "./img2.jpg") {
		t.Error("result missing relative paths")
	}
}
