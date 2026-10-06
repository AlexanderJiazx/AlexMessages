package webapp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/debuglog"
	"alexmessage/internal/httpx"
)

// registerTranscribeRoutes wires POST /api/transcribe — voice dictation.
// The browser uploads a short audio clip; the server forwards it to an
// OpenRouter chat-completion model that accepts audio input and returns the
// transcript. The OpenRouter key lives only on the server (OPENROUTER_API_KEY);
// clients never see it.
func registerTranscribeRoutes(r *gin.Engine) {
	r.POST("/api/transcribe", handleTranscribe)
}

// transcribeMaxBytes caps one dictation clip. 16 kHz mono WAV is ~1.9 MB/min,
// so this comfortably covers ten-minute voice notes.
const transcribeMaxBytes = 20 * 1024 * 1024

// openRouterTimeout bounds the upstream transcription call.
const openRouterTimeout = 60 * time.Second

// audioFormatFor maps an uploaded clip to OpenRouter's input_audio format.
// Returns "" when the container can't be identified.
func audioFormatFor(name, contentType string) string {
	ct := strings.ToLower(strings.Split(contentType, ";")[0])
	switch ct {
	case "audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave":
		return "wav"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/mp4", "audio/m4a", "audio/aac", "audio/x-m4a", "video/mp4":
		return "m4a"
	case "audio/webm", "video/webm":
		return "webm"
	case "audio/ogg", "application/ogg":
		return "ogg"
	case "audio/flac", "audio/x-flac":
		return "flac"
	case "audio/aiff", "audio/x-aiff":
		return "aiff"
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".wav":
		return "wav"
	case ".mp3":
		return "mp3"
	case ".m4a", ".mp4", ".aac":
		return "m4a"
	case ".webm":
		return "webm"
	case ".ogg", ".oga":
		return "ogg"
	case ".flac":
		return "flac"
	case ".aif", ".aiff", ".caf":
		return "aiff"
	}
	return ""
}

// transcribeModel picks the transcription model for a clip. Voxtral (Mistral's
// dedicated speech model — the strongest transcript quality on OpenRouter for
// this workload) only accepts wav/mp3; anything else routes to Gemini, whose
// audio ingestion covers m4a/ogg/webm/aiff/flac. Both are env-overridable.
func transcribeModel(format string) string {
	if m := os.Getenv("OPENROUTER_TRANSCRIBE_MODEL"); m != "" {
		return m
	}
	if format == "wav" || format == "mp3" {
		return "mistralai/voxtral-small-24b-2507"
	}
	return "google/gemini-3.1-flash-lite"
}

// openRouterURL is the chat-completions endpoint. Overridable for tests.
func openRouterURL() string {
	if u := os.Getenv("OPENROUTER_BASE_URL"); u != "" {
		return strings.TrimRight(u, "/") + "/chat/completions"
	}
	return "https://openrouter.ai/api/v1/chat/completions"
}

type orContentPart struct {
	Type       string        `json:"type"`
	Text       string        `json:"text,omitempty"`
	InputAudio *orInputAudio `json:"input_audio,omitempty"`
}

type orInputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type orMessage struct {
	Role    string          `json:"role"`
	Content []orContentPart `json:"content"`
}

type orRequest struct {
	Model     string      `json:"model"`
	Messages  []orMessage `json:"messages"`
	MaxTokens int         `json:"max_tokens"`
}

type orResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// transcribePrompt keeps the model to a bare transcript — no commentary, no
// summarizing, no language translation.
const transcribePrompt = "Transcribe this audio exactly as spoken. Output only the transcript, with no commentary, labels, or quotation marks."

func handleTranscribe(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		httpx.Error(c, http.StatusServiceUnavailable, "Transcription is not configured on this server")
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "Missing audio file")
		return
	}
	if fileHeader.Size > transcribeMaxBytes {
		httpx.Error(c, http.StatusRequestEntityTooLarge, "Audio exceeds 20MB limit")
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "Missing audio file")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(f, transcribeMaxBytes+1))
	_ = f.Close()
	if err != nil || len(raw) == 0 {
		httpx.Error(c, http.StatusBadRequest, "Empty audio file")
		return
	}
	if len(raw) > transcribeMaxBytes {
		httpx.Error(c, http.StatusRequestEntityTooLarge, "Audio exceeds 20MB limit")
		return
	}

	format := audioFormatFor(fileHeader.Filename, fileHeader.Header.Get("Content-Type"))
	if format == "" {
		// Sniff as a last resort — browsers report octet-stream for some clips.
		format = audioFormatFor("", http.DetectContentType(raw))
	}
	if format == "" {
		httpx.Error(c, http.StatusBadRequest, "Unsupported audio format")
		return
	}

	payload := orRequest{
		Model: transcribeModel(format),
		Messages: []orMessage{{
			Role: "user",
			Content: []orContentPart{
				{Type: "text", Text: transcribePrompt},
				{Type: "input_audio", InputAudio: &orInputAudio{
					Data:   base64.StdEncoding.EncodeToString(raw),
					Format: format,
				}},
			},
		}},
		MaxTokens: 2048,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, openRouterURL(), bytes.NewReader(body))
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: openRouterTimeout}
	resp, err := client.Do(req)
	if err != nil {
		debuglog.Emit("messages", "warn", "transcribe_upstream", "OpenRouter request failed", map[string]any{"err": err.Error()})
		httpx.Error(c, http.StatusBadGateway, "Transcription service unreachable")
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		httpx.Error(c, http.StatusBadGateway, "Transcription service unreachable")
		return
	}

	var or orResponse
	if err := json.Unmarshal(respBody, &or); err != nil {
		debuglog.Emit("messages", "warn", "transcribe_bad_json", "OpenRouter returned non-JSON", map[string]any{"status": resp.StatusCode})
		httpx.Error(c, http.StatusBadGateway, "Transcription failed")
		return
	}
	if or.Error != nil {
		debuglog.Emit("messages", "warn", "transcribe_upstream_error", "OpenRouter error", map[string]any{"status": resp.StatusCode, "msg": or.Error.Message})
		httpx.Error(c, http.StatusBadGateway, "Transcription failed: "+truncateRunes(or.Error.Message, 160))
		return
	}
	if len(or.Choices) == 0 {
		httpx.Error(c, http.StatusBadGateway, "Transcription returned no result")
		return
	}

	text := trimSpace(or.Choices[0].Message.Content)
	debuglog.Emit("messages", "info", "transcribe_ok", "Audio transcribed", map[string]any{"user": user.Username, "bytes": len(raw), "format": format})
	c.JSON(http.StatusOK, gin.H{"text": text})
}

