package tool

import (
	"testing"
)

func TestParseGitHubURL(t *testing.T) {
	tests := []struct {
		url       string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{"https://github.com/owner/repo", "owner", "repo", false},
		{"https://github.com/owner/repo/tree/main/src", "owner", "repo", false},
		{"http://github.com/owner/repo", "owner", "repo", false},
		{"github.com/owner/repo", "owner", "repo", false},
		{"https://github.com/imbue-ai/offload", "imbue-ai", "offload", false},
		{"https://github.com/owner/repo.git", "owner", "repo", false},
		{"https://github.com/owner/repo/", "owner", "repo", false},
		{"https://example.com/owner/repo", "", "", true},
		{"https://github.com/", "", "", true},
		{"https://github.com/owner", "", "", true},
		{"not a url", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			owner, repo, err := parseGitHubURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGitHubURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
				return
			}
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}

func Test_isGitHubURL(t *testing.T) {
	if !isGitHubURL("https://github.com/owner/repo") {
		t.Error("expected true for github URL")
	}
	if isGitHubURL("https://example.com/page") {
		t.Error("expected false for non-github URL")
	}
}
