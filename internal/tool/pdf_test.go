package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsPDFURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give string
		want bool
	}{
		{"https://example.com/file.pdf", true},
		{"https://example.com/file.PDF", true},
		{"http://example.com/path/to/doc.pdf", true},
		{"https://example.com/doc.pdf?v=1", true},
		{"https://example.com/doc.pdf#page=1", true},
		{"https://example.com/article", false},
		{"https://example.com/foo.html", false},
		{"https://example.com/pdf-thing", false},
		{"not a url", false},
	}

	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()
			if got := IsPDFURL(tt.give); got != tt.want {
				t.Errorf("IsPDFURL(%q) = %v, want %v", tt.give, got, tt.want)
			}
		})
	}
}

func TestIsPDF(t *testing.T) {
	tests := []struct {
		name           string
		giveStatus     int
		giveCT         string
		giveExtension  bool // if true, the test server URL has .pdf appended in path
		wantNetworkHit bool
		want           bool
	}{
		{
			name:           "extension shortcut",
			giveExtension:  true,
			wantNetworkHit: false,
			want:           true,
		},
		{
			name:           "no extension, application/pdf",
			giveStatus:     http.StatusOK,
			giveCT:         "application/pdf",
			wantNetworkHit: true,
			want:           true,
		},
		{
			name:           "no extension, application/pdf with charset",
			giveStatus:     http.StatusOK,
			giveCT:         "application/pdf; charset=binary",
			wantNetworkHit: true,
			want:           true,
		},
		{
			name:           "no extension, html content-type",
			giveStatus:     http.StatusOK,
			giveCT:         "text/html",
			wantNetworkHit: true,
			want:           false,
		},
		{
			name:           "no extension, 404",
			giveStatus:     http.StatusNotFound,
			giveCT:         "application/pdf",
			wantNetworkHit: true,
			want:           false,
		},
	}

	// Subtests share the package-level pdfDetectClient, so they run serially.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hit bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hit = true
				if r.Method != http.MethodHead {
					t.Errorf("expected HEAD, got %s", r.Method)
				}
				if tt.giveCT != "" {
					w.Header().Set("Content-Type", tt.giveCT)
				}
				w.WriteHeader(tt.giveStatus)
			}))
			defer server.Close()

			origClient := pdfDetectClient
			pdfDetectClient = server.Client()
			defer func() { pdfDetectClient = origClient }()

			testURL := server.URL + "/document"
			if tt.giveExtension {
				testURL += ".pdf"
			}

			got := IsPDF(context.Background(), testURL)
			if got != tt.want {
				t.Errorf("IsPDF = %v, want %v", got, tt.want)
			}
			if hit != tt.wantNetworkHit {
				t.Errorf("network hit = %v, want %v", hit, tt.wantNetworkHit)
			}
		})
	}
}

func TestPDFSlug(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give string
		want string
	}{
		{
			"https://www-cs-faculty.stanford.edu/~knuth/papers/claude-cycles.pdf",
			"claude-cycles",
		},
		{
			"https://assets.anthropic.com/m/ec212e6566a0d47/original/Disrupting-the-first-reported-AI-orchestrated-cyber-espionage-campaign.pdf",
			"disrupting-the-first-reported-ai-orchestrated-cyber-espionage-campaign",
		},
		{
			"https://example.com/path/to/Some File Name.PDF",
			"some-file-name",
		},
		{
			"https://example.com/file.pdf?v=1",
			"file",
		},
		{
			"https://example.com/no-extension",
			"no-extension",
		},
		{
			"https://arxiv.org/pdf/1706.03762",
			"1706-03762",
		},
		{
			"https://arxiv.org/pdf/2301.12345v2",
			"2301-12345v2",
		},
		{
			"https://example.com/",
			"document",
		},
		{
			"https://example.com/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.pdf",
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()
			if got := PDFSlug(tt.give); got != tt.want {
				t.Errorf("PDFSlug(%q) = %q, want %q", tt.give, got, tt.want)
			}
		})
	}
}

