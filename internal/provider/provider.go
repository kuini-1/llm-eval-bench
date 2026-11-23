// Package provider calls models: Claude through the official SDK, any
// OpenAI-compatible API (DeepSeek, vLLM, ...), Ollama's native API, and a
// deterministic mock for tests.
package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/kuini-1/llm-eval-bench/internal/config"
	"github.com/kuini-1/llm-eval-bench/internal/dataset"
)

type Message struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

type Request struct {
	System      string
	Messages    []Message
	MaxTokens   int
	Temperature *float64
	// JSONSchema asks for JSON output in this shape where the provider
	// supports it (structured outputs on Claude, format on Ollama, JSON mode
	// on OpenAI-compatible APIs). Callers still validate what comes back.
	JSONSchema map[string]any
	// Hints for the mock provider, which answers from the item instead of
	// reading the prompt. Real providers ignore them.
	Item   *dataset.Item
	Answer string // judge calls: the answer being graded
	Sample int    // judge calls: 0 for the first verdict, 1+ for repeats
}

type Response struct {
	Text         string
	Model        string // as reported by the provider
	FinishReason string // stop | length
	InputTokens  int64
	OutputTokens int64
	Latency      time.Duration
}

type Provider interface {
	Complete(ctx context.Context, r Request) (*Response, error)
}

type Kind int

const (
	Retryable Kind = iota // rate limits, overload, timeouts, network
	Permanent             // bad request, auth, unknown model
	Refused               // the model declined
)

type Error struct {
	Kind    Kind
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
	}
	return e.Message
}

// Classify maps an HTTP status (0 for transport errors) to a Kind.
func Classify(status int, msg string, err error) *Error {
	if status == 0 {
		var ne net.Error
		if errors.As(err, &ne) || errors.Is(err, context.DeadlineExceeded) {
			return &Error{Kind: Retryable, Message: "timeout or network error: " + err.Error()}
		}
		if err != nil {
			return &Error{Kind: Retryable, Message: err.Error()}
		}
		return &Error{Kind: Retryable, Message: msg}
	}
	switch {
	case status == 408 || status == 409 || status == 429 || status >= 500:
		return &Error{Kind: Retryable, Status: status, Message: msg}
	default:
		return &Error{Kind: Permanent, Status: status, Message: msg}
	}
}

func IsRetryable(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind == Retryable
	}
	// Unknown errors (including context deadline on one call) are worth one more try.
	return !errors.Is(err, context.Canceled)
}

// New builds the client for a configured model.
func New(m config.Model) (Provider, error) {
	switch m.Provider {
	case "anthropic":
		return NewAnthropic(m), nil
	case "openai":
		return NewOpenAI(m), nil
	case "ollama":
		return NewOllama(m), nil
	case "mock":
		return NewMock(m.Model), nil
	}
	return nil, fmt.Errorf("unknown provider %q", m.Provider)
}
