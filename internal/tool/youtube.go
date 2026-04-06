package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

var youtubeURLPattern = regexp.MustCompile(
	`^https?://(?:(?:www|m)\.)?(?:youtube\.com/(?:watch\?.*v=|shorts/)|youtu\.be/)([a-zA-Z0-9_-]{11})`,
)

// IsYouTubeURL returns true if the URL is a YouTube video URL.
func IsYouTubeURL(rawURL string) bool {
	return youtubeURLPattern.MatchString(rawURL)
}

// ParseYouTubeVideoID extracts the 11-character video ID from a YouTube URL.
func ParseYouTubeVideoID(rawURL string) (string, error) {
	matches := youtubeURLPattern.FindStringSubmatch(rawURL)
	if matches == nil {
		return "", fmt.Errorf("not a valid YouTube URL: %s", rawURL)
	}
	return matches[1], nil
}

// YtDlpAvailable returns true if yt-dlp is in PATH.
func YtDlpAvailable() bool {
	_, err := exec.LookPath("yt-dlp")
	return err == nil
}

// FfmpegAvailable returns true if ffmpeg is in PATH.
func FfmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// YouTubeResult holds the structured result of processing a YouTube video,
// suitable for creating an archive directly without the agent pipeline.
type YouTubeResult struct {
	Title    string
	Author   string
	Date     string
	Type     string // always "video"
	Markdown string // description + formatted transcript
}

// YouTube downloads, transcribes, and archives YouTube videos.
type YouTube struct {
	client        *http.Client
	elevenLabsKey string
	exaAPIKey     string
	anthClient    *anthropic.Client
	model         anthropic.Model
	verbose       bool
}

func NewYouTube(elevenLabsKey, exaAPIKey string, anthClient *anthropic.Client, model anthropic.Model) *YouTube {
	return &YouTube{
		client:        &http.Client{Timeout: 15 * time.Minute},
		elevenLabsKey: elevenLabsKey,
		exaAPIKey:     exaAPIKey,
		anthClient:    anthClient,
		model:         model,
	}
}

// SetVerbose enables verbose logging.
func (yt *YouTube) SetVerbose(v bool) { yt.verbose = v }

// Fetch downloads a YouTube video, transcribes it, identifies speakers, and
// returns a structured result for direct archiving (bypassing the agent pipeline).
// The video is saved as video.mp4 in archiveDir.
func (yt *YouTube) Fetch(ctx context.Context, rawURL, archiveDir string) (*YouTubeResult, error) {
	videoID, err := ParseYouTubeVideoID(rawURL)
	if err != nil {
		return nil, err
	}

	// Step 1: Download video via yt-dlp
	if yt.verbose {
		log.Printf("  downloading video %s via yt-dlp", videoID)
	}
	meta, videoPath, err := yt.downloadVideo(ctx, videoID, archiveDir)
	if err != nil {
		return nil, fmt.Errorf("downloading video: %w", err)
	}
	if yt.verbose {
		log.Printf("  video saved to %s", videoPath)
	}

	// Step 2: Extract audio for transcription
	if yt.verbose {
		log.Printf("  extracting audio from video")
	}
	audioPath, err := yt.extractAudio(ctx, videoPath)
	if err != nil {
		return nil, fmt.Errorf("extracting audio: %w", err)
	}
	defer func() { _ = os.Remove(audioPath) }()

	// Step 3: Transcribe via ElevenLabs
	if yt.verbose {
		log.Printf("  transcribing audio via ElevenLabs (this may take a while)")
	}
	transcript, err := yt.transcribe(ctx, audioPath)
	if err != nil {
		return nil, fmt.Errorf("transcribing: %w", err)
	}
	if yt.verbose {
		log.Printf("  transcription complete: %d words", len(transcript.Words))
	}

	// Step 4: Identify speakers
	speakers := yt.identifySpeakersOrFallback(ctx, transcript, meta)
	if yt.verbose {
		for id, name := range speakers {
			log.Printf("  speaker %s → %s", id, name)
		}
	}

	// Step 5: Format transcript as markdown
	markdown := formatYouTubeMarkdown(transcript, speakers, meta)

	// Build result
	date := ""
	if meta.UploadDate != "" && len(meta.UploadDate) == 8 {
		date = meta.UploadDate[:4] + "-" + meta.UploadDate[4:6] + "-" + meta.UploadDate[6:8]
	}

	author := meta.Channel
	if author == "" {
		author = meta.Uploader
	}

	return &YouTubeResult{
		Title:    meta.Title,
		Author:   author,
		Date:     date,
		Type:     "video",
		Markdown: markdown,
	}, nil
}

