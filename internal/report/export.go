package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kuini-1/llm-eval-bench/internal/store"
)

func pct(v float64) string { return fmt.Sprintf("%.0f%%", 100*v) }

func optPct(v *float64) string {
	if v == nil {
		return "-"
	}
	return pct(*v)
}

func usd(v *float64) string {
	if v == nil {
		return "n/a"
	}
	if *v < 0.01 && *v > 0 {
		return fmt.Sprintf("$%.5f", *v)
	}
	return fmt.Sprintf("$%.4f", *v)
}

func optF(v *float64, format string) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf(format, *v)
}

func kappa(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.2f", *v)
}

func mdCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

// Markdown renders the comparison as Markdown tables.
func (r *Report) Markdown() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	run := r.Run
	w("# %s\n\n", mdCell(run.Name))
	w("Dataset **%s** (%d items) · run `%s` · status %s", r.Dataset.Name, r.Dataset.ItemCount, run.ID, run.Status)
	if run.StartedAt != nil && run.FinishedAt != nil {
		d := run.FinishedAt.Sub(*run.StartedAt)
		if d < time.Minute {
			d = d.Round(100 * time.Millisecond)
		} else {
			d = d.Round(time.Second)
		}
		w(" · took %s", d)
	}
	w("\n\nJudge: **%s**", run.Judge)
	if run.JudgeSamples > 1 {
		w(", %d verdicts per answer (majority vote)", run.JudgeSamples)
	}
	w(". Items with an objective answer are scored by code; open-ended answers and hallucination probes by the judge.\n\n")

	w("## Results\n\n")
	w("| Model | Accuracy (95%% CI) | Judge score | Hallucination rate | p50 latency | p95 latency | Output tok/s | Cost | Cost per correct | Errors |\n")
	w("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, m := range r.Models {
		cost, per := usd(m.CostUSD), usd(m.CostPerRight)
		if m.Local && m.CostUSD == nil {
			cost, per = "local", "local"
		}
		hall := "-"
		if m.HallucinationRate != nil {
			hall = fmt.Sprintf("%s (%d/%d)", pct(*m.HallucinationRate), m.Hallucinated, m.Probes)
		}
		acc := "- (nothing scored yet)"
		if m.Scored > 0 {
			acc = fmt.Sprintf("%s (%s-%s), %d/%d", pct(m.Accuracy), pct(m.CILow), pct(m.CIHigh), m.Correct, m.Scored)
		}
		w("| %s | %s | %s | %s | %.1f s | %.1f s | %s | %s | %s | %d |\n",
			m.Model, acc, optF(m.MeanJudgeScore, "%.2f"), hall,
			float64(m.LatencyP50MS)/1000, float64(m.LatencyP95MS)/1000, optF(m.TokensPerSec, "%.0f"), cost, per, m.Errors)
	}

	if len(r.Categories) > 0 {
		w("\n## Accuracy by category\n\n| Category | Items |")
		for _, m := range r.Models {
			w(" %s |", m.Model)
		}
		w("\n|---|---|")
		for range r.Models {
			w("---|")
		}
		w("\n")
		for _, c := range r.Categories {
			w("| %s | %d |", c.Category, c.Items)
			for _, m := range r.Models {
				cell := c.Cells[m.Model]
				if cell.Scored == 0 {
					w(" - |")
				} else {
					w(" %s (%d/%d) |", pct(float64(cell.Correct)/float64(cell.Scored)), cell.Correct, cell.Scored)
				}
			}
			w("\n")
		}
	}

	if len(r.Judges) > 0 {
		w("\n## How far to trust the judge\n\n")
		w("On items with an objective answer, the code grader's verdict is taken as ground truth and compared with the judge's.\n\n")
		w("| Judge | Agrees with code grader | Passed wrong answers | Failed right answers | Cohen's kappa | Consistency across samples | Unreadable verdicts | Cost |\n")
		w("|---|---|---|---|---|---|---|---|\n")
		for _, j := range r.Judges {
			name := j.Judge
			if j.Primary {
				name += " (primary)"
			}
			cons := "-"
			if j.Consistent != nil {
				cons = fmt.Sprintf("%s (%d split of %d)", pct(*j.Consistent), j.Split, j.Judged)
			}
			cost := usd(j.CostUSD)
			if j.CostUSD == nil && j.Local {
				cost = "local"
			}
			w("| %s | %s (%d/%d) | %d | %d | %s | %s | %d | %s |\n", name, pct(j.VsGrader.Rate), j.VsGrader.Agree, j.VsGrader.N,
				j.VsGrader.FalsePass, j.VsGrader.FalseFail, kappa(j.VsGrader.Kappa), cons, j.Errors, cost)
		}
		for _, j := range r.Judges {
			if len(j.ByModel) == 0 {
				continue
			}
			w("\n**%s, agreement with the code grader by model answered:**\n\n| Answers from | Agreement | Passed wrong | Failed right |\n|---|---|---|---|\n", j.Judge)
			names := make([]string, 0, len(j.ByModel))
			for m := range j.ByModel {
				names = append(names, m)
			}
			sort.Strings(names)
			for _, m := range names {
				a := j.ByModel[m]
				label := m
				if m == j.Judge {
					label += " (itself)"
				}
				w("| %s | %s (%d/%d) | %d | %d |\n", label, pct(a.Rate), a.Agree, a.N, a.FalsePass, a.FalseFail)
			}
		}
		if len(r.Pairs) > 0 {
			w("\n**Judges compared with each other (all answers):**\n\n| Judges | Same verdict | Cohen's kappa |\n|---|---|---|\n")
			for _, p := range r.Pairs {
				w("| %s vs %s | %s (%d/%d) | %s |\n", p.A, p.B, pct(p.Rate), p.Agree, p.N, kappa(p.Kappa))
			}
		}
	}

	if len(r.Flags) > 0 {
		w("\n## Flagged for review\n\n")
		names := make([]string, 0, len(r.Flags))
		for f := range r.Flags {
			names = append(names, f)
		}
		sort.Strings(names)
		for _, f := range names {
			w("- `%s`: %d\n", f, r.Flags[f])
		}
	}
	w("\n## Models\n\n| Name | Provider | Model | Settings |\n|---|---|---|---|\n")
	for _, m := range r.Run.ModelConfig {
		var s []string
		s = append(s, fmt.Sprintf("max_tokens %d", m.MaxTokens))
		if m.Effort != "" {
			s = append(s, "effort "+m.Effort)
		}
		if m.Temperature != nil {
			s = append(s, fmt.Sprintf("temperature %g", *m.Temperature))
		}
		if m.Think != nil {
			s = append(s, fmt.Sprintf("think %v", *m.Think))
		}
		if m.Local {
			s = append(s, "local")
		}
		w("| %s | %s | `%s` | %s |\n", m.Name, m.Provider, m.Model, strings.Join(s, ", "))
	}
	return b.String()
}

