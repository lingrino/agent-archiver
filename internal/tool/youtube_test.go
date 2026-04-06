package tool

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestIsYouTubeURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"https://youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=120", true},
		{"https://youtu.be/dQw4w9WgXcQ", true},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", true},
		{"https://youtube.com/shorts/dQw4w9WgXcQ", true},
		{"http://youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"https://example.com/watch?v=dQw4w9WgXcQ", false},
		{"https://youtube.com/channel/UCxyz", false},
		{"https://youtube.com/", false},
		{"https://x.com/user/status/123", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()
			if got := IsYouTubeURL(tt.url); got != tt.want {
				t.Errorf("IsYouTubeURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestParseYouTubeVideoID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantID  string
		wantErr bool
	}{
		{
			name:   "standard watch URL",
			url:    "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			wantID: "dQw4w9WgXcQ",
		},
		{
			name:   "short URL",
			url:    "https://youtu.be/dQw4w9WgXcQ",
			wantID: "dQw4w9WgXcQ",
		},
		{
			name:   "shorts URL",
			url:    "https://www.youtube.com/shorts/dQw4w9WgXcQ",
			wantID: "dQw4w9WgXcQ",
		},
		{
			name:   "with extra params",
			url:    "https://www.youtube.com/watch?v=abc123def45&list=PLxyz",
			wantID: "abc123def45",
		},
		{
			name:   "with hyphen and underscore",
			url:    "https://www.youtube.com/watch?v=a-b_c1234d5",
			wantID: "a-b_c1234d5",
		},
		{
			name:    "not a YouTube URL",
			url:     "https://example.com/watch?v=dQw4w9WgXcQ",
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
			id, err := ParseYouTubeVideoID(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tt.wantID {
				t.Errorf("got %q, want %q", id, tt.wantID)
			}
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		seconds float64
		want    string
	}{
		{0, "00:00:00"},
		{5.5, "00:00:05"},
		{65, "00:01:05"},
		{3661.9, "01:01:01"},
		{7200, "02:00:00"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := formatTimestamp(tt.seconds); got != tt.want {
				t.Errorf("formatTimestamp(%v) = %q, want %q", tt.seconds, got, tt.want)
			}
		})
	}
}

func TestBuildSpeakerTurns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		words     []elevenlabsWord
		wantTurns int
		wantFirst string // first turn speaker ID
		wantTexts []string
	}{
		{
			name:      "empty transcript",
			words:     nil,
			wantTurns: 0,
		},
		{
			name: "single speaker",
			words: []elevenlabsWord{
				{Text: "Hello", Start: 0, Type: "word", SpeakerID: "speaker_0"},
				{Text: " ", Start: 0.3, Type: "spacing", SpeakerID: "speaker_0"},
				{Text: "world", Start: 0.5, Type: "word", SpeakerID: "speaker_0"},
			},
			wantTurns: 1,
			wantFirst: "speaker_0",
			wantTexts: []string{"Hello world"},
		},
		{
			name: "two speakers alternating",
			words: []elevenlabsWord{
				{Text: "Hi", Start: 0, Type: "word", SpeakerID: "speaker_0"},
				{Text: " ", Start: 0.2, Type: "spacing", SpeakerID: "speaker_0"},
				{Text: "there", Start: 0.3, Type: "word", SpeakerID: "speaker_0"},
				{Text: "Hey", Start: 1.0, Type: "word", SpeakerID: "speaker_1"},
				{Text: " ", Start: 1.2, Type: "spacing", SpeakerID: "speaker_1"},
				{Text: "back", Start: 1.3, Type: "word", SpeakerID: "speaker_1"},
				{Text: "Cool", Start: 2.0, Type: "word", SpeakerID: "speaker_0"},
			},
			wantTurns: 3,
			wantFirst: "speaker_0",
			wantTexts: []string{"Hi there", "Hey back", "Cool"},
		},
		{
			name: "skips audio events",
			words: []elevenlabsWord{
				{Text: "Hello", Start: 0, End: 0.3, Type: "word", SpeakerID: "speaker_0"},
				{Text: "(laughter)", Start: 0.5, End: 0.7, Type: "audio_event", SpeakerID: "speaker_0"},
				{Text: " ", Start: 0.8, End: 0.8, Type: "spacing", SpeakerID: "speaker_0"},
				{Text: "world", Start: 1.0, End: 1.3, Type: "word", SpeakerID: "speaker_0"},
			},
			wantTurns: 1,
			wantFirst: "speaker_0",
			wantTexts: []string{"Hello world"},
		},
		{
			name: "splits same speaker on pause at sentence boundary",
			words: func() []elevenlabsWord {
				// First sentence spoken from 0-35s
				words := []elevenlabsWord{
					{Text: "First", Start: 0, End: 0.5, Type: "word", SpeakerID: "speaker_0"},
					{Text: " ", Start: 0.5, End: 0.5, Type: "spacing", SpeakerID: "speaker_0"},
					{Text: "sentence.", Start: 30, End: 35, Type: "word", SpeakerID: "speaker_0"},
				}
				// 2-second pause, then second sentence at 37s
				words = append(words,
					elevenlabsWord{Text: " ", Start: 35, End: 35, Type: "spacing", SpeakerID: "speaker_0"},
					elevenlabsWord{Text: "Second", Start: 37, End: 37.5, Type: "word", SpeakerID: "speaker_0"},
					elevenlabsWord{Text: " ", Start: 37.5, End: 37.5, Type: "spacing", SpeakerID: "speaker_0"},
					elevenlabsWord{Text: "part.", Start: 37.5, End: 38, Type: "word", SpeakerID: "speaker_0"},
				)
				return words
			}(),
			wantTurns: 2,
			wantFirst: "speaker_0",
			wantTexts: []string{"First sentence.", "Second part."},
		},
		{
			name: "no split when pause is short",
			words: []elevenlabsWord{
				{Text: "Hello.", Start: 0, End: 0.5, Type: "word", SpeakerID: "speaker_0"},
				{Text: " ", Start: 0.5, End: 0.5, Type: "spacing", SpeakerID: "speaker_0"},
				// Only 0.5s gap — not enough to split
				{Text: "World.", Start: 1.0, End: 1.5, Type: "word", SpeakerID: "speaker_0"},
			},
			wantTurns: 1,
			wantFirst: "speaker_0",
			wantTexts: []string{"Hello. World."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			transcript := &elevenlabsResponse{Words: tt.words}
			turns := buildSpeakerTurns(transcript)

			if len(turns) != tt.wantTurns {
				t.Fatalf("got %d turns, want %d", len(turns), tt.wantTurns)
			}
			if tt.wantTurns == 0 {
				return
			}
			if turns[0].SpeakerID != tt.wantFirst {
				t.Errorf("first turn speaker: got %q, want %q", turns[0].SpeakerID, tt.wantFirst)
			}
			for i, want := range tt.wantTexts {
				if i >= len(turns) {
					break
				}
				if turns[i].Text != want {
					t.Errorf("turn %d text: got %q, want %q", i, turns[i].Text, want)
				}
			}
		})
	}
}

func TestFormatYouTubeMarkdown(t *testing.T) {
	t.Parallel()

	transcript := &elevenlabsResponse{
		Words: []elevenlabsWord{
			{Text: "Welcome", Start: 0, Type: "word", SpeakerID: "speaker_0"},
			{Text: " ", Start: 0.3, Type: "spacing", SpeakerID: "speaker_0"},
			{Text: "everyone", Start: 0.5, Type: "word", SpeakerID: "speaker_0"},
			{Text: "Thanks", Start: 2.0, Type: "word", SpeakerID: "speaker_1"},
			{Text: " ", Start: 2.2, Type: "spacing", SpeakerID: "speaker_1"},
			{Text: "John", Start: 2.3, Type: "word", SpeakerID: "speaker_1"},
		},
	}

	speakers := speakerMap{
		"speaker_0": "John Doe",
		"speaker_1": "Jane Smith",
	}

	meta := &videoMetadata{
		Title:       "Test Video",
		Description: "A test video description.",
	}

	result := formatYouTubeMarkdown(transcript, speakers, meta)

	wantContains := []string{
		"## Description",
		"A test video description.",
		"## Transcript",
		"**[00:00:00] John Doe:** Welcome everyone",
		"**[00:00:02] Jane Smith:** Thanks John",
	}

	for _, s := range wantContains {
		if !strings.Contains(result, s) {
			t.Errorf("result should contain %q, got:\n%s", s, result)
		}
	}
}

func TestSpeakerSamples(t *testing.T) {
	t.Parallel()

	transcript := &elevenlabsResponse{
		Words: []elevenlabsWord{
			{Text: "word1", Type: "word", SpeakerID: "a"},
			{Text: " ", Type: "spacing", SpeakerID: "a"},
			{Text: "word2", Type: "word", SpeakerID: "a"},
			{Text: "word3", Type: "word", SpeakerID: "b"},
		},
	}

	samples := speakerSamples(transcript, []string{"a", "b"}, 10)
	if !strings.Contains(samples["a"], "word1") {
		t.Errorf("speaker a sample should contain word1, got %q", samples["a"])
	}
	if !strings.Contains(samples["b"], "word3") {
		t.Errorf("speaker b sample should contain word3, got %q", samples["b"])
	}
}

func TestUniqueSpeakerIDs(t *testing.T) {
	t.Parallel()

	transcript := &elevenlabsResponse{
		Words: []elevenlabsWord{
			{SpeakerID: "b"},
			{SpeakerID: "a"},
			{SpeakerID: "b"},
			{SpeakerID: "a"},
			{SpeakerID: "c"},
		},
	}

	ids := uniqueSpeakerIDs(transcript)
	if len(ids) != 3 {
		t.Fatalf("got %d unique IDs, want 3", len(ids))
	}
	// Should be sorted
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("got %v, want [a b c]", ids)
	}
}

