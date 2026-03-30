package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

var tweetURLPattern = regexp.MustCompile(`^https?://(?:www\.)?(?:twitter\.com|x\.com)/([^/]+)/status/(\d+)`)

// IsTweetURL returns true if the URL is a Twitter/X post URL.
func IsTweetURL(rawURL string) bool {
	return tweetURLPattern.MatchString(rawURL)
}

type twitterInput struct {
	URL string `json:"url" jsonschema_description:"The Twitter/X post URL to fetch (x.com or twitter.com)"`
}

// TweetResult holds the structured result of fetching a tweet/thread,
// suitable for creating an archive directly without the agent pipeline.
type TweetResult struct {
	Title    string // e.g. "Thread by @user" or first ~100 chars of tweet
	Author   string // "Display Name (@username)"
	Date     string // YYYY-MM-DD
	Type     string // "tweet" or "thread"
	Markdown string // formatted markdown content
}

// Twitter fetches tweets and threads via the X API v2.
type Twitter struct {
	client      *http.Client
	bearerToken string
	verbose     bool
}

func NewTwitter(bearerToken string) *Twitter {
	return &Twitter{
		client:      &http.Client{Timeout: 30 * time.Second},
		bearerToken: bearerToken,
	}
}

// SetVerbose enables verbose logging for the Twitter tool.
func (t *Twitter) SetVerbose(v bool) { t.verbose = v }

func (t *Twitter) Name() string { return "twitter" }

func (t *Twitter) Description() string {
	return "Fetch a Twitter/X post and its full thread using the X API. " +
		"Works with both x.com and twitter.com URLs. " +
		"If the post is part of a thread, the entire thread is returned. " +
		"If the post is a reply, the parent post is included for context. " +
		"Returns the post content formatted as clean markdown including any attached images."
}

func (t *Twitter) InputSchema() anthropic.ToolInputSchemaParam {
	return GenerateSchema[twitterInput]()
}

// parseTweetURL extracts the username and tweet ID from a Twitter/X URL.
func parseTweetURL(rawURL string) (username string, tweetID string, err error) {
	matches := tweetURLPattern.FindStringSubmatch(rawURL)
	if matches == nil {
		return "", "", fmt.Errorf("not a valid Twitter/X post URL: %s", rawURL)
	}
	return matches[1], matches[2], nil
}

// API response types

type twitterAPIResponse struct {
	Data     json.RawMessage  `json:"data"`
	Includes *twitterIncludes `json:"includes"`
	Meta     *twitterMeta     `json:"meta"`
	Errors   []twitterError   `json:"errors"`
}

type tweetData struct {
	ID               string            `json:"id"`
	Text             string            `json:"text"`
	AuthorID         string            `json:"author_id"`
	CreatedAt        string            `json:"created_at"`
	ConversationID   string            `json:"conversation_id"`
	InReplyToUserID  string            `json:"in_reply_to_user_id"`
	ReferencedTweets []referencedTweet `json:"referenced_tweets"`
	Attachments      *tweetAttachments `json:"attachments"`
	PublicMetrics    *tweetMetrics     `json:"public_metrics"`
	NoteTweet        *noteTweet        `json:"note_tweet"`
}

type noteTweet struct {
	Text string `json:"text"`
}

// fullText returns the complete tweet text, preferring note_tweet for long posts.
func (td *tweetData) fullText() string {
	if td.NoteTweet != nil && td.NoteTweet.Text != "" {
		return td.NoteTweet.Text
	}
	return td.Text
}

type referencedTweet struct {
	Type string `json:"type"` // "replied_to", "quoted", "retweeted"
	ID   string `json:"id"`
}

type tweetAttachments struct {
	MediaKeys []string `json:"media_keys"`
}

type tweetMetrics struct {
	LikeCount    int `json:"like_count"`
	RetweetCount int `json:"retweet_count"`
	ReplyCount   int `json:"reply_count"`
	QuoteCount   int `json:"quote_count"`
}

type twitterIncludes struct {
	Media  []twitterMedia `json:"media"`
	Users  []twitterUser  `json:"users"`
	Tweets []tweetData    `json:"tweets"`
}

