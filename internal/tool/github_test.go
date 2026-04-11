package tool

import (
	"testing"
)

func TestParseGitHubURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give      string
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
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()

			owner, repo, err := parseGitHubURL(tt.give)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGitHubURL(%q) error = %v, wantErr %v", tt.give, err, tt.wantErr)
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

func TestIsGitHubURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give string
		want bool
	}{
		{"https://github.com/owner/repo", true},
		{"https://example.com/page", false},
	}

	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			t.Parallel()

			if got := isGitHubURL(tt.give); got != tt.want {
				t.Errorf("isGitHubURL(%q) = %v, want %v", tt.give, got, tt.want)
			}
		})
	}
}
