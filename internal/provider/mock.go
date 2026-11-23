package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/kuini-1/llm-eval-bench/internal/dataset"
	"github.com/kuini-1/llm-eval-bench/internal/grade"
)

// Mock answers from the benchmark item itself, so tests and the CI demo run
// without a model. It is deterministic: whether it gets an item right
// depends on a hash of the model name and item id. It is not a language
// model and its results say nothing about real models.
//
//	mock-strong  right about 90% of the time, declines most false premises
//	mock-weak    right about 55% of the time, often plays along with false premises
//	mock-judge   grades with the code graders, and is wrong on purpose about 8% of the time
type Mock struct{ model string }

func NewMock(model string) *Mock { return &Mock{model: model} }

func unit(parts ...string) float64 {
	h := fnv.New64a()
	h.Write([]byte(strings.Join(parts, "|")))
	return float64(h.Sum64()%10000) / 10000
}

func (m *Mock) Complete(ctx context.Context, r Request) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.Item == nil {
		return nil, &Error{Kind: Permanent, Message: "mock: request has no item"}
	}
	var text string
	var ms float64
	switch m.model {
	case "mock-strong":
		text, ms = answer(*r.Item, unit(m.model, r.Item.ID) < 0.9, unit(m.model, r.Item.ID, "probe") < 0.85), 400+500*unit(m.model, r.Item.ID, "t")
	case "mock-weak":
		text, ms = answer(*r.Item, unit(m.model, r.Item.ID) < 0.55, unit(m.model, r.Item.ID, "probe") < 0.4), 150+250*unit(m.model, r.Item.ID, "t")
	case "mock-judge":
		text, ms = judge(*r.Item, r.Answer, r.Sample), 200+200*unit(m.model, r.Item.ID, r.Answer)
	default:
		return nil, &Error{Kind: Permanent, Message: fmt.Sprintf("mock: unknown model %q (mock-strong, mock-weak or mock-judge)", m.model)}
	}
	prompt := r.System
	for _, msg := range r.Messages {
		prompt += msg.Content
	}
	return &Response{
		Text: text, Model: m.model, FinishReason: "stop",
		// Simulated, not measured: there is nothing to time.
		Latency:     time.Duration(ms * float64(time.Millisecond)),
		InputTokens: int64(len(prompt)/4 + 1), OutputTokens: int64(len(text)/4 + 1),
	}, nil
}

func answer(it dataset.Item, right, declines bool) string {
	g := it.Grader
	if g.Type == "abstain" {
		if declines {
			return "I can't find any record of that, and the premise looks wrong. " + it.Reference
		}
		return "Yes. It was first described in 1987 by Dr. Mara Velde in Geneva, and it is still widely used today."
	}
	if right {
		switch g.Type {
		case "json":
			b, _ := json.Marshal(g.Expected)
			return string(b)
		case "contains":
			return it.Reference + " " + strings.Join(g.MustInclude, " ")
		case "number", "exact", "choice":
			return "Answer: " + it.Reference
		default:
			return it.Reference
		}
	}
	switch g.Type {
	case "number":
		return "Answer: 1" + it.Reference
	case "choice":
		if strings.EqualFold(it.Reference, "A") {
			return "Answer: B"
		}
		return "Answer: A"
	case "json":
		return `{"note": "I could not extract the fields."}`
	default:
		return "I think the answer is probably something else, but I'm not sure."
	}
}

func judge(it dataset.Item, ans string, sample int) string {
	o, ok := grade.Check(it, ans)
	pass := o.Pass
	if !ok { // judge-only items: pass if the answer repeats most of the reference
		pass = overlap(it.Reference, ans) >= 0.5
	}
	// Simulated judge mistakes, plus a few more on repeat samples, so the
	// agreement and consistency numbers have something to measure.
	if unit("judge", it.ID, ans) < 0.08 || (sample > 0 && unit("judge", it.ID, ans, fmt.Sprint(sample)) < 0.05) {
		pass = !pass
	}
	halluc := it.Grader.Type == "abstain" && !pass
	score := 2
	if pass {
		score = 4
	}
	b, _ := json.Marshal(grade.Verdict{Reasoning: "mock judge: compared the answer with the reference.", Pass: pass, Score: score, Hallucination: halluc})
	return string(b)
}

func overlap(ref, ans string) float64 {
	words := strings.Fields(strings.ToLower(ref))
	if len(words) == 0 {
		return 0
	}
	low := strings.ToLower(ans)
	hit := 0
	for _, w := range words {
		if strings.Contains(low, w) {
			hit++
		}
	}
	return float64(hit) / float64(len(words))
}