// videoMetadata holds metadata extracted from yt-dlp's info JSON.
type videoMetadata struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Uploader    string `json:"uploader"`
	Channel     string `json:"channel"`
	UploadDate  string `json:"upload_date"` // YYYYMMDD
	Duration    int    `json:"duration"`
}

func (yt *YouTube) downloadVideo(ctx context.Context, videoID, outputDir string) (*videoMetadata, string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, "", fmt.Errorf("creating output directory: %w", err)
	}

	videoPath := filepath.Join(outputDir, "video.mp4")
	outputTemplate := filepath.Join(outputDir, "video.%(ext)s")

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"-f", "bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
		"--merge-output-format", "mp4",
		"--write-info-json",
		"-o", outputTemplate,
		"https://www.youtube.com/watch?v="+videoID,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("yt-dlp failed: %s", stderr.String())
	}

	// Read the info JSON that yt-dlp writes alongside the video
	infoPath := filepath.Join(outputDir, "video.info.json")
	infoData, err := os.ReadFile(infoPath)
	if err != nil {
		return nil, "", fmt.Errorf("reading info JSON: %w", err)
	}
	// Clean up info JSON — we don't need it in the archive
	defer func() { _ = os.Remove(infoPath) }()

	var meta videoMetadata
	if err := json.Unmarshal(infoData, &meta); err != nil {
		return nil, "", fmt.Errorf("parsing info JSON: %w", err)
	}

	return &meta, videoPath, nil
}

func (yt *YouTube) extractAudio(ctx context.Context, videoPath string) (string, error) {
	dir := filepath.Dir(videoPath)
	audioPath := filepath.Join(dir, "audio.m4a")

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", videoPath,
		"-vn",
		"-acodec", "copy",
		"-y", // overwrite if exists
		audioPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg failed: %s", stderr.String())
	}

	return audioPath, nil
}

// ElevenLabs API types

type elevenlabsResponse struct {
	LanguageCode string           `json:"language_code"`
	Text         string           `json:"text"`
	Words        []elevenlabsWord `json:"words"`
}

type elevenlabsWord struct {
	Text      string  `json:"text"`
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
	Type      string  `json:"type"` // "word", "spacing", "punctuation", "audio_event"
	SpeakerID string  `json:"speaker_id"`
}

