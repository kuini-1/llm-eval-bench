package provider

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/kuini-1/llm-eval-bench/internal/config"
)

// Anthropic calls the Messages API through the official Go SDK.
type Anthropic struct {
	client anthropic.Client
	model  string
	effort string
}

// Models that accept server-side refusal fallbacks.
var fallbackModels = map[string]bool{
	"claude-fable-5-2": true, "claude-fable-5-1": true, "claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true,
}

func NewAnthropic(m config.Model) *Anthropic {
	opts := []option.RequestOption{
		option.WithMaxRetries(0), // the runner retries, with the same policy for every provider
		option.WithRequestTimeout(m.Timeout.Duration),
	}
	if m.APIKey != "" {
		opts = append(opts, option.WithAPIKey(m.APIKey))
	}
	if m.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(m.BaseURL))
	}
	return &Anthropic{client: anthropic.NewClient(opts...), model: m.Model, effort: m.Effort}
}

func (a *Anthropic) Complete(ctx context.Context, r Request) (*Response, error) {
	p := anthropic.BetaMessageNewParams{Model: anthropic.Model(a.model), MaxTokens: int64(r.MaxTokens)}
	if r.System != "" {
		p.System = []anthropic.BetaTextBlockParam{{Text: r.System}}
	}
	for _, m := range r.Messages {
		if m.Role == "assistant" {
			p.Messages = append(p.Messages, anthropic.BetaMessageParam{OfAssistant: &anthropic.BetaAssistantMessageParam{
				Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(m.Content)}}})
		} else {
			p.Messages = append(p.Messages, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(m.Content)))
		}
	}
	// Temperature is not sent: current Claude models reject sampling parameters.
	if a.effort != "" {
		p.OutputConfig.Effort = anthropic.BetaOutputConfigEffort(a.effort)
	}
	if r.JSONSchema != nil {
		p.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{Schema: r.JSONSchema}
	}
	if fallbackModels[a.model] {
		// A declined request is retried on a fallback model inside the same call.
		p.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		p.Betas = append(p.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}

	start := time.Now()
	msg, err := a.client.Beta.Messages.New(ctx, p)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return nil, Classify(apiErr.StatusCode, apiErr.Error(), nil)
		}
		return nil, Classify(0, "", err)
	}
	if msg.StopReason == anthropic.BetaStopReasonRefusal {
		m := "the model declined"
		if c := string(msg.StopDetails.Category); c != "" {
			m += " (" + c + ")"
		}
		return nil, &Error{Kind: Refused, Message: m}
	}
	var sb strings.Builder
	for _, c := range msg.Content {
		if t, ok := c.AsAny().(anthropic.BetaTextBlock); ok {
			sb.WriteString(t.Text)
		}
	}
	finish := "stop"
	if msg.StopReason == anthropic.BetaStopReasonMaxTokens {
		finish = "length"
	}
	u := msg.Usage
	return &Response{
		Text: sb.String(), Model: string(msg.Model), FinishReason: finish, Latency: time.Since(start),
		InputTokens:  u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		OutputTokens: u.OutputTokens,
	}, nil
}
