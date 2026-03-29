package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPFetchExecute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body><h1>Test</h1></body></html>"))
	}))
	defer srv.Close()

	tool := NewHTTPFetch()

	input, _ := json.Marshal(httpFetchInput{URL: srv.URL})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "<h1>Test</h1>") {
		t.Errorf("expected HTML content, got: %s", result)
	}
}

func TestHTTPFetchExecuteNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tool := NewHTTPFetch()

	input, _ := json.Marshal(httpFetchInput{URL: srv.URL})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for 404 response")
	}
}

func TestHTTPFetchExecuteInvalidInput(t *testing.T) {
	tool := NewHTTPFetch()
	_, err := tool.Execute(context.Background(), []byte("not json"))
	if err == nil {
		t.Error("expected error for invalid input")
	}
}