func (yt *YouTube) transcribe(ctx context.Context, audioPath string) (*elevenlabsResponse, error) {
	f, err := os.Open(audioPath)
	if err != nil {
		return nil, fmt.Errorf("opening audio file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Add the audio file
	part, err := writer.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return nil, fmt.Errorf("creating form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, fmt.Errorf("copying audio data: %w", err)
	}

	// Add parameters
	_ = writer.WriteField("model_id", "scribe_v2")
	_ = writer.WriteField("diarize", "true")
	_ = writer.WriteField("timestamps_granularity", "word")
	_ = writer.WriteField("tag_audio_events", "false")

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("closing multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.elevenlabs.io/v1/speech-to-text", &body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("xi-api-key", yt.elevenLabsKey)

	resp, err := yt.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling ElevenLabs API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ElevenLabs API returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result elevenlabsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &result, nil
}

// Speaker identification

type speakerMap map[string]string // speaker_id → display name

// identifySpeakersOrFallback attempts speaker identification via LLM+search,
// falling back to generic labels on any error.
func (yt *YouTube) identifySpeakersOrFallback(ctx context.Context, transcript *elevenlabsResponse, meta *videoMetadata) speakerMap {
	speakers, err := yt.identifySpeakers(ctx, transcript, meta)
	if err != nil {
		if yt.verbose {
			log.Printf("  speaker identification failed, using generic labels: %v", err)
		}
		return fallbackSpeakerMap(transcript)
	}
	return speakers
}

func fallbackSpeakerMap(transcript *elevenlabsResponse) speakerMap {
	ids := uniqueSpeakerIDs(transcript)
	m := make(speakerMap, len(ids))
	for i, id := range ids {
		m[id] = fmt.Sprintf("Speaker %d", i+1)
	}
	return m
}

func uniqueSpeakerIDs(transcript *elevenlabsResponse) []string {
	seen := map[string]bool{}
	var ids []string
	for _, w := range transcript.Words {
		if w.SpeakerID != "" && !seen[w.SpeakerID] {
			seen[w.SpeakerID] = true
			ids = append(ids, w.SpeakerID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (yt *YouTube) identifySpeakers(ctx context.Context, transcript *elevenlabsResponse, meta *videoMetadata) (speakerMap, error) {
	ids := uniqueSpeakerIDs(transcript)
	if len(ids) == 0 {
		return speakerMap{}, nil
	}

	// Collect speaker dialogue samples
	samples := speakerSamples(transcript, ids, 200)

	// Gather search context if Exa key is available
	var searchContext string
	if yt.exaAPIKey != "" {
		searchContext = yt.searchForSpeakers(ctx, meta)
	}

	// Build the identification prompt
	prompt := buildSpeakerIDPrompt(meta, samples, searchContext)

	// Call LLM for identification
	return yt.callSpeakerIDLLM(ctx, prompt, ids)
}

// speakerSamples extracts the first ~maxWords words spoken by each speaker.
func speakerSamples(transcript *elevenlabsResponse, speakerIDs []string, maxWords int) map[string]string {
	counts := make(map[string]int, len(speakerIDs))
	builders := make(map[string]*strings.Builder, len(speakerIDs))
	for _, id := range speakerIDs {
		builders[id] = &strings.Builder{}
	}

	for _, w := range transcript.Words {
		b, ok := builders[w.SpeakerID]
		if !ok || counts[w.SpeakerID] >= maxWords {
			continue
		}
		if w.Type == "word" {
			counts[w.SpeakerID]++
		}
		b.WriteString(w.Text)
	}

	result := make(map[string]string, len(speakerIDs))
	for _, id := range speakerIDs {
		result[id] = strings.TrimSpace(builders[id].String())
	}
	return result
}

func (yt *YouTube) searchForSpeakers(ctx context.Context, meta *videoMetadata) string {
	exa := NewExaSearch(yt.exaAPIKey)

	query := fmt.Sprintf("%q %s", meta.Title, meta.Channel)
	input, _ := json.Marshal(exaSearchInput{Query: query})

	result, err := exa.Execute(ctx, input)
	if err != nil {
		if yt.verbose {
			log.Printf("  web search for speakers failed: %v", err)
		}
		return ""
	}
	return result
}

func buildSpeakerIDPrompt(meta *videoMetadata, samples map[string]string, searchContext string) string {
	var sb strings.Builder

	sb.WriteString("You are identifying speakers in a YouTube video transcript. ")
	sb.WriteString("Based on the video metadata and context provided, map each speaker label to a real name.\n\n")

	fmt.Fprintf(&sb, "Video Title: %s\n", meta.Title)
	fmt.Fprintf(&sb, "Channel: %s\n", meta.Channel)
	if meta.Uploader != "" && meta.Uploader != meta.Channel {
		fmt.Fprintf(&sb, "Uploader: %s\n", meta.Uploader)
	}

	if meta.Description != "" {
		desc := meta.Description
		if len(desc) > 3000 {
			desc = desc[:3000] + "..."
		}
		fmt.Fprintf(&sb, "\nVideo Description:\n%s\n", desc)
	}

	if searchContext != "" {
		fmt.Fprintf(&sb, "\nWeb search results about this video:\n%s\n", searchContext)
	}

	sb.WriteString("\nSpeaker dialogue samples:\n")
	for id, sample := range samples {
		text := sample
		if len(text) > 500 {
			text = text[:500] + "..."
		}
		fmt.Fprintf(&sb, "\n%s: \"%s\"\n", id, text)
	}

	sb.WriteString("\nInstructions:\n")
	sb.WriteString("- The channel host is typically the one who asks questions or introduces topics.\n")
	sb.WriteString("- Look for names mentioned in the title, description, and search results.\n")
	sb.WriteString("- If a speaker introduces themselves, use that name.\n")
	sb.WriteString("- If you cannot confidently identify a speaker, use a descriptive fallback like \"Host\" or \"Guest\".\n")

	return sb.String()
}

type speakerIdentificationResult struct {
	Speakers []identifiedSpeaker `json:"speakers" jsonschema_description:"Mapping of speaker IDs to identified names"`
}

type identifiedSpeaker struct {
	SpeakerID string `json:"speaker_id" jsonschema_description:"The speaker label from the transcript (e.g. speaker_0)"`
	Name      string `json:"name" jsonschema_description:"The identified real name, or a descriptive label like Host or Guest"`
}

const submitSpeakerIDToolName = "submit_speaker_identification"

func (yt *YouTube) callSpeakerIDLLM(ctx context.Context, prompt string, speakerIDs []string) (speakerMap, error) {
	schema := GenerateSchema[speakerIdentificationResult]()
	tool := anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name:        submitSpeakerIDToolName,
			Description: anthropic.String("Submit the identified speaker names."),
			InputSchema: schema,
		},
	}

	msg, err := yt.anthClient.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     yt.model,
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		Tools:      []anthropic.ToolUnionParam{tool},
		ToolChoice: anthropic.ToolChoiceUnionParam{OfTool: &anthropic.ToolChoiceToolParam{Name: submitSpeakerIDToolName}},
	})
	if err != nil {
		return nil, fmt.Errorf("calling LLM for speaker identification: %w", err)
	}

	for _, block := range msg.Content {
		if block.Type != "tool_use" || block.Name != submitSpeakerIDToolName {
			continue
		}
		var result speakerIdentificationResult
		if err := json.Unmarshal(block.Input, &result); err != nil {
			return nil, fmt.Errorf("parsing speaker identification result: %w", err)
		}

		m := make(speakerMap, len(result.Speakers))
		for _, s := range result.Speakers {
			m[s.SpeakerID] = s.Name
		}

		// Ensure all speaker IDs have a mapping
		for i, id := range speakerIDs {
			if _, ok := m[id]; !ok {
				m[id] = fmt.Sprintf("Speaker %d", i+1)
			}
		}

		return m, nil
	}

	return nil, fmt.Errorf("LLM did not return speaker identification")
}

// Transcript formatting

func formatYouTubeMarkdown(transcript *elevenlabsResponse, speakers speakerMap, meta *videoMetadata) string {
	var sb strings.Builder

	// Description section
	if meta.Description != "" {
		sb.WriteString("## Description\n\n")
		sb.WriteString(meta.Description)
		sb.WriteString("\n\n")
	}

	// Transcript section
	sb.WriteString("## Transcript\n\n")

	turns := buildSpeakerTurns(transcript)
	if len(turns) == 0 {
		sb.WriteString("*No speech detected in audio.*\n\n")
	}
	for _, turn := range turns {
		name := speakers[turn.SpeakerID]
		if name == "" {
			name = turn.SpeakerID
		}
		timestamp := formatTimestamp(turn.Start)
		fmt.Fprintf(&sb, "**[%s] %s:** %s\n\n", timestamp, name, turn.Text)
	}

	return sb.String()
}

type speakerTurn struct {
	SpeakerID string
	Start     float64
	Text      string
}

// pauseThreshold is the minimum gap (in seconds) between words that indicates
// a natural pause suitable for starting a new paragraph.
const pauseThreshold = 1.5

// minTurnDuration is the minimum duration (in seconds) of a speaker turn before
// we consider splitting it at a pause. Prevents overly short paragraphs.
const minTurnDuration = 30.0

func buildSpeakerTurns(transcript *elevenlabsResponse) []speakerTurn {
	if len(transcript.Words) == 0 {
		return nil
	}

	var turns []speakerTurn
	var current *speakerTurn
	var lastEnd float64

	for _, w := range transcript.Words {
		// Skip audio events
		if w.Type == "audio_event" {
			continue
		}

		speakerChanged := current != nil && w.SpeakerID != current.SpeakerID && w.SpeakerID != ""

		// Check if we should split within the same speaker on a natural pause.
		// Split when: same speaker, pause exceeds threshold, turn is long enough,
		// and the previous text ended at a sentence boundary.
		pauseSplit := false
		if current != nil && !speakerChanged && w.Type == "word" {
			gap := w.Start - lastEnd
			duration := w.Start - current.Start
			if gap >= pauseThreshold && duration >= minTurnDuration && endsWithSentence(current.Text) {
				pauseSplit = true
			}
		}

		if current == nil || speakerChanged || pauseSplit {
			// Finalize the current turn
			if current != nil {
				current.Text = strings.TrimSpace(current.Text)
				turns = append(turns, *current)
			}
			sid := w.SpeakerID
			if sid == "" && current != nil {
				sid = current.SpeakerID
			}
			current = &speakerTurn{
				SpeakerID: sid,
				Start:     w.Start,
			}
		}

		current.Text += w.Text
		if w.End > 0 {
			lastEnd = w.End
		}
	}

	if current != nil {
		current.Text = strings.TrimSpace(current.Text)
		if current.Text != "" {
			turns = append(turns, *current)
		}
	}

	return turns
}

// endsWithSentence returns true if the text ends with sentence-ending punctuation.
func endsWithSentence(text string) bool {
	trimmed := strings.TrimRight(text, " \t\n")
	if trimmed == "" {
		return false
	}
	last := trimmed[len(trimmed)-1]
	return last == '.' || last == '?' || last == '!'
}

func formatTimestamp(seconds float64) string {
	total := int(seconds)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
