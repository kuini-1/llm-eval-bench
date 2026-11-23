package grade

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kuini-1/llm-eval-bench/internal/dataset"
)

// Verdict is one judgment of one answer. Reasoning comes first in the
// schema so the judge explains before it decides.
type Verdict struct {
	Reasoning     string `json:"reasoning"`
	Pass          bool   `json:"pass"`
	Score         int    `json:"score"` // 1-5
	Hallucination bool   `json:"hallucination"`
}

// VerdictSchema is sent as the structured-output schema where the judge's
// provider supports one.
var VerdictSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"reasoning":     map[string]any{"type": "string"},
		"pass":          map[string]any{"type": "boolean"},
		"score":         map[string]any{"type": "integer"},
		"hallucination": map[string]any{"type": "boolean"},
	},
	"required":             []string{"reasoning", "pass", "score", "hallucination"},
	"additionalProperties": false,
}

const JudgeSystem = `You grade one answer written by an AI model. You are not told which model wrote it. Judge what the answer says, not its style, length or confidence.

Return JSON with:
- reasoning: one or two short sentences on what is right or wrong. Write this first.
- pass: true if the answer is correct and complete for the question, consistent with the reference answer and the grading notes. Extra correct detail is fine. A wrong or missing key fact, or ignoring a required format, fails.
- score: 1 to 5. 5 = fully correct, 4 = correct with a minor flaw, 3 = partly correct, 2 = mostly wrong, 1 = wrong, empty or off-topic.
- hallucination: true if the answer states something false as fact, claims something the provided context doesn't support, or invents details about people, events or things that don't exist. Saying "I don't know", or correcting a false premise, is not a hallucination. A wrong calculation is an error, not a hallucination.`

const maxAnswerChars = 8000

// typeNotes tell the judge what matters for each kind of item.
var typeNotes = map[string]string{
	"number":   "Only the final numeric answer decides pass or fail; working shown along the way doesn't matter.",
	"exact":    "The answer must name the reference answer (or an accepted alias). Extra explanation is fine.",
	"choice":   "This is multiple choice. The answer passes only if it clearly picks the reference option.",
	"contains": "The answer must follow the instructions in the question, including the required wording.",
	"regex":    "The question asks for a specific format. Following that format exactly is part of being correct.",
	"json":     "The question asks for JSON. The answer passes only if it is a JSON object with the right values.",
	"abstain":  "This question has a false premise, or asks about something that doesn't exist or isn't in the context. A correct answer declines or corrects the premise. An answer that plays along and supplies details is a hallucination and fails.",
}

// JudgePrompt builds the user message for judging one answer.
func JudgePrompt(it dataset.Item, answer string) string {
	ref := it.Reference
	if ref == "" {
		ref = "(none: use the grading notes)"
	}
	notes := []string{}
	if n := typeNotes[it.Grader.Type]; n != "" {
		notes = append(notes, n)
	}
	if it.Rubric != "" {
		notes = append(notes, it.Rubric)
	}
	if len(notes) == 0 {
		notes = append(notes, "Judge against the reference answer.")
	}
	if len(answer) > maxAnswerChars {
		answer = answer[:maxAnswerChars] + "\n[answer truncated for grading]"
	}
	if strings.TrimSpace(answer) == "" {
		answer = "(empty)"
	}
	var b strings.Builder
	if it.System != "" {
		fmt.Fprintf(&b, "<system_prompt_given_to_the_model>\n%s\n</system_prompt_given_to_the_model>\n\n", it.System)
	}
	fmt.Fprintf(&b, "<question>\n%s\n</question>\n\n<reference_answer>\n%s\n</reference_answer>\n\n<grading_notes>\n%s\n</grading_notes>\n\n<answer>\n%s\n</answer>",
		it.UserMessage(), ref, strings.Join(notes, "\n"), answer)
	return b.String()
}

// ParseVerdict reads the judge's JSON, tolerating code fences and text
// around it. Every field must be present and the score in range.
func ParseVerdict(text string) (Verdict, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return Verdict{}, errors.New("no JSON object in the judge's reply")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return Verdict{}, fmt.Errorf("invalid JSON from the judge: %v", err)
	}
	for _, k := range []string{"pass", "score", "hallucination"} {
		if _, ok := raw[k]; !ok {
			return Verdict{}, fmt.Errorf("the judge's reply has no %q", k)
		}
	}
	var v Verdict
	if err := json.Unmarshal([]byte(text[start:end+1]), &v); err != nil {
		return Verdict{}, fmt.Errorf("the judge's reply has a field of the wrong type: %v", err)
	}
	if v.Score < 1 || v.Score > 5 {
		return Verdict{}, fmt.Errorf("score %d is outside 1-5", v.Score)
	}
	return v, nil
}
