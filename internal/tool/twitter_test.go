package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseTweetURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		url          string
		wantUsername string
		wantTweetID  string
		wantErr      bool
	}{
		{
			name:         "x.com URL",
			url:          "https://x.com/elonmusk/status/1234567890",
			wantUsername: "elonmusk",
			wantTweetID:  "1234567890",
		},
		{
			name:         "twitter.com URL",
			url:          "https://twitter.com/jack/status/9876543210",
			wantUsername: "jack",
			wantTweetID:  "9876543210",
		},
		{
			name:         "www.x.com URL",
			url:          "https://www.x.com/user/status/111222333",
			wantUsername: "user",
			wantTweetID:  "111222333",
		},
		{
			name:         "www.twitter.com URL",
			url:          "https://www.twitter.com/user/status/111222333",
			wantUsername: "user",
			wantTweetID:  "111222333",
		},
		{
			name:         "URL with query params",
			url:          "https://x.com/user/status/123?s=20&t=abc",
			wantUsername: "user",
			wantTweetID:  "123",
		},
		{
			name:    "not a tweet URL",
			url:     "https://x.com/user",
			wantErr: true,
		},
		{
			name:    "different domain",
			url:     "https://example.com/user/status/123",
			wantErr: true,
		},
		{
			name:    "empty string",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			username, tweetID, err := parseTweetURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if username != tt.wantUsername {
				t.Errorf("username: got %q, want %q", username, tt.wantUsername)
			}
			if tweetID != tt.wantTweetID {
				t.Errorf("tweetID: got %q, want %q", tweetID, tt.wantTweetID)
			}
		})
	}
}

func TestIsTweetURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url  string
		want bool
	}{
		{"https://x.com/user/status/123", true},
		{"https://twitter.com/user/status/123", true},
		{"https://www.x.com/user/status/123", true},
		{"https://example.com/page", false},
		{"https://x.com/user", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()
			if got := IsTweetURL(tt.url); got != tt.want {
				t.Errorf("IsTweetURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestTwitterFetch(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/tweets/456") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": tweetData{
					ID:             "456",
					Text:           "A short tweet",
					AuthorID:       "u1",
					CreatedAt:      "2024-03-20T15:00:00Z",
					ConversationID: "456",
					PublicMetrics:  &tweetMetrics{LikeCount: 5, RetweetCount: 1, ReplyCount: 0},
				},
				"includes": twitterIncludes{
					Users: []twitterUser{
						{ID: "u1", Name: "Author", Username: "author"},
					},
				},
			})
			return
		}
		// Search returns empty
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []tweetData{},
			"meta": twitterMeta{ResultCount: 0},
		})
	}

	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	tw := &Twitter{
		client:      server.Client(),
		bearerToken: "test-token",
	}
	tw.client.Transport = rewriteHostTransport{
		target:    server.URL,
		transport: server.Client().Transport,
	}

	result, err := tw.Fetch(context.Background(), "https://x.com/author/status/456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Type != "tweet" {
		t.Errorf("type: got %q, want %q", result.Type, "tweet")
	}
	if result.Author != "Author (@author)" {
		t.Errorf("author: got %q, want %q", result.Author, "Author (@author)")
	}
	if result.Date != "2024-03-20" {
		t.Errorf("date: got %q, want %q", result.Date, "2024-03-20")
	}
	if !strings.Contains(result.Title, "A short tweet") {
		t.Errorf("title should contain tweet text, got %q", result.Title)
	}
	if !strings.Contains(result.Markdown, "A short tweet") {
		t.Errorf("markdown should contain tweet text, got:\n%s", result.Markdown)
	}
}

func TestBuildSelfReplyChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		candidates map[string]tweetData
		targetID   string
		authorID   string
		wantIDs    []string
	}{
		{
			name: "standalone tweet",
			candidates: map[string]tweetData{
				"1": {ID: "1", AuthorID: "a", ConversationID: "1"},
			},
			targetID: "1",
			authorID: "a",
			wantIDs:  []string{"1"},
		},
		{
			name: "self-reply thread",
			candidates: map[string]tweetData{
				"1": {ID: "1", AuthorID: "a", ConversationID: "1"},
				"2": {ID: "2", AuthorID: "a", ConversationID: "1", ReferencedTweets: []referencedTweet{{Type: "replied_to", ID: "1"}}},
				"3": {ID: "3", AuthorID: "a", ConversationID: "1", ReferencedTweets: []referencedTweet{{Type: "replied_to", ID: "2"}}},
			},
			targetID: "1",
			authorID: "a",
			wantIDs:  []string{"1", "2", "3"},
		},
		{
			name: "excludes reply to other user",
			candidates: map[string]tweetData{
				"1": {ID: "1", AuthorID: "a", ConversationID: "1"},
				// other user's reply (not by author a)
				"2": {ID: "2", AuthorID: "b", ConversationID: "1", ReferencedTweets: []referencedTweet{{Type: "replied_to", ID: "1"}}},
				// author a replies to user b — NOT a self-reply
				"3": {ID: "3", AuthorID: "a", ConversationID: "1", ReferencedTweets: []referencedTweet{{Type: "replied_to", ID: "2"}}},
			},
			targetID: "1",
			authorID: "a",
			wantIDs:  []string{"1"}, // only the original, NOT tweet 3
		},
		{
			name: "target in middle of thread finds whole chain",
			candidates: map[string]tweetData{
				"1": {ID: "1", AuthorID: "a", ConversationID: "1"},
				"2": {ID: "2", AuthorID: "a", ConversationID: "1", ReferencedTweets: []referencedTweet{{Type: "replied_to", ID: "1"}}},
				"3": {ID: "3", AuthorID: "a", ConversationID: "1", ReferencedTweets: []referencedTweet{{Type: "replied_to", ID: "2"}}},
			},
			targetID: "2",
			authorID: "a",
			wantIDs:  []string{"1", "2", "3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chain := buildSelfReplyChain(tt.candidates, tt.targetID, tt.authorID)
			gotIDs := make([]string, len(chain))
			for i, tw := range chain {
				gotIDs[i] = tw.ID
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Fatalf("got %v, want %v", gotIDs, tt.wantIDs)
			}
			for i, id := range tt.wantIDs {
				if gotIDs[i] != id {
					t.Errorf("index %d: got %q, want %q (full: %v)", i, gotIDs[i], id, gotIDs)
				}
			}
		})
	}
}

func TestFormatTweetsMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		tweets       []tweetData
		users        map[string]*twitterUser
		media        map[string]*twitterMedia
		wantContains []string
	}{
		{
			name: "single tweet",
			tweets: []tweetData{
				{
					ID:             "1",
					Text:           "Hello world!",
					AuthorID:       "u1",
					CreatedAt:      "2024-01-15T12:00:00Z",
					ConversationID: "1",
					PublicMetrics:  &tweetMetrics{LikeCount: 42, RetweetCount: 10, ReplyCount: 5},
				},
			},
			users: map[string]*twitterUser{
				"u1": {ID: "u1", Name: "Test User", Username: "testuser"},
			},
			media:        map[string]*twitterMedia{},
			wantContains: []string{"Post by Test User (@testuser)", "Hello world!", "January 15, 2024", "Likes: 42", "Retweets: 10"},
		},
		{
			name: "tweet with image",
			tweets: []tweetData{
				{
					ID:             "1",
					Text:           "Check this out",
					AuthorID:       "u1",
					CreatedAt:      "2024-01-15T12:00:00Z",
					ConversationID: "1",
					Attachments:    &tweetAttachments{MediaKeys: []string{"m1"}},
				},
			},
			users: map[string]*twitterUser{
				"u1": {ID: "u1", Name: "Test User", Username: "testuser"},
			},
			media: map[string]*twitterMedia{
				"m1": {MediaKey: "m1", Type: "photo", URL: "https://pbs.twimg.com/media/test.jpg", AltText: "A test image"},
			},
			wantContains: []string{"![A test image](https://pbs.twimg.com/media/test.jpg)"},
		},
		{
			name: "thread",
			tweets: []tweetData{
				{
					ID:             "1",
					Text:           "Thread start",
					AuthorID:       "u1",
					CreatedAt:      "2024-01-15T12:00:00Z",
					ConversationID: "1",
				},
				{
					ID:               "2",
					Text:             "Thread continues",
					AuthorID:         "u1",
					CreatedAt:        "2024-01-15T12:01:00Z",
					ConversationID:   "1",
					ReferencedTweets: []referencedTweet{{Type: "replied_to", ID: "1"}},
				},
				{
					ID:               "3",
					Text:             "Thread ends",
					AuthorID:         "u1",
					CreatedAt:        "2024-01-15T12:02:00Z",
					ConversationID:   "1",
					ReferencedTweets: []referencedTweet{{Type: "replied_to", ID: "2"}},
					PublicMetrics:    &tweetMetrics{LikeCount: 100, RetweetCount: 50, ReplyCount: 25},
				},
			},
			users: map[string]*twitterUser{
				"u1": {ID: "u1", Name: "Test User", Username: "testuser"},
			},
			media:        map[string]*twitterMedia{},
			wantContains: []string{"Thread by Test User (@testuser)", "## 1.", "Thread start", "## 2.", "Thread continues", "## 3.", "Thread ends", "Likes: 100"},
		},
		{
			name: "reply to another user",
			tweets: []tweetData{
				{
					ID:             "1",
					Text:           "Original post",
					AuthorID:       "u1",
					CreatedAt:      "2024-01-15T12:00:00Z",
					ConversationID: "1",
				},
				{
					ID:               "2",
					Text:             "My reply",
					AuthorID:         "u2",
					CreatedAt:        "2024-01-15T12:05:00Z",
					ConversationID:   "1",
					ReferencedTweets: []referencedTweet{{Type: "replied_to", ID: "1"}},
					PublicMetrics:    &tweetMetrics{LikeCount: 5, RetweetCount: 1, ReplyCount: 0},
				},
			},
			users: map[string]*twitterUser{
				"u1": {ID: "u1", Name: "Original Author", Username: "original"},
				"u2": {ID: "u2", Name: "Replier", Username: "replier"},
			},
			media:        map[string]*twitterMedia{},
			wantContains: []string{"Original Author", "Original post", "Replier", "My reply"},
		},
		{
			name: "note tweet with long text",
			tweets: []tweetData{
				{
					ID:             "1",
					Text:           "Truncated text...",
					AuthorID:       "u1",
					CreatedAt:      "2024-01-15T12:00:00Z",
					ConversationID: "1",
					NoteTweet:      &noteTweet{Text: "This is the full long-form text of the note tweet that exceeds 280 characters."},
				},
			},
			users: map[string]*twitterUser{
				"u1": {ID: "u1", Name: "Test User", Username: "testuser"},
			},
			media:        map[string]*twitterMedia{},
			wantContains: []string{"This is the full long-form text"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := formatTweetsMarkdown(tt.tweets, tt.users, tt.media)
			for _, s := range tt.wantContains {
				if !strings.Contains(result, s) {
					t.Errorf("result should contain %q, got:\n%s", s, result)
				}
			}
		})
	}
}

