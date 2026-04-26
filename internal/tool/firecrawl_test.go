package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFirecrawlMarkdownExecute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		giveHandler     func(t *testing.T) http.HandlerFunc
		giveInput       json.RawMessage
		giveAPIKey      string
		wantContains    []string
		wantResult      string
		wantErr         bool
		wantErrContains []string
	}{
		{
			name: "success",
			giveHandler: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						t.Errorf("expected POST, got %s", r.Method)
					}
					if r.URL.Path != "/v2/scrape" {
						t.Errorf("expected /v2/scrape, got %s", r.URL.Path)
					}
					if r.Header.Get("Authorization") != "Bearer test-key" {
						t.Errorf("expected Bearer test-key, got %s", r.Header.Get("Authorization"))
					}
					if r.Header.Get("Content-Type") != "application/json" {
						t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
					}

					var reqBody struct {
						URL     string   `json:"url"`
						Formats []string `json:"formats"`
					}
					if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
						t.Errorf("decoding request body: %v", err)
						return
					}
					if reqBody.URL != "https://example.com/article" {
						t.Errorf("expected url, got %q", reqBody.URL)
					}
					if len(reqBody.Formats) != 1 || reqBody.Formats[0] != "markdown" {
						t.Errorf("expected formats [markdown], got %v", reqBody.Formats)
					}

					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"# Hello\n\nworld","metadata":{}}}`))
				}
			},
			wantContains: []string{"# Hello", "world"},
		},
		{
			name: "empty markdown",
			giveHandler: func(_ *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":""}}`))
				}
			},
			wantErr:         true,
			wantErrContains: []string{"empty markdown"},
		},
		{
			name: "unsuccessful response",
			giveHandler: func(_ *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"success":false,"error":"site blocked"}`))
				}
			},
			wantErr:         true,
			wantErrContains: []string{"unsuccessful", "site blocked"},
		},
		{
			name:       "invalid input",
			giveAPIKey: "test-key",
			giveInput:  json.RawMessage(`{invalid`),
			wantErr:    true,
		},
		{
			name: "api error",
			giveHandler: func(_ *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
				}
			},
			giveAPIKey:      "bad-key",
			wantErr:         true,
			wantErrContains: []string{"401", "invalid api key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var tool *FirecrawlMarkdown
			input := tt.giveInput

			if tt.giveHandler != nil {
				server := httptest.NewServer(tt.giveHandler(t))
				defer server.Close()

				apiKey := tt.giveAPIKey
				if apiKey == "" {
					apiKey = "test-key"
				}

				tool = &FirecrawlMarkdown{
					client:  server.Client(),
					apiKey:  apiKey,
					baseURL: server.URL,
				}

				if input == nil {
					input, _ = json.Marshal(firecrawlInput{URL: "https://example.com/article"})
				}
			} else {
				tool = NewFirecrawlMarkdown(tt.giveAPIKey)
			}

			result, err := tool.Execute(context.Background(), input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				for _, s := range tt.wantErrContains {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("error should contain %q, got: %v", s, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantResult != "" && result != tt.wantResult {
				t.Errorf("got %q, want %q", result, tt.wantResult)
			}
			for _, s := range tt.wantContains {
				if !strings.Contains(result, s) {
					t.Errorf("result should contain %q, got: %s", s, result)
				}
			}
		})
	}
}

func TestFirecrawlContentExecute(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			Formats []string `json:"formats"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("decoding request body: %v", err)
			return
		}
		if len(reqBody.Formats) != 1 || reqBody.Formats[0] != "html" {
			t.Errorf("expected formats [html], got %v", reqBody.Formats)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"html":"<h1>hi</h1>"}}`))
	}))
	defer server.Close()

	tool := &FirecrawlContent{
		client:  server.Client(),
		apiKey:  "test-key",
		baseURL: server.URL,
	}
	input, _ := json.Marshal(firecrawlInput{URL: "https://example.com"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "<h1>hi</h1>") {
		t.Errorf("expected html in result, got: %s", result)
	}
}

func TestFirecrawlNamesAndDescriptions(t *testing.T) {
	t.Parallel()

	md := NewFirecrawlMarkdown("k")
	if md.Name() != "firecrawl_markdown" {
		t.Errorf("unexpected name: %s", md.Name())
	}
	if !strings.Contains(md.Description(), "backup") {
		t.Errorf("description should mention backup role: %s", md.Description())
	}

	html := NewFirecrawlContent("k")
	if html.Name() != "firecrawl_content" {
		t.Errorf("unexpected name: %s", html.Name())
	}
	if !strings.Contains(html.Description(), "backup") {
		t.Errorf("description should mention backup role: %s", html.Description())
	}
}
