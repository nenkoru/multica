package streams

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Transcriber turns a voice note into text.
type Transcriber interface {
	Transcribe(ctx context.Context, filename string, audio io.Reader) (string, error)
}

// WhisperClient calls an OpenAI-compatible POST /v1/audio/transcriptions
// (faster-whisper, whisper.cpp server, vLLM). Token, when set, goes as a
// Bearer header (the fabrika-whisper service requires one).
type WhisperClient struct {
	BaseURL string
	Model   string
	Token   string
	HTTP    *http.Client
}

// NewWhisperClient returns nil for an empty URL, which keeps voice off. The
// URL is the service root; a full endpoint URL is accepted too.
func NewWhisperClient(baseURL, model, token string) *WhisperClient {
	baseURL = strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(baseURL), "/"), "/v1/audio/transcriptions")
	if baseURL == "" {
		return nil
	}
	if model == "" {
		model = "whisper-1"
	}
	return &WhisperClient{BaseURL: baseURL, Model: model, Token: strings.TrimSpace(token), HTTP: &http.Client{Timeout: 90 * time.Second}}
}

func (c *WhisperClient) Transcribe(ctx context.Context, filename string, audio io.Reader) (string, error) {
	if filename == "" {
		filename = "voice.webm"
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, io.LimitReader(audio, MaxVoiceBytes)); err != nil {
		return "", err
	}
	_ = mw.WriteField("model", c.Model)
	_ = mw.WriteField("language", "ru")
	_ = mw.WriteField("response_format", "json")
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whisper: %d %s", res.StatusCode, firstLine(string(raw), 200))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("whisper: bad response: %w", err)
	}
	text := strings.TrimSpace(out.Text)
	if text == "" {
		return "", errors.New("whisper: пустой текст — повтори громче или ближе к микрофону")
	}
	return text, nil
}