func TestTwitterExecute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		giveInput       json.RawMessage
		giveHandler     func(t *testing.T) http.HandlerFunc
		wantContains    []string
		wantErr         bool
		wantErrContains []string
	}{
		{
			name: "single tweet",
			giveHandler: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Errorf("expected Bearer test-token, got %s", r.Header.Get("Authorization"))
					}

					w.Header().Set("Content-Type", "application/json")
					// Single tweet lookup
					if strings.Contains(r.URL.Path, "/tweets/123") {
						_ = json.NewEncoder(w).Encode(map[string]any{
							"data": tweetData{
								ID:             "123",
								Text:           "Test tweet content",
								AuthorID:       "u1",
								CreatedAt:      "2024-06-15T10:30:00Z",
								ConversationID: "123",
								PublicMetrics:  &tweetMetrics{LikeCount: 10, RetweetCount: 2, ReplyCount: 1},
							},
							"includes": twitterIncludes{
								Users: []twitterUser{
									{ID: "u1", Name: "Test User", Username: "testuser"},
								},
							},
						})
						return
					}
					// Search returns empty (no thread)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"data": []tweetData{},
						"meta": twitterMeta{ResultCount: 0},
					})
				}
			},
			wantContains: []string{"Test User", "@testuser", "Test tweet content", "Likes: 10"},
		},
		{
			name:      "invalid input",
			giveInput: json.RawMessage(`{invalid`),
			wantErr:   true,
		},
		{
			name:            "invalid URL",
			giveInput:       mustMarshal(twitterInput{URL: "https://example.com/not-twitter"}),
			wantErr:         true,
			wantErrContains: []string{"not a valid Twitter/X post URL"},
		},
		{
			name: "API error",
			giveHandler: func(_ *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"errors":[{"title":"Unauthorized","detail":"Invalid token"}]}`))
				}
			},
			wantErr:         true,
			wantErrContains: []string{"401"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var tw *Twitter
			input := tt.giveInput

			if tt.giveHandler != nil {
				server := httptest.NewServer(tt.giveHandler(t))
				defer server.Close()

				tw = &Twitter{
					client:      server.Client(),
					bearerToken: "test-token",
				}
				// Override API base URL by wrapping the tool with a custom transport
				tw.client.Transport = rewriteHostTransport{
					target:    server.URL,
					transport: server.Client().Transport,
				}

				if input == nil {
					input = mustMarshal(twitterInput{URL: "https://x.com/testuser/status/123"})
				}
			} else {
				tw = NewTwitter("test-token")
			}

			result, err := tw.Execute(context.Background(), input)
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
			for _, s := range tt.wantContains {
				if !strings.Contains(result, s) {
					t.Errorf("result should contain %q, got:\n%s", s, result)
				}
			}
		})
	}
}

// rewriteHostTransport redirects all requests to the test server.
type rewriteHostTransport struct {
	target    string
	transport http.RoundTripper
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Preserve the original path and query
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.target, "http://")
	return t.transport.RoundTrip(req)
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