func TestFallbackSpeakerMap(t *testing.T) {
	t.Parallel()

	transcript := &elevenlabsResponse{
		Words: []elevenlabsWord{
			{SpeakerID: "speaker_1"},
			{SpeakerID: "speaker_0"},
		},
	}

	m := fallbackSpeakerMap(transcript)
	if m["speaker_0"] != "Speaker 1" {
		t.Errorf("speaker_0: got %q, want %q", m["speaker_0"], "Speaker 1")
	}
	if m["speaker_1"] != "Speaker 2" {
		t.Errorf("speaker_1: got %q, want %q", m["speaker_1"], "Speaker 2")
	}
}

func TestTranscribeRequest(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method and auth
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("xi-api-key") != "test-key" {
			t.Errorf("expected xi-api-key test-key, got %s", r.Header.Get("xi-api-key"))
		}

		// Verify multipart form
		contentType := r.Header.Get("Content-Type")
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			t.Fatalf("parsing content type: %v", err)
		}

		reader := multipart.NewReader(r.Body, params["boundary"])
		fields := map[string]string{}
		hasFile := false

		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("reading multipart: %v", err)
			}

			if part.FileName() != "" {
				hasFile = true
			} else {
				data, _ := io.ReadAll(part)
				fields[part.FormName()] = string(data)
			}
			_ = part.Close()
		}

		if !hasFile {
			t.Error("expected file in multipart form")
		}
		if fields["model_id"] != "scribe_v2" {
			t.Errorf("model_id: got %q, want %q", fields["model_id"], "scribe_v2")
		}
		if fields["diarize"] != "true" {
			t.Errorf("diarize: got %q, want %q", fields["diarize"], "true")
		}
		if fields["timestamps_granularity"] != "word" {
			t.Errorf("timestamps_granularity: got %q, want %q", fields["timestamps_granularity"], "word")
		}

		// Return mock response
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(elevenlabsResponse{
			LanguageCode: "eng",
			Text:         "Hello world",
			Words: []elevenlabsWord{
				{Text: "Hello", Start: 0, End: 0.5, Type: "word", SpeakerID: "speaker_0"},
				{Text: " ", Start: 0.5, End: 0.5, Type: "spacing", SpeakerID: "speaker_0"},
				{Text: "world", Start: 0.5, End: 1.0, Type: "word", SpeakerID: "speaker_0"},
			},
		})
	}))
	defer server.Close()

	// Create a temp audio file
	tmpFile := t.TempDir() + "/test.m4a"
	if err := writeTestFile(tmpFile, "fake audio data"); err != nil {
		t.Fatalf("creating temp file: %v", err)
	}

	yt := &YouTube{
		client:        server.Client(),
		elevenLabsKey: "test-key",
	}
	yt.client.Transport = rewriteHostTransport{
		target:    server.URL,
		transport: server.Client().Transport,
	}

	result, err := yt.transcribe(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Words) != 3 {
		t.Errorf("got %d words, want 3", len(result.Words))
	}
	if result.Words[0].SpeakerID != "speaker_0" {
		t.Errorf("first word speaker: got %q, want %q", result.Words[0].SpeakerID, "speaker_0")
	}
}

func TestTranscribeAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": "invalid api key"}`))
	}))
	defer server.Close()

	tmpFile := t.TempDir() + "/test.m4a"
	if err := writeTestFile(tmpFile, "fake audio data"); err != nil {
		t.Fatalf("creating temp file: %v", err)
	}

	yt := &YouTube{
		client:        server.Client(),
		elevenLabsKey: "bad-key",
	}
	yt.client.Transport = rewriteHostTransport{
		target:    server.URL,
		transport: server.Client().Transport,
	}

	_, err := yt.transcribe(context.Background(), tmpFile)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should contain 401, got: %v", err)
	}
}

func TestBuildSpeakerIDPrompt(t *testing.T) {
	t.Parallel()

	meta := &videoMetadata{
		Title:       "Interview with Jane Smith",
		Channel:     "TechPodcast",
		Uploader:    "TechPodcast",
		Description: "In this episode we talk to Jane Smith about AI.",
	}

	samples := map[string]string{
		"speaker_0": "Welcome to the show today",
		"speaker_1": "Thanks for having me",
	}

	prompt := buildSpeakerIDPrompt(meta, samples, "search results here")

	wantContains := []string{
		"Interview with Jane Smith",
		"TechPodcast",
		"In this episode we talk to Jane Smith about AI.",
		"speaker_0",
		"speaker_1",
		"Welcome to the show today",
		"Thanks for having me",
		"search results here",
	}

	for _, s := range wantContains {
		if !strings.Contains(prompt, s) {
			t.Errorf("prompt should contain %q", s)
		}
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
