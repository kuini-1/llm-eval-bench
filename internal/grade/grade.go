// Package grade checks answers in code where an item has an objective
// answer, and builds and parses the LLM judge's prompts and verdicts.
package grade

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/kuini-1/llm-eval-bench/internal/dataset"
)

// Outcome of a code grader. Detail says what was compared, for reviewers.
type Outcome struct {
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

// Check runs the item's code grader. ok is false for "judge" items, which
// have no code check. For "abstain" items the check is a phrase heuristic,
// used only to flag disagreements with the judge (see Heuristic).
func Check(it dataset.Item, output string) (o Outcome, ok bool) {
	g := it.Grader
	switch g.Type {
	case "exact":
		return exact(it, output), true
	case "number":
		return number(it, output), true
	case "choice":
		return choice(it, output), true
	case "contains":
		return contains(g, output), true
	case "regex":
		re := regexp.MustCompile(g.Pattern) // validated at import
		if re.MatchString(strings.TrimSpace(output)) {
			return Outcome{true, "matches " + g.Pattern}, true
		}
		return Outcome{false, "does not match " + g.Pattern}, true
	case "json":
		return jsonFields(g, output), true
	case "abstain":
		return abstain(output), true
	}
	return Outcome{}, false
}

// Heuristic reports whether the item's code check is only a heuristic.
func Heuristic(it dataset.Item) bool { return it.Grader.Type == "abstain" }

var answerLine = regexp.MustCompile(`(?im)^[ \t>*_#-]*(?:final\s+)?answer\s*[*_]*\s*[:：]\s*[*_]*\s*(.+?)\s*[*_]*\s*$`)

// answerPart is the text after the last "Answer:" line, or the whole output.
func answerPart(output string) string {
	m := answerLine.FindAllStringSubmatch(output, -1)
	if len(m) > 0 {
		return m[len(m)-1][1]
	}
	return strings.TrimSpace(output)
}

func normalize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		case !space:
			b.WriteRune(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

const exactContainWords = 8

func exact(it dataset.Item, output string) Outcome {
	got := normalize(answerPart(output))
	for _, want := range append([]string{it.Reference}, it.Grader.Aliases...) {
		w := normalize(want)
		if got == w {
			return Outcome{true, fmt.Sprintf("answer %q equals %q", got, w)}
		}
		// A short answer that contains the expected words, e.g. "It's Canberra."
		// for "Canberra". The limit is low on purpose: a longer answer can name
		// the right word while committing to a wrong one ("Sydney is the
		// capital, though some say Canberra"). The judge cross-check catches
		// the cases this rule gets wrong.
		if len(strings.Fields(got)) <= exactContainWords && strings.Contains(" "+got+" ", " "+w+" ") {
			return Outcome{true, fmt.Sprintf("short answer %q contains %q", got, w)}
		}
	}
	return Outcome{false, fmt.Sprintf("answer %q, expected %q", truncate(got, 80), it.Reference)}
}

var numberRe = regexp.MustCompile(`-?\$?\d[\d,]*(?:\.\d+)?%?`)

func lastNumber(s string) (float64, string, bool) {
	all := numberRe.FindAllString(s, -1)
	if len(all) == 0 {
		return 0, "", false
	}
	raw := all[len(all)-1]
	v, err := strconv.ParseFloat(strings.NewReplacer("$", "", ",", "", "%", "").Replace(raw), 64)
	return v, raw, err == nil
}

func number(it dataset.Item, output string) Outcome {
	want, _ := strconv.ParseFloat(it.Reference, 64)
	got, raw, ok := lastNumber(answerPart(output))
	if !ok {
		return Outcome{false, "no number in the answer"}
	}
	tol := math.Max(it.Grader.Tolerance, 1e-9*math.Max(1, math.Abs(want)))
	if math.Abs(got-want) <= tol {
		return Outcome{true, fmt.Sprintf("%s equals %s", raw, it.Reference)}
	}
	return Outcome{false, fmt.Sprintf("%s, expected %s", raw, it.Reference)}
}

var (
	letterRe     = regexp.MustCompile(`\b([A-E])\b`)
	answerIsRe   = regexp.MustCompile(`(?i)answer is:?\s*\(?([A-E])\b`)
	parenLetterR = regexp.MustCompile(`\(([A-E])\)`)
)

func choice(it dataset.Item, output string) Outcome {
	want := strings.ToUpper(strings.TrimSpace(it.Reference))
	var got string
	if m := answerLine.FindAllStringSubmatch(output, -1); len(m) > 0 {
		if l := letterRe.FindStringSubmatch(strings.ToUpper(m[len(m)-1][1])); l != nil {
			got = l[1]
		}
	}
	if got == "" {
		if m := answerIsRe.FindAllStringSubmatch(output, -1); len(m) > 0 {
			got = strings.ToUpper(m[len(m)-1][1])
		} else if m := parenLetterR.FindAllStringSubmatch(output, -1); len(m) > 0 {
			got = m[len(m)-1][1]
		} else if t := strings.TrimSpace(output); len(t) == 1 {
			got = strings.ToUpper(t)
		}
	}
	if got == "" {
		return Outcome{false, "no option letter found"}
	}
	return Outcome{got == want, fmt.Sprintf("chose %s, expected %s", got, want)}
}

func contains(g dataset.Grader, output string) Outcome {
	low := strings.ToLower(output)
	var missing, forbidden []string
	for _, s := range g.MustInclude {
		if !strings.Contains(low, strings.ToLower(s)) {
			missing = append(missing, s)
		}
	}
	for _, s := range g.MustNotInclude {
		if strings.Contains(low, strings.ToLower(s)) {
			forbidden = append(forbidden, s)
		}
	}
	if len(missing) == 0 && len(forbidden) == 0 {
		return Outcome{true, "has every required phrase and none of the forbidden ones"}
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing "+strings.Join(quoteAll(missing), ", "))
	}
	if len(forbidden) > 0 {
		parts = append(parts, "contains "+strings.Join(quoteAll(forbidden), ", "))
	}
	return Outcome{false, strings.Join(parts, "; ")}
}

// ExtractJSON finds the JSON object in an answer, with or without code fences.
func ExtractJSON(s string) (map[string]any, error) {
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object found")
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(s[start:end+1]), &v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	return v, nil
}

func jsonFields(g dataset.Grader, output string) Outcome {
	got, err := ExtractJSON(output)
	if err != nil {
		return Outcome{false, err.Error()}
	}
	var wrong []string
	for k, want := range g.Expected {
		v, ok := got[k]
		if !ok {
			wrong = append(wrong, k+" missing")
		} else if !sameValue(want, v) {
			wrong = append(wrong, fmt.Sprintf("%s is %s, expected %s", k, compact(v), compact(want)))
		}
	}
	if len(wrong) == 0 {
		return Outcome{true, fmt.Sprintf("all %d expected fields match", len(g.Expected))}
	}
	return Outcome{false, strings.Join(wrong, "; ")}
}

// sameValue compares JSON values loosely: numbers by value, strings after
// normalizing case and punctuation, lists of scalars in any order.
func sameValue(want, got any) bool {
	switch w := want.(type) {
	case float64:
		g, ok := got.(float64)
		return ok && math.Abs(w-g) < 1e-9
	case string:
		g, ok := got.(string)
		return ok && normalize(w) == normalize(g)
	case bool:
		g, ok := got.(bool)
		return ok && w == g
	case nil:
		return got == nil
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		used := make([]bool, len(g))
	outer:
		for _, wv := range w {
			for i, gv := range g {
				if !used[i] && sameValue(wv, gv) {
					used[i] = true
					continue outer
				}
			}
			return false
		}
		return true
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for k, wv := range w {
			if !sameValue(wv, g[k]) {
				return false
			}
		}
		return true
	}
	return false
}

// Phrases that signal declining, or correcting a false premise. This is
// deliberately simple: it is a cross-check on the judge, not a grader.
var declineMarkers = []string{
	"i don't know", "i do not know", "i'm not aware", "i am not aware", "not aware of", "no record", "no information",
	"no evidence", "doesn't exist", "does not exist", "did not happen", "didn't happen", "never happened", "there is no",
	"there was no", "there are no", "isn't a real", "is not a real", "fictional", "not a real", "couldn't find", "could not find",
	"can't find", "cannot find", "unable to find", "no such", "premise", "not mentioned", "does not mention", "doesn't mention",
	"does not say", "doesn't say", "does not contain", "doesn't contain", "does not provide", "doesn't provide", "not provided",
	"not stated", "not specified", "cannot determine", "can't determine", "cannot be determined", "not possible to determine",
	"not enough information", "isn't enough information", "unclear from", "no known", "not known",
}

func abstain(output string) Outcome {
	low := strings.ToLower(strings.ReplaceAll(output, "’", "'"))
	for _, m := range declineMarkers {
		if strings.Contains(low, m) {
			return Outcome{true, fmt.Sprintf("heuristic: declines or corrects (found %q)", m)}
		}
	}
	return Outcome{false, "heuristic: no sign of declining or correcting the premise"}
}

func quoteAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = strconv.Quote(x)
	}
	return out
}

func compact(v any) string {
	b, _ := json.Marshal(v)
	return truncate(string(b), 60)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