func TestReductoFetch(t *testing.T) {
	t.Parallel()

	pdfBytes := []byte("%PDF-1.4 fake pdf data")
	tmpDir := t.TempDir()

	mux := http.NewServeMux()
	mux.HandleFunc("/file.pdf", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdfBytes)
	})
	mux.HandleFunc("/parse", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
		}

		var req reductoParseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		if !strings.HasSuffix(req.Input, "/file.pdf") {
			t.Errorf("expected input ending with /file.pdf, got %q", req.Input)
		}
		if req.Retrieval.Chunking.ChunkMode != "disabled" {
			t.Errorf("expected chunk_mode=disabled, got %q", req.Retrieval.Chunking.ChunkMode)
		}
		gotHyperlinks := false
		for _, item := range req.Formatting.Include {
			if item == "hyperlinks" {
				gotHyperlinks = true
			}
		}
		if !gotHyperlinks {
			t.Errorf("expected formatting.include to contain \"hyperlinks\", got %v", req.Formatting.Include)
		}
		gotFigures := false
		for _, item := range req.Settings.ReturnImages {
			if item == "figure" {
				gotFigures = true
			}
		}
		if !gotFigures {
			t.Errorf("expected settings.return_images to contain \"figure\", got %v", req.Settings.ReturnImages)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"job_id": "j1",
			"result": {
				"type": "full",
				"chunks": [{
					"content": "# Title\n\nbody paragraph",
					"blocks": [
						{"type": "Title", "content": "Title"},
						{"type": "Text", "content": "body paragraph"},
						{"type": "Figure", "content": "Figure 1: example", "image_url": "https://reducto.example/img1.png", "bbox": {"page": 2}},
						{"type": "Figure", "content": "", "image_url": "https://reducto.example/img2.png", "bbox": {"page": 5}},
						{"type": "Figure", "content": "no image here", "image_url": ""}
					]
				}]
			}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	r := NewReducto("test-key")
	r.client = server.Client()
	r.baseURL = server.URL

	archiveDir := filepath.Join(tmpDir, "out")
	result, err := r.Fetch(context.Background(), server.URL+"/file.pdf", archiveDir)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(result.Markdown, "# Title") {
		t.Errorf("expected markdown with title, got: %s", result.Markdown)
	}
	if result.PDFPath != filepath.Join(archiveDir, "document.pdf") {
		t.Errorf("unexpected PDFPath: %s", result.PDFPath)
	}
	if len(result.Figures) != 2 {
		t.Fatalf("expected 2 figures with images, got %d", len(result.Figures))
	}
	if result.Figures[0].URL != "https://reducto.example/img1.png" {
		t.Errorf("figure 0 URL: %q", result.Figures[0].URL)
	}
	if result.Figures[0].Caption != "Figure 1: example" {
		t.Errorf("figure 0 caption: %q", result.Figures[0].Caption)
	}
	if result.Figures[0].Page != 2 {
		t.Errorf("figure 0 page: %d", result.Figures[0].Page)
	}
	if result.Figures[1].URL != "https://reducto.example/img2.png" {
		t.Errorf("figure 1 URL: %q", result.Figures[1].URL)
	}

	saved, err := os.ReadFile(filepath.Join(archiveDir, "document.pdf"))
	if err != nil {
		t.Fatalf("reading saved PDF: %v", err)
	}
	if string(saved) != string(pdfBytes) {
		t.Errorf("saved PDF mismatch")
	}
}

func TestReductoParseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		giveStatus  int
		giveBody    string
		wantErrSubs []string
	}{
		{
			name:        "non-200 status",
			giveStatus:  http.StatusUnauthorized,
			giveBody:    `{"error":"bad key"}`,
			wantErrSubs: []string{"401", "bad key"},
		},
		{
			name:        "empty chunks",
			giveStatus:  http.StatusOK,
			giveBody:    `{"job_id":"j","result":{"type":"full","chunks":[]}}`,
			wantErrSubs: []string{"no chunks"},
		},
		{
			name:        "empty content",
			giveStatus:  http.StatusOK,
			giveBody:    `{"job_id":"j","result":{"type":"full","chunks":[{"content":"   ","blocks":[]}]}}`,
			wantErrSubs: []string{"empty content"},
		},
		{
			name:        "invalid json",
			giveStatus:  http.StatusOK,
			giveBody:    `{not json`,
			wantErrSubs: []string{"parsing response"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.giveStatus)
				_, _ = w.Write([]byte(tt.giveBody))
			}))
			defer server.Close()

			r := NewReducto("test-key")
			r.client = server.Client()
			r.baseURL = server.URL

			_, _, err := r.parse(context.Background(), "https://example.com/x.pdf")
			if err == nil {
				t.Fatalf("expected error")
			}
			for _, s := range tt.wantErrSubs {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error should contain %q, got: %v", s, err)
				}
			}
		})
	}
}

func TestReductoURLResult(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	resultServer := httptest.NewServer(mux)
	defer resultServer.Close()

	mux.HandleFunc("/result", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"full","chunks":[{"content":"hello world","blocks":[]}]}`))
	})
	mux.HandleFunc("/parse", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":"j","result":{"type":"url","url":"` + resultServer.URL + `/result"}}`))
	})

	r := NewReducto("k")
	r.client = resultServer.Client()
	r.baseURL = resultServer.URL

	md, _, err := r.parse(context.Background(), "https://example.com/x.pdf")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if md != "hello world" {
		t.Errorf("got %q, want %q", md, "hello world")
	}
}
