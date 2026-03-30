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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			t.Fatalf("decoding request body: %v", err)
		}
		if reqBody.Query != "test query" {
			t.Errorf("expected query 'test query', got %q", reqBody.Query)
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
	}))
	defer server.Close()

	tool := &ExaSearch{
		client:  server.Client(),
		apiKey:  "test-key",
		baseURL: server.URL,
	}

	input, _ := json.Marshal(exaSearchInput{Query: "test query"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if !strings.Contains(result, "First Result") {
		t.Error("result should contain first result title")
	}
	if !strings.Contains(result, "https://example.com/first") {
		t.Error("result should contain first result URL")
	}
	if !strings.Contains(result, "Published: 2024-01-15") {
		t.Error("result should contain published date")
	}
	if !strings.Contains(result, "This is a highlight") {
		t.Error("result should contain highlight")
	}
	if !strings.Contains(result, "Second Result") {
		t.Error("result should contain second result title")
	}
}

func TestExaSearchExecuteNoResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(exaResponse{Results: []exaResult{}})
	}))
	defer server.Close()

	tool := &ExaSearch{
		client:  server.Client(),
		apiKey:  "test-key",
		baseURL: server.URL,
	}

	input, _ := json.Marshal(exaSearchInput{Query: "obscure query"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if result != "No results found." {
		t.Errorf("expected 'No results found.', got %q", result)
	}
}

func TestExaSearchExecuteInvalidInput(t *testing.T) {
	tool := NewExaSearch("test-key")

	_, err := tool.Execute(context.Background(), []byte(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid input")
	}
}

func TestExaSearchExecuteAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	tool := &ExaSearch{
		client:  server.Client(),
		apiKey:  "bad-key",
		baseURL: server.URL,
	}

	input, _ := json.Marshal(exaSearchInput{Query: "test"})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status code, got: %v", err)
	}
}
