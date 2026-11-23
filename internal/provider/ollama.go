package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kuini-1/llm-eval-bench/internal/config"
)

// Ollama uses the native /api/chat endpoint rather than Ollama's
// OpenAI-compatible one, because only the native API takes a JSON schema
// for constrained output and a switch for thinking models.
type Ollama struct {
	baseURL, model string
	think          *bool
	http           *http.Client
}

func NewOllama(m config.Model) *Ollama {
	base := m.BaseURL
	if base == "" {
		base = "http://127.0.0.1:11434"
	}
	return &Ollama{baseURL: strings.TrimRight(base, "/"), model: m.Model, think: m.Think, http: &http.Client{Timeout: m.Timeout.Duration}}
}

type olRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Stream   bool           `json:"stream"`
	Think    *bool          `json:"think,omitempty"`
	Format   map[string]any `json:"format,omitempty"`
	Options  map[string]any `json:"options"`
}

type olResponse struct {
	Model   string `json:"model"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int64  `json:"prompt_eval_count"`
	EvalCount       int64  `json:"eval_count"`
	LoadDuration    int64  `json:"load_duration"` // ns
	Error           string `json:"error"`
}

func (o *Ollama) Complete(ctx context.Context, r Request) (*Response, error) {
	body := olRequest{Model: o.model, Stream: false, Think: o.think, Format: r.JSONSchema,
		Options: map[string]any{"num_predict": r.MaxTokens, "seed": 42}}
	if r.Temperature != nil {
		body.Options["temperature"] = *r.Temperature
	}
	if r.System != "" {
		body.Messages = append(body.Messages, Message{Role: "system", Content: r.System})
	}
	body.Messages = append(body.Messages, r.Messages...)
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := o.http.Do(req)
	if err != nil {
		return nil, Classify(0, "", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	var out olResponse
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK {
		msg := out.Error
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return nil, Classify(resp.StatusCode, msg, nil)
	}
	finish := "stop"
	if out.DoneReason == "length" {
		finish = "length"
	}
	// Loading the model into memory isn't part of answering. The runner warms
	// models up first, but subtract any load time that still slips in.
	latency := time.Since(start) - time.Duration(out.LoadDuration)
	return &Response{
		Text: stripThinking(out.Message.Content), Model: out.Model, FinishReason: finish, Latency: latency,
		InputTokens: out.PromptEvalCount, OutputTokens: out.EvalCount,
	}, nil
}

// stripThinking drops reasoning that some thinking models still write into
// the answer when thinking is switched off: everything up to the last
// </think> tag.
func stripThinking(s string) string {
	if i := strings.LastIndex(s, "</think>"); i >= 0 {
		return strings.TrimSpace(s[i+len("</think>"):])
	}
	return strings.TrimSpace(s)
}
