// Package dataset defines benchmark items and reads them from JSONL or CSV.
package dataset

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Grader says how an answer is checked in code. "judge" means there is no
// objective check and only the LLM judge decides.
type Grader struct {
	Type           string         `json:"type"`
	Aliases        []string       `json:"aliases,omitempty"`          // exact: other accepted answers
	Tolerance      float64        `json:"tolerance,omitempty"`        // number: allowed absolute difference
	MustInclude    []string       `json:"must_include,omitempty"`     // contains
	MustNotInclude []string       `json:"must_not_include,omitempty"` // contains
	Pattern        string         `json:"pattern,omitempty"`          // regex
	Expected       map[string]any `json:"expected,omitempty"`         // json: fields the output must contain
}

// UnmarshalJSON accepts "grader": "judge" as shorthand for {"type": "judge"}.
func (g *Grader) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*g = Grader{Type: s}
		return nil
	}
	type plain Grader
	return json.Unmarshal(b, (*plain)(g))
}

var graderTypes = map[string]bool{"exact": true, "number": true, "choice": true, "contains": true, "regex": true, "json": true, "abstain": true, "judge": true}

// Objective graders have a right answer that code can check. The judge's
// verdict on these items is compared with the grader's to measure the judge.
func (g Grader) Objective() bool {
	switch g.Type {
	case "exact", "number", "choice", "contains", "regex", "json":
		return true
	}
	return false
}

type Item struct {
	ID        string   `json:"id"`
	Category  string   `json:"category"`
	Prompt    string   `json:"prompt"`
	System    string   `json:"system,omitempty"`
	Context   string   `json:"context,omitempty"`   // a passage the answer must be grounded in
	Reference string   `json:"reference,omitempty"` // the correct answer, or what a correct answer must say
	Grader    Grader   `json:"grader"`
	Rubric    string   `json:"rubric,omitempty"`              // extra grading notes for the judge
	Probe     bool     `json:"hallucination_probe,omitempty"` // counts toward the hallucination rate
	Tags      []string `json:"tags,omitempty"`
}

// UserMessage is the prompt as the model sees it.
func (it Item) UserMessage() string {
	if it.Context == "" {
		return it.Prompt
	}
	return "Answer using only the context below. If the context doesn't contain the answer, say so.\n\n<context>\n" + it.Context + "\n</context>\n\n" + it.Prompt
}

func (it Item) Validate() error {
	if it.ID == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(it.Prompt) == "" {
		return errors.New("prompt is required")
	}
	if it.Category == "" {
		return errors.New("category is required")
	}
	g := it.Grader
	if !graderTypes[g.Type] {
		return fmt.Errorf("unknown grader %q", g.Type)
	}
	switch g.Type {
	case "exact", "choice":
		if it.Reference == "" {
			return fmt.Errorf("grader %s needs a reference", g.Type)
		}
	case "number":
		if _, err := strconv.ParseFloat(it.Reference, 64); err != nil {
			return fmt.Errorf("grader number needs a numeric reference, got %q", it.Reference)
		}
	case "contains":
		if len(g.MustInclude) == 0 && len(g.MustNotInclude) == 0 {
			return errors.New("grader contains needs must_include or must_not_include")
		}
	case "regex":
		if _, err := regexp.Compile(g.Pattern); err != nil {
			return fmt.Errorf("grader regex: %w", err)
		}
	case "json":
		if len(g.Expected) == 0 {
			return errors.New("grader json needs expected fields")
		}
	case "abstain":
		if it.Reference == "" {
			return errors.New("grader abstain needs a reference saying what is true")
		}
	case "judge":
		if it.Reference == "" && it.Rubric == "" {
			return errors.New("grader judge needs a reference or a rubric")
		}
	}
	return nil
}

// ParseError points at the line or row that failed.
type ParseError struct {
	Line int
	ID   string
	Err  error
}

func (e ParseError) Error() string {
	if e.ID != "" {
		return fmt.Sprintf("line %d (%s): %v", e.Line, e.ID, e.Err)
	}
	return fmt.Sprintf("line %d: %v", e.Line, e.Err)
}

