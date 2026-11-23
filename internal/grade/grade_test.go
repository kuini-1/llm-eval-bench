package grade

import (
	"os"
	"strings"
	"testing"

	"github.com/kuini-1/llm-eval-bench/internal/dataset"
)

func item(grader dataset.Grader, ref string) dataset.Item {
	return dataset.Item{ID: "t", Category: "c", Prompt: "p", Reference: ref, Grader: grader}
}

func TestCodeGraders(t *testing.T) {
	cases := []struct {
		name   string
		it     dataset.Item
		output string
		pass   bool
	}{
		{"number on the answer line", item(dataset.Grader{Type: "number"}, "99.36"), "Tip is $12.96.\nTotal: $99.36\n\nAnswer: $99.36", true},
		{"number: last number wins without an answer line", item(dataset.Grader{Type: "number"}, "215"), "From 9:40 to 13:15 is 215", true},
		{"number with thousands separator", item(dataset.Grader{Type: "number"}, "69104"), "**Answer:** 69,104", true},
		{"number within tolerance", item(dataset.Grader{Type: "number", Tolerance: 0.005}, "52.5"), "Answer: 52.50", true},
		{"wrong number", item(dataset.Grader{Type: "number"}, "129"), "Answer: 128", false},
		{"working shown, wrong final answer", item(dataset.Grader{Type: "number"}, "5"), "Many say 10 cents.\nAnswer: 10", false},
		{"number missing", item(dataset.Grader{Type: "number"}, "4"), "I can't tell.", false},

		{"exact, bare", item(dataset.Grader{Type: "exact"}, "Canberra"), "Canberra", true},
		{"exact, short sentence", item(dataset.Grader{Type: "exact"}, "Canberra"), "It's Canberra.", true},
		{"exact alias", item(dataset.Grader{Type: "exact", Aliases: []string{"Austen"}}, "Jane Austen"), "Austen", true},
		{"exact, long answer doesn't count by containment", item(dataset.Grader{Type: "exact"}, "Sydney"),
			"Many people think it is Sydney, but the capital is actually Canberra, which was chosen as a compromise.", false},
		{"exact, hedged wrong answer", item(dataset.Grader{Type: "exact"}, "Canberra"), "Sydney is the capital, I believe, though some say Canberra.", false},
		{"exact, full short sentence", item(dataset.Grader{Type: "exact"}, "Canberra"), "The capital of Australia is Canberra.", true},
		{"exact, wrong", item(dataset.Grader{Type: "exact"}, "Raj"), "Lee", false},

		{"choice on the answer line", item(dataset.Grader{Type: "choice"}, "B"), "All bloops are lazzies.\nAnswer: B", true},
		{"choice in parentheses", item(dataset.Grader{Type: "choice"}, "B"), "The correct option is (B).", true},
		{"choice: 'the answer is'", item(dataset.Grader{Type: "choice"}, "B"), "So the answer is B because n(n+1)...", true},
		{"choice, wrong", item(dataset.Grader{Type: "choice"}, "B"), "Answer: D", false},
		{"choice missing", item(dataset.Grader{Type: "choice"}, "B"), "It depends.", false},

		{"contains", item(dataset.Grader{Type: "contains", MustInclude: []string{"blue", "whale"}, MustNotInclude: []string{"water"}}, ""), "A Blue Whale sings.", true},
		{"contains a forbidden word", item(dataset.Grader{Type: "contains", MustInclude: []string{"blue", "whale"}, MustNotInclude: []string{"water"}}, ""), "A blue whale in deep water.", false},
		{"contains, missing word", item(dataset.Grader{Type: "contains", MustInclude: []string{"android", "ios"}}, ""), "Only on Android.", false},

		{"regex", item(dataset.Grader{Type: "regex", Pattern: `^\s*YES\.?\s*$`}, ""), " YES\n", true},
		{"regex fails", item(dataset.Grader{Type: "regex", Pattern: `^\s*YES\.?\s*$`}, ""), "Yes, of course.", false},

		{"json in a code fence", item(dataset.Grader{Type: "json", Expected: map[string]any{"name": "Marta Silva", "age": 34.0}}, ""),
			"```json\n{\"name\": \"marta silva\", \"age\": 34, \"city\": \"Porto\"}\n```", true},
		{"json list in another order", item(dataset.Grader{Type: "json", Expected: map[string]any{"languages": []any{"Go", "Python", "TypeScript"}}}, ""),
			`{"languages": ["TypeScript", "Go", "Python"]}`, true},
		{"json number as a string", item(dataset.Grader{Type: "json", Expected: map[string]any{"age": 34.0}}, ""), `{"age": "34"}`, false},
		{"json boolean", item(dataset.Grader{Type: "json", Expected: map[string]any{"transferable": false}}, ""), `{"transferable": true}`, false},
		{"not json", item(dataset.Grader{Type: "json", Expected: map[string]any{"a": 1.0}}, ""), "a is 1", false},

		{"abstain heuristic: declines", item(dataset.Grader{Type: "abstain"}, "x"), "I couldn't find any record of that novel.", true},
		{"abstain heuristic: corrects premise", item(dataset.Grader{Type: "abstain"}, "x"), "The Eiffel Tower did not move; there is no record of that.", true},
		{"abstain heuristic: plays along", item(dataset.Grader{Type: "abstain"}, "x"), "It moved because of a flood in 1998.", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, ok := Check(c.it, c.output)
			if !ok {
				t.Fatal("expected a code grader")
			}
			if o.Pass != c.pass {
				t.Fatalf("pass = %v, want %v (%s)", o.Pass, c.pass, o.Detail)
			}
		})
	}
}

