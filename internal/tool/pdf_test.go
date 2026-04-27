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

func TestStripPDFExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give string
		want string
	}{
		{"https://example.com/file.pdf", "https://example.com/file"},
		{"https://example.com/path/to/doc.PDF", "https://example.com/path/to/doc"},
		{"https://example.com/doc.pdf?v=1", "https://example.com/doc?v=1"},
		{"https://example.com/article", "https://example.com/article"},
	}

	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()
			if got := StripPDFExtension(tt.give); got != tt.want {
				t.Errorf("StripPDFExtension(%q) = %q, want %q", tt.give, got, tt.want)
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
	mux.HandleFunc("/extract", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
		}

		var req reductoExtractRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		if !strings.HasSuffix(req.Input, "/file.pdf") {
			t.Errorf("expected input ending with /file.pdf, got %q", req.Input)
		}
		if !req.Settings.DeepExtract {
			t.Errorf("expected deep_extract=true")
		}
		if req.Instructions.SystemPrompt == "" {
			t.Errorf("expected non-empty system_prompt")
		}
		if _, ok := req.Instructions.Schema["properties"]; !ok {
			t.Errorf("expected schema with properties, got %v", req.Instructions.Schema)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":"j1","result":{"markdown":"# Title\n\nbody"}}`))
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

	saved, err := os.ReadFile(filepath.Join(archiveDir, "document.pdf"))
	if err != nil {
		t.Fatalf("reading saved PDF: %v", err)
	}
	if string(saved) != string(pdfBytes) {
		t.Errorf("saved PDF mismatch")
	}
}

func TestReductoExtractErrors(t *testing.T) {
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
			name:        "empty markdown",
			giveStatus:  http.StatusOK,
			giveBody:    `{"job_id":"j","result":{"markdown":""}}`,
			wantErrSubs: []string{"did not contain markdown"},
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

			_, err := r.extract(context.Background(), "https://example.com/x.pdf")
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

func TestParseExtractResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		give    string
		want    string
		wantErr bool
	}{
		{
			name: "single object",
			give: `{"markdown":"# hello"}`,
			want: "# hello",
		},
		{
			name: "array of objects",
			give: `[{"markdown":"first"},{"markdown":"second"}]`,
			want: "first\n\nsecond",
		},
		{
			name:    "empty object",
			give:    `{"markdown":""}`,
			wantErr: true,
		},
		{
			name:    "empty array",
			give:    `[]`,
			wantErr: true,
		},
		{
			name:    "wrong shape",
			give:    `{"foo":"bar"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseExtractResult(json.RawMessage(tt.give))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
