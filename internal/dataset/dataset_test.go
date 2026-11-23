package dataset

import (
	"os"
	"strings"
	"testing"
)

func TestParseJSONL(t *testing.T) {
	in := `{"id":"a","category":"math","prompt":"2+2?","reference":"4","grader":"number"}
# comments and blank lines are skipped

{"id":"b","category":"fmt","prompt":"Say YES","reference":"YES","grader":{"type":"regex","pattern":"^YES$"}}`
	items, errs := Parse(strings.NewReader(in), "jsonl")
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(items) != 2 || items[0].Grader.Type != "number" || items[1].Grader.Pattern != "^YES$" {
		t.Fatalf("items: %+v", items)
	}
}

func TestParseReportsEveryProblem(t *testing.T) {
	in := `{"id":"a","category":"math","prompt":"2+2?","reference":"four","grader":"number"}
{"id":"b","category":"x","prompt":"p","grader":"fuzzy"}
{"id":"c","category":"x","prompt":"p","grader":"regex","pattern":"("}
{"id":"a","category":"x","prompt":"p","reference":"r","grader":"exact"}
{"id":"d","category":"x","prompt":"p","grader":"judge"}
{"id":"e","category":"x","prompt":"p","reference":"r","grader":"exact","colour":"red"}
not json`
	_, errs := Parse(strings.NewReader(in), "jsonl")
	got := make([]string, len(errs))
	for i, e := range errs {
		got[i] = e.Error()
	}
	want := []string{
		`line 1 (a): grader number needs a numeric reference, got "four"`,
		`line 2 (b): unknown grader "fuzzy"`,
		`line 3`, // the regex is a top-level field here, which the strict decoder rejects
		`line 5 (d): grader judge needs a reference or a rubric`,
		`line 6`, // unknown field
		`line 7`,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d errors, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("error %d = %q, want prefix %q", i, got[i], want[i])
		}
	}
}

func TestParseRejectsDuplicateIDs(t *testing.T) {
	in := `{"id":"a","category":"x","prompt":"p","reference":"r","grader":"exact"}
{"id":"a","category":"x","prompt":"q","reference":"r","grader":"exact"}`
	_, errs := Parse(strings.NewReader(in), "jsonl")
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `id "a" is used by items 1 and 2`) {
		t.Fatalf("errs = %v", errs)
	}
}

func TestParseCSV(t *testing.T) {
	in := "id,category,prompt,reference,grader,aliases,hallucination_probe,expected,tolerance\n" +
		"k1,knowledge,Capital of Australia?,Canberra,exact,canberra act|the capital territory,,,\n" +
		`e1,extract,"Name and age, as JSON",,json,,,"{""age"": 34}",` + "\n" +
		"p1,probe,Who was the 60th US president?,No one yet,abstain,,true,,\n" +
		"n1,math,6*7?,42,number,,,,0.5\n"
	items, errs := Parse(strings.NewReader(in), "csv")
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(items[0].Grader.Aliases) != 2 || items[1].Grader.Expected["age"] != 34.0 || !items[2].Probe || items[3].Grader.Tolerance != 0.5 {
		t.Fatalf("items: %+v", items)
	}
}

func TestUserMessageWrapsContext(t *testing.T) {
	it := Item{Prompt: "Who owns it?", Context: "Dana owns the migration."}
	m := it.UserMessage()
	if !strings.Contains(m, "<context>\nDana owns the migration.\n</context>") || !strings.HasSuffix(m, "Who owns it?") {
		t.Fatalf("message: %q", m)
	}
	if (Item{Prompt: "plain"}).UserMessage() != "plain" {
		t.Fatal("an item without context should be sent as is")
	}
}

func TestBundledDatasetIsValid(t *testing.T) {
	f, err := os.Open("../../datasets/evalbench-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	items, errs := Parse(f, "jsonl")
	if len(errs) != 0 {
		t.Fatalf("bundled dataset has problems: %v", errs)
	}
	if len(items) != 72 {
		t.Fatalf("got %d items, want 72", len(items))
	}
	probes := 0
	for _, it := range items {
		if it.Probe {
			probes++
		}
	}
	if probes != 13 {
		t.Fatalf("got %d hallucination probes, want 13", probes)
	}
}
