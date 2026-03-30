package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExaSearchExecute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		giveHandler     func(t *testing.T) http.HandlerFunc // nil means no server; uses giveAPIKey + giveInput directly
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
					if r.Header.Get("x-api-key") != "test-key" {
						t.Errorf("expected x-api-key header to be test-key, got %s", r.Header.Get("x-api-key"))
					}
					if r.Header.Get("Content-Type") != "application/json" {
						t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
					}

					var reqBody exaRequest
					if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
						t.Errorf("decoding request body: %v", err)
						return
					}
					if reqBody.Query != "test query" {
						t.Errorf("expected query 'test query', got %q", reqBody.Query)
					}
					if reqBody.Type != "auto" {
						t.Errorf("expected type 'auto', got %q", reqBody.Type)
					}
					if reqBody.NumResults != 5 {
						t.Errorf("expected numResults 5, got %d", reqBody.NumResults)
					}
					if reqBody.Contents.Highlights.NumHighlightsPerURL != 3 {
						t.Errorf("expected numHighlightsPerUrl 3, got %d", reqBody.Contents.Highlights.NumHighlightsPerURL)
					}
					if reqBody.Contents.Highlights.MaxCharacters != 4000 {
						t.Errorf("expected maxCharacters 4000, got %d", reqBody.Contents.Highlights.MaxCharacters)
					}

					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(exaResponse{
						Results: []exaResult{
							{
								Title:         "First Result",
								URL:           "https://example.com/first",
								PublishedDate: "2024-01-15",
								Highlights:    []string{"This is a highlight from the first result."},
							},
							{
								Title:      "Second Result",
								URL:        "https://example.com/second",
								Highlights: []string{"Another highlight here."},
							},
						},
					})
				}
			},
			wantContains: []string{
				"First Result",
				"https://example.com/first",
				"Published: 2024-01-15",
				"This is a highlight",
				"Second Result",
			},
		},
		{
			name: "no results",
			giveHandler: func(_ *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(exaResponse{Results: []exaResult{}})
				}
			},
			wantResult: "No results found.",
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
					_, _ = w.Write([]byte(`{"error": "invalid api key"}`))
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

			var tool *ExaSearch
			input := tt.giveInput

			if tt.giveHandler != nil {
				server := httptest.NewServer(tt.giveHandler(t))
				defer server.Close()

				apiKey := tt.giveAPIKey
				if apiKey == "" {
					apiKey = "test-key"
				}

				tool = &ExaSearch{
					client:  server.Client(),
					apiKey:  apiKey,
					baseURL: server.URL,
				}

				if input == nil {
					input, _ = json.Marshal(exaSearchInput{Query: "test query"})
				}
			} else {
				tool = NewExaSearch(tt.giveAPIKey)
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
