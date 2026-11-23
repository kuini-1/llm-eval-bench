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

// OpenAI speaks the OpenAI chat completions format, which DeepSeek, vLLM,
// llama.cpp's server and most other hosted or self-hosted APIs share.
type OpenAI struct {
	baseURL, apiKey, model string
	http                   *http.Client
}

func NewOpenAI(m config.Model) *OpenAI {
	base := m.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &OpenAI{baseURL: strings.TrimRight(base, "/"), apiKey: m.APIKey, model: m.Model, http: &http.Client{Timeout: m.Timeout.Duration}}
}

type oaRequest struct {
	Model          string         `json:"model"`
	Messages       []Message      `json:"messages"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	Temperature    *float64       `json:"temperature,omitempty"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
}

type oaResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      struct{ Content string } `json:"message"`
		FinishReason string                   `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func (o *OpenAI) Complete(ctx context.Context, r Request) (*Response, error) {
	body := oaRequest{Model: o.model, MaxTokens: r.MaxTokens, Temperature: r.Temperature}
	if r.System != "" {
		body.Messages = append(body.Messages, Message{Role: "system", Content: r.System})
	}
	body.Messages = append(body.Messages, r.Messages...)
	if r.JSONSchema != nil {
		// JSON mode is the widely supported subset (DeepSeek has no json_schema mode).
		body.ResponseFormat = map[string]any{"type": "json_object"}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	start := time.Now()
	resp, err := o.http.Do(req)
	if err != nil {
		return nil, Classify(0, "", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, Classify(resp.StatusCode, strings.TrimSpace(string(msg)), nil)
	}
	var out oaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, Classify(0, "invalid response JSON", err)
	}
	if len(out.Choices) == 0 {
		return nil, &Error{Kind: Retryable, Message: "response has no choices"}
	}
	c := out.Choices[0]
	if c.FinishReason == "content_filter" {
		return nil, &Error{Kind: Refused, Message: "the provider's content filter stopped the response"}
	}
	finish := "stop"
	if c.FinishReason == "length" {
		finish = "length"
	}
	return &Response{
		Text: c.Message.Content, Model: out.Model, FinishReason: finish, Latency: time.Since(start),
		InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens,
	}, nil
}