func TestJudgeItemsHaveNoCodeGrader(t *testing.T) {
	if _, ok := Check(item(dataset.Grader{Type: "judge"}, "r"), "anything"); ok {
		t.Fatal("judge items should have no code grader")
	}
}

// Every reference answer in the bundled dataset must pass its own code
// grader. This catches a wrong reference or a too-strict pattern before a
// model is blamed for it.
func TestBundledReferencesPassTheirGraders(t *testing.T) {
	f, err := os.Open("../../datasets/evalbench-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	items, errs := dataset.Parse(f, "jsonl")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	checked := 0
	for _, it := range items {
		if !it.Grader.Objective() {
			continue
		}
		o, _ := Check(it, it.Reference)
		if !o.Pass {
			t.Errorf("%s: reference fails its own grader: %s", it.ID, o.Detail)
		}
		checked++
	}
	if checked < 40 {
		t.Fatalf("only %d objective items checked", checked)
	}
}

func TestParseVerdict(t *testing.T) {
	v, err := ParseVerdict("```json\n{\"reasoning\": \"Correct.\", \"pass\": true, \"score\": 5, \"hallucination\": false}\n```")
	if err != nil || !v.Pass || v.Score != 5 || v.Reasoning != "Correct." {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	for _, bad := range []string{
		`{"reasoning": "x", "pass": true, "hallucination": false}`,              // no score
		`{"reasoning": "x", "pass": "yes", "score": 3, "hallucination": false}`, // wrong type
		`{"reasoning": "x", "pass": true, "score": 9, "hallucination": false}`,  // out of range
		`I think it passes.`,
	} {
		if _, err := ParseVerdict(bad); err == nil {
			t.Errorf("expected an error for %s", bad)
		}
	}
}

func TestJudgePrompt(t *testing.T) {
	it := dataset.Item{Prompt: "What did finance decide?", Context: "Ken will check with finance.", Reference: "The notes don't say.",
		Grader: dataset.Grader{Type: "abstain"}, Rubric: "Extra note."}
	p := JudgePrompt(it, "")
	for _, want := range []string{"<context>\nKen will check with finance.\n</context>", "<reference_answer>\nThe notes don't say.", "false premise", "Extra note.", "<answer>\n(empty)\n</answer>"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	long := JudgePrompt(dataset.Item{Prompt: "q", Reference: "r", Grader: dataset.Grader{Type: "judge"}}, strings.Repeat("x", 9000))
	if !strings.Contains(long, "[answer truncated for grading]") {
		t.Error("long answers should be truncated")
	}
}
