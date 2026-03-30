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
	t.Parallel()

	tests := []struct {
		name         string
		giveMarkdown string
		giveBaseURL  string
		want         []imageRef
	}{
		{
			name: "absolute URLs with dedup",
			giveMarkdown: `# Test Article

Here is an image: ![alt text](https://example.com/image.png)

And another: ![](https://example.com/photo.jpg)

A data URI should be skipped: ![](data:image/png;base64,abc123)

A relative path should be skipped: ![](./local.png)

An HTML image: <img src="https://example.com/html-image.webp" alt="test">

A duplicate: ![dup](https://example.com/image.png)

URL with parens: ![chart](https://example.com/Diagram%20(2).png/_jcr_content/renditions/Diagram%20(2).webp)
`,
			want: []imageRef{
				{original: "https://example.com/image.png", resolved: "https://example.com/image.png"},
				{original: "https://example.com/photo.jpg", resolved: "https://example.com/photo.jpg"},
				{original: "https://example.com/Diagram%20(2).png/_jcr_content/renditions/Diagram%20(2).webp", resolved: "https://example.com/Diagram%20(2).png/_jcr_content/renditions/Diagram%20(2).webp"},
				{original: "https://example.com/html-image.webp", resolved: "https://example.com/html-image.webp"},
			},
		},
		{
			name: "relative URLs with base",
			giveMarkdown: `![logo](assets/images/logo.png)

![absolute](https://example.com/abs.png)

![root-relative](/images/root.png)
`,
			giveBaseURL: "https://vaku.dev/",
			want: []imageRef{
				{original: "assets/images/logo.png", resolved: "https://vaku.dev/assets/images/logo.png"},
				{original: "https://example.com/abs.png", resolved: "https://example.com/abs.png"},
				{original: "/images/root.png", resolved: "https://vaku.dev/images/root.png"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			refs := extractImageURLs(tt.giveMarkdown, tt.giveBaseURL)

			if len(refs) != len(tt.want) {
				t.Fatalf("got %d refs, want %d: %v", len(refs), len(tt.want), refs)
			}

			for i, ref := range refs {
				if ref.original != tt.want[i].original {
					t.Errorf("ref[%d].original = %q, want %q", i, ref.original, tt.want[i].original)
				}
				if ref.resolved != tt.want[i].resolved {
					t.Errorf("ref[%d].resolved = %q, want %q", i, ref.resolved, tt.want[i].resolved)
				}
			}
		})
	}
}

func TestIsDownloadableURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give string
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
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()

			if got := isDownloadableURL(tt.give); got != tt.want {
				t.Errorf("isDownloadableURL(%q) = %v, want %v", tt.give, got, tt.want)
			}
		})
	}
}

func TestFilenameFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		give       string
		wantSuffix string
	}{
		{
			name:       "jpg extension",
			give:       "https://example.com/images/photo.jpg",
			wantSuffix: ".jpg",
		},
		{
			name:       "png extension",
			give:       "https://example.com/images/other.png",
			wantSuffix: ".png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			name := filenameFromURL(tt.give)
			if !strings.HasSuffix(name, tt.wantSuffix) {
				t.Errorf("expected %s suffix, got %q", tt.wantSuffix, name)
			}
			if strings.Contains(name, " ") || strings.Contains(name, "(") {
				t.Errorf("filename should not contain spaces or parens, got %q", name)
			}
		})
	}

	t.Run("deterministic", func(t *testing.T) {
		t.Parallel()

		a := filenameFromURL("https://example.com/a.jpg")
		b := filenameFromURL("https://example.com/a.jpg")
		if a != b {
			t.Error("same URL should produce same filename")
		}
	})

	t.Run("unique", func(t *testing.T) {
		t.Parallel()

		a := filenameFromURL("https://example.com/a.jpg")
		b := filenameFromURL("https://example.com/b.png")
		if a == b {
			t.Error("different URLs should produce different filenames")
		}
	})
}

func TestProcessMarkdown(t *testing.T) {
	t.Parallel()

	t.Run("downloads and rewrites images", func(t *testing.T) {
		t.Parallel()

		imageData := []byte{0x89, 0x50, 0x4e, 0x47}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageData)
		}))
		defer srv.Close()

		imageDir := t.TempDir()
		markdown := "# Test\n\n![alt](" + srv.URL + "/test-image.png)\n"

		result, err := ProcessMarkdown(context.Background(), markdown, imageDir, srv.URL)
		if err != nil {
			t.Fatalf("ProcessMarkdown() error: %v", err)
		}

		if strings.Contains(result, srv.URL) {
			t.Error("result still contains the original URL")
		}
		if !strings.Contains(result, "./") {
			t.Errorf("result should contain relative path, got: %s", result)
		}
		if !strings.Contains(result, ".png") {
			t.Errorf("result should preserve .png extension, got: %s", result)
		}

		files, _ := filepath.Glob(filepath.Join(imageDir, "*"))
		if len(files) == 0 {
			t.Error("no image files downloaded")
		}
	})

	t.Run("no images", func(t *testing.T) {
		t.Parallel()

		markdown := "# Test\n\nNo images here.\n"
		result, err := ProcessMarkdown(context.Background(), markdown, t.TempDir(), "https://example.com/page")
		if err != nil {
			t.Fatalf("ProcessMarkdown() error: %v", err)
		}
		if result != markdown {
			t.Errorf("result should be unchanged, got: %s", result)
		}
	})

	t.Run("failed download", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		imageDir := t.TempDir()
		markdown := "![alt](" + srv.URL + "/missing.png)\n"

		result, err := ProcessMarkdown(context.Background(), markdown, imageDir, srv.URL)
		if err != nil {
			t.Fatalf("ProcessMarkdown() error: %v", err)
		}

		if !strings.Contains(result, srv.URL) {
			t.Error("failed download should leave original URL in place")
		}

		files, _ := os.ReadDir(imageDir)
		if len(files) != 0 {
			t.Errorf("expected no files after failed download, got %d", len(files))
		}
	})

	t.Run("relative images", func(t *testing.T) {
		t.Parallel()

		imageData := []byte{0x89, 0x50, 0x4e, 0x47}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageData)
		}))
		defer srv.Close()

		imageDir := t.TempDir()
		markdown := "# Test\n\n![logo](assets/images/logo.png)\n"

		result, err := ProcessMarkdown(context.Background(), markdown, imageDir, srv.URL+"/page/")
		if err != nil {
			t.Fatalf("ProcessMarkdown() error: %v", err)
		}

		if strings.Contains(result, "assets/images/logo.png") {
			t.Error("result still contains original relative path")
		}
		if !strings.Contains(result, "./") {
			t.Errorf("result should contain relative path, got: %s", result)
		}

		files, _ := filepath.Glob(filepath.Join(imageDir, "*"))
		if len(files) == 0 {
			t.Error("no image files downloaded")
		}
	})
}

func TestRewriteImagePaths(t *testing.T) {
	t.Parallel()

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