// Parse reads items in "jsonl" or "csv" format and validates every one.
// It returns all problems at once rather than stopping at the first.
func Parse(r io.Reader, format string) ([]Item, []error) {
	var items []Item
	var errs []error
	switch format {
	case "jsonl", "json":
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		line := 0
		for sc.Scan() {
			line++
			b := bytes.TrimSpace(sc.Bytes())
			if len(b) == 0 || b[0] == '#' {
				continue
			}
			var it Item
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&it); err != nil {
				errs = append(errs, ParseError{Line: line, Err: err})
				continue
			}
			if err := it.Validate(); err != nil {
				errs = append(errs, ParseError{Line: line, ID: it.ID, Err: err})
				continue
			}
			items = append(items, it)
		}
		if err := sc.Err(); err != nil {
			errs = append(errs, err)
		}
	case "csv":
		items, errs = parseCSV(r)
	default:
		return nil, []error{fmt.Errorf("unknown format %q (jsonl or csv)", format)}
	}
	seen := map[string]int{}
	for i, it := range items {
		if j, ok := seen[it.ID]; ok {
			errs = append(errs, fmt.Errorf("id %q is used by items %d and %d", it.ID, j+1, i+1))
		}
		seen[it.ID] = i
	}
	if len(items) == 0 && len(errs) == 0 {
		errs = append(errs, errors.New("the file has no items"))
	}
	return items, errs
}

// CSV columns: id, category, prompt and grader are required. Optional:
// system, context, reference, rubric, hallucination_probe (true/false),
// tags, aliases, must_include, must_not_include (lists separated by "|"),
// tolerance, pattern, expected (a JSON object).
func parseCSV(r io.Reader) ([]Item, []error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, []error{err}
	}
	if len(rows) < 2 {
		return nil, []error{errors.New("the CSV needs a header row and at least one item")}
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, req := range []string{"id", "category", "prompt", "grader"} {
		if _, ok := col[req]; !ok {
			return nil, []error{fmt.Errorf("the CSV header has no %q column", req)}
		}
	}
	var items []Item
	var errs []error
	for n, row := range rows[1:] {
		get := func(k string) string {
			if i, ok := col[k]; ok && i < len(row) {
				return strings.TrimSpace(row[i])
			}
			return ""
		}
		list := func(k string) []string {
			var out []string
			for _, s := range strings.Split(get(k), "|") {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
			return out
		}
		it := Item{
			ID: get("id"), Category: get("category"), Prompt: get("prompt"), System: get("system"), Context: get("context"),
			Reference: get("reference"), Rubric: get("rubric"), Probe: strings.EqualFold(get("hallucination_probe"), "true"), Tags: list("tags"),
			Grader: Grader{Type: get("grader"), Aliases: list("aliases"), MustInclude: list("must_include"), MustNotInclude: list("must_not_include"), Pattern: get("pattern")},
		}
		line := n + 2
		if t := get("tolerance"); t != "" {
			v, err := strconv.ParseFloat(t, 64)
			if err != nil {
				errs = append(errs, ParseError{Line: line, ID: it.ID, Err: fmt.Errorf("tolerance: %w", err)})
				continue
			}
			it.Grader.Tolerance = v
		}
		if e := get("expected"); e != "" {
			if err := json.Unmarshal([]byte(e), &it.Grader.Expected); err != nil {
				errs = append(errs, ParseError{Line: line, ID: it.ID, Err: fmt.Errorf("expected: %w", err)})
				continue
			}
		}
		if err := it.Validate(); err != nil {
			errs = append(errs, ParseError{Line: line, ID: it.ID, Err: err})
			continue
		}
		items = append(items, it)
	}
	return items, errs
}

// FormatFor guesses the format from a file name.
func FormatFor(filename string) string {
	if strings.HasSuffix(strings.ToLower(filename), ".csv") {
		return "csv"
	}
	return "jsonl"
}
