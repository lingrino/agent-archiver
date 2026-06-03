package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckLiveness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		wantAlive  bool
		wantStatus int
	}{
		{"ok", http.StatusOK, true, http.StatusOK},
		{"not found", http.StatusNotFound, false, http.StatusNotFound},
		{"gone", http.StatusGone, false, http.StatusGone},
		{"server error", http.StatusInternalServerError, false, http.StatusInternalServerError},
		{"forbidden treated as alive", http.StatusForbidden, true, http.StatusForbidden},
		{"unauthorized treated as alive", http.StatusUnauthorized, true, http.StatusUnauthorized},
		{"rate limited treated as alive", http.StatusTooManyRequests, true, http.StatusTooManyRequests},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			got := CheckLiveness(context.Background(), srv.URL)
			if got.Alive != tt.wantAlive {
				t.Errorf("Alive = %v, want %v (reason: %q)", got.Alive, tt.wantAlive, got.Reason)
			}
			if got.StatusCode != tt.wantStatus {
				t.Errorf("StatusCode = %d, want %d", got.StatusCode, tt.wantStatus)
			}
		})
	}
}

func TestCheckLivenessFollowsRedirect(t *testing.T) {
	t.Parallel()

	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, final.URL, http.StatusMovedPermanently)
	}))
	defer redirector.Close()

	got := CheckLiveness(context.Background(), redirector.URL)
	if !got.Alive {
		t.Fatalf("expected alive after redirect to 200, got dead: %q", got.Reason)
	}
	if !got.Redirected {
		t.Errorf("expected Redirected = true")
	}
	if got.FinalURL != final.URL {
		t.Errorf("FinalURL = %q, want %q", got.FinalURL, final.URL)
	}
}

func TestCheckLivenessUnreachable(t *testing.T) {
	t.Parallel()

	// Closed server -> connection refused -> dead.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	got := CheckLiveness(context.Background(), url)
	if got.Alive {
		t.Errorf("expected dead for unreachable host, got alive")
	}
	if got.Reason == "" {
		t.Errorf("expected a reason for unreachable host")
	}
}