type twitterMedia struct {
	MediaKey        string         `json:"media_key"`
	Type            string         `json:"type"` // "photo", "video", "animated_gif"
	URL             string         `json:"url"`
	PreviewImageURL string         `json:"preview_image_url"`
	AltText         string         `json:"alt_text"`
	Variants        []mediaVariant `json:"variants"`
}

// bestURL returns the most useful URL for the media item.
func (m *twitterMedia) bestURL() string {
	if m.URL != "" {
		return m.URL
	}
	if m.PreviewImageURL != "" {
		return m.PreviewImageURL
	}
	// For videos, find the highest bitrate mp4 variant
	var bestVariant *mediaVariant
	for i := range m.Variants {
		v := &m.Variants[i]
		if v.ContentType == "video/mp4" {
			if bestVariant == nil || v.BitRate > bestVariant.BitRate {
				bestVariant = v
			}
		}
	}
	if bestVariant != nil {
		return bestVariant.URL
	}
	return ""
}

type mediaVariant struct {
	BitRate     int    `json:"bit_rate"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
}

type twitterUser struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Username        string `json:"username"`
	ProfileImageURL string `json:"profile_image_url"`
}

type twitterMeta struct {
	NextToken   string `json:"next_token"`
	ResultCount int    `json:"result_count"`
}

type twitterError struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Type   string `json:"type"`
}

func (t *Twitter) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params twitterInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	username, tweetID, err := parseTweetURL(params.URL)
	if err != nil {
		return "", err
	}

	// Fetch the target tweet with full expansions
	tweet, includes, err := t.fetchTweet(ctx, tweetID)
	if err != nil {
		return "", fmt.Errorf("fetching tweet: %w", err)
	}

	// Build lookup maps from includes
	userMap := buildUserMap(includes)
	mediaMap := buildMediaMap(includes)

	// Determine if this is a thread or reply and collect all relevant tweets
	tweets, err := t.collectThread(ctx, tweet, includes, username, userMap, mediaMap)
	if err != nil {
		return "", fmt.Errorf("collecting thread: %w", err)
	}

	// Format as markdown
	return formatTweetsMarkdown(tweets, userMap, mediaMap, includes), nil
}

// Fetch fetches a tweet/thread and returns a structured result for direct archiving,
// bypassing the agent extraction pipeline.
func (t *Twitter) Fetch(ctx context.Context, rawURL string) (*TweetResult, error) {
	username, tweetID, err := parseTweetURL(rawURL)
	if err != nil {
		return nil, err
	}

	tweet, includes, err := t.fetchTweet(ctx, tweetID)
	if err != nil {
		return nil, fmt.Errorf("fetching tweet: %w", err)
	}

	userMap := buildUserMap(includes)
	mediaMap := buildMediaMap(includes)

	tweets, err := t.collectThread(ctx, tweet, includes, username, userMap, mediaMap)
	if err != nil {
		return nil, fmt.Errorf("collecting thread: %w", err)
	}

	isThread := isThreadTweets(tweets)

	result := &TweetResult{
		Markdown: formatTweetsMarkdown(tweets, userMap, mediaMap, includes),
	}

	// Author
	if user, ok := userMap[tweets[0].AuthorID]; ok {
		result.Author = fmt.Sprintf("%s (@%s)", user.Name, user.Username)
	}

	// Date from first tweet
	if tweets[0].CreatedAt != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, tweets[0].CreatedAt); parseErr == nil {
			result.Date = parsed.Format("2006-01-02")
		}
	}

	// Title and type
	if isThread {
		result.Type = "thread"
		if user, ok := userMap[tweets[0].AuthorID]; ok {
			result.Title = fmt.Sprintf("Thread by %s (@%s)", user.Name, user.Username)
		} else {
			result.Title = "Thread"
		}
	} else {
		result.Type = "tweet"
		title := tweets[0].fullText()
		// Remove newlines from title
		title = strings.ReplaceAll(title, "\n", " ")
		// Truncate long titles (rune-safe to avoid splitting multi-byte chars)
		runes := []rune(title)
		if len(runes) > 100 {
			title = string(runes[:100]) + "..."
		}
		result.Title = title
	}

	return result, nil
}

// fetchTweet fetches a single tweet by ID with all relevant expansions.
func (t *Twitter) fetchTweet(ctx context.Context, tweetID string) (*tweetData, *twitterIncludes, error) {
	params := url.Values{}
	params.Set("tweet.fields", "author_id,conversation_id,created_at,entities,in_reply_to_user_id,public_metrics,referenced_tweets,attachments,note_tweet")
	params.Set("expansions", "author_id,attachments.media_keys,referenced_tweets.id,referenced_tweets.id.author_id")
	params.Set("user.fields", "name,username,profile_image_url")
	params.Set("media.fields", "type,url,preview_image_url,alt_text,variants")

	apiURL := fmt.Sprintf("https://api.x.com/2/tweets/%s?%s", tweetID, params.Encode())

	body, err := t.doRequest(ctx, apiURL)
	if err != nil {
		return nil, nil, err
	}

	var resp twitterAPIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, fmt.Errorf("parsing response: %w", err)
	}

	if len(resp.Errors) > 0 {
		return nil, nil, fmt.Errorf("API error: %s — %s", resp.Errors[0].Title, resp.Errors[0].Detail)
	}

	var tweet tweetData
	if err := json.Unmarshal(resp.Data, &tweet); err != nil {
		return nil, nil, fmt.Errorf("parsing tweet data: %w", err)
	}

	includes := resp.Includes
	if includes == nil {
		includes = &twitterIncludes{}
	}

	return &tweet, includes, nil
}

// searchConversation searches for all tweets in a conversation by a specific user.
// It tries recent search first, then falls back to full-archive search for older threads.
func (t *Twitter) searchConversation(ctx context.Context, conversationID, username string) ([]tweetData, *twitterIncludes, error) {
	// Try recent search first (last 7 days)
	if t.verbose {
		log.Printf("  searching recent tweets for conversation %s from @%s", conversationID, username)
	}
	tweets, includes, err := t.doConversationSearch(ctx, "recent", conversationID, username)
	if err == nil && len(tweets) > 0 {
		if t.verbose {
			log.Printf("  recent search found %d tweets", len(tweets))
		}
		return tweets, includes, nil
	}
	if t.verbose {
		if err != nil {
			log.Printf("  recent search failed: %v", err)
		} else {
			log.Printf("  recent search returned 0 tweets, trying full-archive search")
		}
	}

	// Fall back to full-archive search for older threads (requires Pro/Enterprise tier)
	tweets, includes, err = t.doConversationSearch(ctx, "all", conversationID, username)
	if err == nil {
		if t.verbose {
			log.Printf("  full-archive search found %d tweets", len(tweets))
		}
		return tweets, includes, nil
	}
	if t.verbose {
		log.Printf("  full-archive search failed: %v", err)
	}

	// Both failed
	return nil, &twitterIncludes{}, nil
}

const maxSearchPages = 10

func (t *Twitter) doConversationSearch(ctx context.Context, searchType, conversationID, username string) ([]tweetData, *twitterIncludes, error) {
	allIncludes := &twitterIncludes{}
	var allTweets []tweetData
	nextToken := ""

	for page := range maxSearchPages {
		_ = page
		params := url.Values{}
		params.Set("query", fmt.Sprintf("conversation_id:%s from:%s", conversationID, username))
		params.Set("tweet.fields", "author_id,conversation_id,created_at,entities,in_reply_to_user_id,public_metrics,referenced_tweets,attachments,note_tweet")
		params.Set("expansions", "author_id,attachments.media_keys,referenced_tweets.id,referenced_tweets.id.author_id")
		params.Set("user.fields", "name,username,profile_image_url")
		params.Set("media.fields", "type,url,preview_image_url,alt_text,variants")
		params.Set("max_results", "100")
		params.Set("sort_order", "recency")
		if nextToken != "" {
			params.Set("next_token", nextToken)
		}

		apiURL := fmt.Sprintf("https://api.x.com/2/tweets/search/%s?%s", searchType, params.Encode())

		body, err := t.doRequest(ctx, apiURL)
		if err != nil {
			return nil, nil, err
		}

		var resp twitterAPIResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, nil, fmt.Errorf("parsing search response: %w", err)
		}

		if resp.Data != nil {
			var tweets []tweetData
			if err := json.Unmarshal(resp.Data, &tweets); err != nil {
				return nil, nil, fmt.Errorf("parsing search tweets: %w", err)
			}
			allTweets = append(allTweets, tweets...)
		}

		if resp.Includes != nil {
			mergeIncludes(allIncludes, resp.Includes)
		}

		if resp.Meta == nil || resp.Meta.NextToken == "" {
			break
		}
		nextToken = resp.Meta.NextToken
	}

	return allTweets, allIncludes, nil
}

func (t *Twitter) doRequest(ctx context.Context, apiURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.bearerToken)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling X API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("x API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// collectThread gathers the self-reply chain containing the target tweet.
// A self-reply chain is a sequence of tweets by the same author where each
// replies directly to the previous one. Replies to other users' tweets in
// the same conversation are NOT included.
func (t *Twitter) collectThread(ctx context.Context, tweet *tweetData, includes *twitterIncludes, username string, userMap map[string]*twitterUser, mediaMap map[string]*twitterMedia) ([]tweetData, error) {
	conversationID := tweet.ConversationID
	if conversationID == "" {
		return []tweetData{*tweet}, nil
	}

	// Gather all candidate tweets into a map
	candidates := map[string]tweetData{tweet.ID: *tweet}
	for _, inc := range includes.Tweets {
		candidates[inc.ID] = inc
	}

	// Determine the thread author username for search
	threadAuthor := username
	if user, ok := userMap[tweet.AuthorID]; ok {
		threadAuthor = user.Username
	}

	// Search for all tweets in this conversation by the same author
	searchTweets, searchIncludes, err := t.searchConversation(ctx, conversationID, threadAuthor)
	if err == nil {
		mergeIncludes(includes, searchIncludes)
		refreshMaps(userMap, mediaMap, searchIncludes)
		for _, st := range searchTweets {
			candidates[st.ID] = st
		}
	}

	// Walk up the reply chain to find ancestors we might be missing
	current := tweet
	for {
		parentID := repliedToID(current)
		if parentID == "" {
			break
		}
		if _, ok := candidates[parentID]; ok {
			// Already have it, keep walking up through it
			p := candidates[parentID]
			current = &p
			continue
		}
		parent, parentIncludes, fetchErr := t.fetchTweet(ctx, parentID)
		if fetchErr != nil {
			break
		}
		mergeIncludes(includes, parentIncludes)
		refreshMaps(userMap, mediaMap, parentIncludes)
		candidates[parent.ID] = *parent
		// Also add any new includes from this fetch
		for _, inc := range parentIncludes.Tweets {
			if _, exists := candidates[inc.ID]; !exists {
				candidates[inc.ID] = inc
			}
		}
		current = parent
	}

	// If we still don't have the conversation root, fetch it
	if _, ok := candidates[conversationID]; !ok {
		root, rootIncludes, fetchErr := t.fetchTweet(ctx, conversationID)
		if fetchErr == nil {
			mergeIncludes(includes, rootIncludes)
			refreshMaps(userMap, mediaMap, rootIncludes)
			candidates[root.ID] = *root
		}
	}

	// Build the self-reply chain and sort chronologically
	chain := buildSelfReplyChain(candidates, tweet.ID, tweet.AuthorID)
	sort.Slice(chain, func(i, j int) bool {
		return tweetIDBefore(chain[i].ID, chain[j].ID)
	})

	return chain, nil
}

// tweetIDBefore compares two tweet IDs numerically.
// Tweet IDs are Snowflake-based uint64s; string comparison would mis-order
// IDs of different lengths (e.g. "9" > "10" lexicographically).
func tweetIDBefore(a, b string) bool {
	ai, errA := strconv.ParseUint(a, 10, 64)
	bi, errB := strconv.ParseUint(b, 10, 64)
	if errA != nil || errB != nil {
		return a < b // fallback to string comparison
	}
	return ai < bi
}

// isThreadTweets returns true if the tweets form a self-reply thread
// (multiple tweets by the same author in the same conversation).
func isThreadTweets(tweets []tweetData) bool {
	if len(tweets) < 2 {
		return false
	}
	convID := tweets[0].ConversationID
	authorID := tweets[0].AuthorID
	count := 0
	for _, tw := range tweets {
		if tw.ConversationID == convID && tw.AuthorID == authorID {
			count++
		}
	}
	return count > 1
}

// findTweet searches for a tweet by ID in the main slice and includes.
func findTweet(id string, tweets []tweetData, includes *twitterIncludes) (tweetData, bool) {
	for _, tw := range tweets {
		if tw.ID == id {
			return tw, true
		}
	}
	if includes != nil {
		for _, tw := range includes.Tweets {
			if tw.ID == id {
				return tw, true
			}
		}
	}
	return tweetData{}, false
}

// repliedToID returns the ID of the tweet this one is replying to, or "".
func repliedToID(tw *tweetData) string {
	for _, ref := range tw.ReferencedTweets {
		if ref.Type == "replied_to" {
			return ref.ID
		}
	}
	return ""
}

// buildSelfReplyChain extracts the self-reply chain containing targetID.
// A self-reply chain is a sequence where each tweet replies to the previous
// one, and all are by the same author. Only tweets connected through this
// chain are included — the author's replies to other people's comments in
// the same conversation are excluded.
func buildSelfReplyChain(candidates map[string]tweetData, targetID, authorID string) []tweetData {
	// Index only tweets by the target author
	byAuthor := map[string]tweetData{}
	for id, tw := range candidates {
		if tw.AuthorID == authorID {
			byAuthor[id] = tw
		}
	}

	// Build parent↔child relationships, but only for self-replies
	// (where the parent is also by the same author)
	parentOf := map[string]string{}     // child → parent
	childrenOf := map[string][]string{} // parent → children
	for id, tw := range byAuthor {
		pid := repliedToID(&tw)
		if pid == "" {
			continue
		}
		if _, ok := byAuthor[pid]; ok {
			parentOf[id] = pid
			childrenOf[pid] = append(childrenOf[pid], id)
		}
	}

	// Walk up from target to find the chain root
	root := targetID
	for {
		parent, ok := parentOf[root]
		if !ok {
			break
		}
		root = parent
	}

	// Walk down from root collecting the chain
	var chain []tweetData
	var walk func(string)
	walk = func(id string) {
		if tw, ok := byAuthor[id]; ok {
			chain = append(chain, tw)
		}
		for _, childID := range childrenOf[id] {
			walk(childID)
		}
	}
	walk(root)

	// If chain is empty (shouldn't happen), at least return the target
	if len(chain) == 0 {
		if tw, ok := candidates[targetID]; ok {
			chain = []tweetData{tw}
		}
	}

	return chain
}

// formatTweetsMarkdown renders a slice of tweets as clean markdown.
// includes provides access to referenced tweets (e.g. quoted tweets) that
// may not be in the main tweets slice.
func formatTweetsMarkdown(tweets []tweetData, userMap map[string]*twitterUser, mediaMap map[string]*twitterMedia, includes *twitterIncludes) string {
	if len(tweets) == 0 {
		return ""
	}

	var sb strings.Builder

	isThread := isThreadTweets(tweets)

	// Title
	firstAuthor := userMap[tweets[0].AuthorID]
	if isThread && firstAuthor != nil {
		fmt.Fprintf(&sb, "# Thread by %s (@%s)\n\n", firstAuthor.Name, firstAuthor.Username)
	} else if len(tweets) == 1 && firstAuthor != nil {
		fmt.Fprintf(&sb, "# Post by %s (@%s)\n\n", firstAuthor.Name, firstAuthor.Username)
	}

	for i, tw := range tweets {
		user := userMap[tw.AuthorID]

		// For threads, show post numbers; for conversations with different authors, show author on each
		if len(tweets) > 1 {
			if isThread && user != nil && user.ID == tweets[0].AuthorID {
				// Thread post by the main author
				fmt.Fprintf(&sb, "## %d.\n\n", threadPostNumber(tweets[:i+1], tweets[0].AuthorID))
			} else if user != nil {
				fmt.Fprintf(&sb, "## %s (@%s)\n\n", user.Name, user.Username)
			}
		}

		// Timestamp
		if tw.CreatedAt != "" {
			if parsed, err := time.Parse(time.RFC3339, tw.CreatedAt); err == nil {
				fmt.Fprintf(&sb, "*%s*\n\n", parsed.Format("January 2, 2006 at 3:04 PM"))
			}
		}

		// Tweet text
		text := tw.fullText()
		// Clean up t.co URLs — they're expanded in entities but for now keep as-is
		sb.WriteString(text)
		sb.WriteString("\n\n")

		// Attached media (images, videos)
		if tw.Attachments != nil {
			for _, key := range tw.Attachments.MediaKeys {
				media, ok := mediaMap[key]
				if !ok {
					continue
				}
				mediaURL := media.bestURL()
				if mediaURL == "" {
					continue
				}
				alt := media.AltText
				switch media.Type {
				case "photo":
					if alt == "" {
						alt = "Image"
					}
					fmt.Fprintf(&sb, "![%s](%s)\n\n", alt, mediaURL)
				case "video", "animated_gif":
					if alt == "" {
						alt = "Video"
					}
					fmt.Fprintf(&sb, "![%s (video)](%s)\n\n", alt, mediaURL)
				}
			}
		}

		// Quoted tweet — search both the main tweets slice and includes
		for _, ref := range tw.ReferencedTweets {
			if ref.Type != "quoted" {
				continue
			}
			qt, found := findTweet(ref.ID, tweets, includes)
			if !found {
				continue
			}
			qUser := userMap[qt.AuthorID]
			if qUser != nil {
				fmt.Fprintf(&sb, "> **%s** (@%s):\n", qUser.Name, qUser.Username)
			}
			for line := range strings.SplitSeq(qt.fullText(), "\n") {
				fmt.Fprintf(&sb, "> %s\n", line)
			}
			sb.WriteString("\n")
		}

		// Metrics (only on the last tweet or standalone tweets)
		if (len(tweets) == 1 || i == len(tweets)-1) && tw.PublicMetrics != nil {
			m := tw.PublicMetrics
			fmt.Fprintf(&sb, "---\n\nLikes: %d · Retweets: %d · Replies: %d",
				m.LikeCount, m.RetweetCount, m.ReplyCount)
			if m.QuoteCount > 0 {
				fmt.Fprintf(&sb, " · Quotes: %d", m.QuoteCount)
			}
			sb.WriteString("\n")
		}

		// Separator between tweets (not after the last one)
		if i < len(tweets)-1 {
			sb.WriteString("\n---\n\n")
		}
	}

	return sb.String()
}

// threadPostNumber counts how many tweets by authorID appear up to and including the current slice.
func threadPostNumber(tweetsUpTo []tweetData, authorID string) int {
	count := 0
	for _, tw := range tweetsUpTo {
		if tw.AuthorID == authorID {
			count++
		}
	}
	return count
}

func buildUserMap(includes *twitterIncludes) map[string]*twitterUser {
	m := make(map[string]*twitterUser)
	if includes == nil {
		return m
	}
	for i := range includes.Users {
		m[includes.Users[i].ID] = &includes.Users[i]
	}
	return m
}

func buildMediaMap(includes *twitterIncludes) map[string]*twitterMedia {
	m := make(map[string]*twitterMedia)
	if includes == nil {
		return m
	}
	for i := range includes.Media {
		m[includes.Media[i].MediaKey] = &includes.Media[i]
	}
	return m
}

func mergeIncludes(dst, src *twitterIncludes) {
	if src == nil {
		return
	}
	dst.Users = append(dst.Users, src.Users...)
	dst.Media = append(dst.Media, src.Media...)
	dst.Tweets = append(dst.Tweets, src.Tweets...)
}

func refreshMaps(userMap map[string]*twitterUser, mediaMap map[string]*twitterMedia, includes *twitterIncludes) {
	if includes == nil {
		return
	}
	for i := range includes.Users {
		userMap[includes.Users[i].ID] = &includes.Users[i]
	}
	for i := range includes.Media {
		mediaMap[includes.Media[i].MediaKey] = &includes.Media[i]
	}
}