// SummaryCSV has one row per model.
func (r *Report) SummaryCSV() []byte {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"model", "provider", "model_id", "items", "scored", "correct", "accuracy", "ci_low", "ci_high", "mean_judge_score",
		"probes", "hallucinated", "hallucination_rate", "errors", "truncated", "latency_p50_ms", "latency_p95_ms", "output_tokens_per_sec",
		"input_tokens", "output_tokens", "cost_usd", "cost_per_correct_usd", "tagged_bad", "overridden"})
	f := func(v *float64) string {
		if v == nil {
			return ""
		}
		return strconv.FormatFloat(*v, 'f', -1, 64)
	}
	for _, m := range r.Models {
		_ = cw.Write([]string{m.Model, m.Provider, m.ModelID, itoa(m.Items), itoa(m.Scored), itoa(m.Correct),
			strconv.FormatFloat(m.Accuracy, 'f', 4, 64), strconv.FormatFloat(m.CILow, 'f', 4, 64), strconv.FormatFloat(m.CIHigh, 'f', 4, 64),
			f(m.MeanJudgeScore), itoa(m.Probes), itoa(m.Hallucinated), f(m.HallucinationRate), itoa(m.Errors), itoa(m.Truncated),
			strconv.FormatInt(m.LatencyP50MS, 10), strconv.FormatInt(m.LatencyP95MS, 10), f(m.TokensPerSec),
			strconv.FormatInt(m.InputTokens, 10), strconv.FormatInt(m.OutputTokens, 10), f(m.CostUSD), f(m.CostPerRight),
			itoa(m.TaggedBad), itoa(m.Overridden)})
	}
	cw.Flush()
	return buf.Bytes()
}

// ResultsCSV has one row per answer, for analysis elsewhere.
func ResultsCSV(results []store.Result) []byte {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"item_id", "category", "model", "correct", "grader_pass", "grader_detail", "judge_pass", "judge_score",
		"judge_hallucination", "judge_agreement", "judge_reasoning", "flags", "review", "review_note", "latency_ms", "input_tokens",
		"output_tokens", "cost_usd", "error", "output"})
	b := func(v *bool) string {
		if v == nil {
			return ""
		}
		return strconv.FormatBool(*v)
	}
	f := func(v *float64) string {
		if v == nil {
			return ""
		}
		return strconv.FormatFloat(*v, 'f', -1, 64)
	}
	for _, r := range results {
		_ = cw.Write([]string{r.ItemID, r.Category, r.Model, b(r.Correct), b(r.GraderPass), r.GraderDetail, b(r.JudgePass), f(r.JudgeScore),
			b(r.JudgeHallucination), f(r.JudgeAgreement), r.JudgeReasoning, strings.Join(r.Flags, ";"), r.Review, r.ReviewNote,
			strconv.FormatInt(r.LatencyMS, 10), strconv.FormatInt(r.InputTokens, 10), strconv.FormatInt(r.OutputTokens, 10), f(r.CostUSD),
			r.Error, r.Output})
	}
	cw.Flush()
	return buf.Bytes()
}

func itoa(n int) string { return strconv.Itoa(n) }
