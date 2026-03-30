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
	t.Parallel()

	tests := []struct {
		name         string
		giveHandler  http.HandlerFunc // nil means no server; uses giveInput directly
		giveInput    json.RawMessage  // if nil and giveHandler is set, input is built from the server URL
		wantContains string
		wantErr      bool
	}{
		{
			name: "success",
			giveHandler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("<html><body><h1>Test</h1></body></html>"))
			},
			wantContains: "<h1>Test</h1>",
		},
		{
			name: "not found",
			giveHandler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantErr: true,
		},
		{
			name:      "invalid input",
			giveInput: json.RawMessage(`not json`),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tool := NewHTTPFetch()

			input := tt.giveInput
			if input == nil {
				srv := httptest.NewServer(tt.giveHandler)
				defer srv.Close()

				input, _ = json.Marshal(httpFetchInput{URL: srv.URL})
			}

			result, err := tool.Execute(context.Background(), input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantContains != "" && !strings.Contains(result, tt.wantContains) {
				t.Errorf("result does not contain %q, got: %s", tt.wantContains, result)
			}
		})
	}
}
