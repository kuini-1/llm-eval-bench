// Package report turns a run's results into comparison tables: per-model
// accuracy, hallucination rate, latency and cost, a per-category breakdown,
// and how far the LLM judge can be trusted on this run.
package report

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/kuini-1/llm-eval-bench/internal/config"
	"github.com/kuini-1/llm-eval-bench/internal/dataset"
	"github.com/kuini-1/llm-eval-bench/internal/runner"
	"github.com/kuini-1/llm-eval-bench/internal/store"
)

type ModelRow struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	ModelID  string `json:"model_id"`
	Local    bool   `json:"local"`

	Items   int `json:"items"`
	Scored  int `json:"scored"` // has a correct/incorrect decision
	Correct int `json:"correct"`
	// Accuracy over scored items, with a 95% Wilson interval. Small
	// datasets give wide intervals; overlapping intervals mean the
	// difference may be noise.
	Accuracy float64 `json:"accuracy"`
	CILow    float64 `json:"ci_low"`
	CIHigh   float64 `json:"ci_high"`

	MeanJudgeScore *float64 `json:"mean_judge_score"`

	Probes            int      `json:"probes"`
	Hallucinated      int      `json:"hallucinated"`
	HallucinationRate *float64 `json:"hallucination_rate"`

	Errors    int `json:"errors"`
	Truncated int `json:"truncated"`

	LatencyP50MS int64    `json:"latency_p50_ms"`
	LatencyP95MS int64    `json:"latency_p95_ms"`
	TokensPerSec *float64 `json:"output_tokens_per_sec"`
	InputTokens  int64    `json:"input_tokens"`
	OutputTokens int64    `json:"output_tokens"`
	CostUSD      *float64 `json:"cost_usd"`
	CostPerRight *float64 `json:"cost_per_correct_usd"`

	TaggedBad  int `json:"tagged_bad"`
	Overridden int `json:"overridden"`
}

type Cell struct {
	Correct int `json:"correct"`
	Scored  int `json:"scored"`
}

type CategoryRow struct {
	Category string          `json:"category"`
	Items    int             `json:"items"`
	Cells    map[string]Cell `json:"cells"`
}

// Agreement between a judge and the code graders on items with an
// objective answer, where the code grader is taken as ground truth.
type Agreement struct {
	N         int      `json:"n"`
	Agree     int      `json:"agree"`
	FalsePass int      `json:"false_pass"` // judge passed a wrong answer
	FalseFail int      `json:"false_fail"` // judge failed a right answer
	Rate      float64  `json:"rate"`
	Kappa     *float64 `json:"kappa"` // Cohen's kappa; agreement beyond chance
}

type JudgeRow struct {
	Judge      string               `json:"judge"`
	Primary    bool                 `json:"primary"`
	Local      bool                 `json:"local"`
	Judged     int                  `json:"judged"`
	Errors     int                  `json:"errors"`
	VsGrader   Agreement            `json:"vs_code_grader"`
	ByModel    map[string]Agreement `json:"by_model"` // shows if the judge favours some models, e.g. itself
	Samples    int                  `json:"samples"`
	Split      int                  `json:"split"` // answers whose repeated verdicts disagreed
	Consistent *float64             `json:"consistency"`
	PassRate   float64              `json:"pass_rate"`
	CostUSD    *float64             `json:"cost_usd"`
	LatencyP50 int64                `json:"latency_p50_ms"`
}

type JudgePair struct {
	A, B      string   `json:"-"`
	Judges    []string `json:"judges"`
	N         int      `json:"n"`
	Agree     int      `json:"agree"`
	Rate      float64  `json:"rate"`
	Kappa     *float64 `json:"kappa"`
	Objective int      `json:"objective_items"`
}

type Report struct {
	Run        store.Run      `json:"run"`
	Dataset    store.Dataset  `json:"dataset"`
	Progress   store.Progress `json:"progress"`
	Models     []ModelRow     `json:"models"`
	Categories []CategoryRow  `json:"categories"`
	Judges     []JudgeRow     `json:"judges"`
	Pairs      []JudgePair    `json:"judge_pairs"`
	Flags      map[string]int `json:"flags"`
	Generated  time.Time      `json:"generated_at"`
}

// Wilson returns the 95% Wilson score interval for k successes in n.
func Wilson(k, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 0
	}
	const z = 1.959964
	p := float64(k) / float64(n)
	d := 1 + z*z/float64(n)
	c := (p + z*z/(2*float64(n))) / d
	h := z * math.Sqrt(p*(1-p)/float64(n)+z*z/(4*float64(n)*float64(n))) / d
	return math.Max(0, c-h), math.Min(1, c+h)
}

// Kappa is Cohen's kappa for two binary raters given the 2x2 counts.
func Kappa(bothYes, aYesBNo, aNoBYes, bothNo int) *float64 {
	n := float64(bothYes + aYesBNo + aNoBYes + bothNo)
	if n == 0 {
		return nil
	}
	po := float64(bothYes+bothNo) / n
	pa, pb := float64(bothYes+aYesBNo)/n, float64(bothYes+aNoBYes)/n
	pe := pa*pb + (1-pa)*(1-pb)
	if pe == 1 {
		return nil // both raters gave one label to everything; kappa is undefined
	}
	k := (po - pe) / (1 - pe)
	return &k
}

func percentile(xs []int64, p float64) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := int(math.Ceil(p*float64(len(s)))) - 1
	return s[max(0, min(i, len(s)-1))]
}

type tally struct{ yy, yn, ny, nn int }

func (t *tally) add(a, b bool) {
	switch {
	case a && b:
		t.yy++
	case a && !b:
		t.yn++
	case !a && b:
		t.ny++
	default:
		t.nn++
	}
}

// agreement treats a as the judge and b as the code grader.
func (t tally) agreement() Agreement {
	n := t.yy + t.yn + t.ny + t.nn
	a := Agreement{N: n, Agree: t.yy + t.nn, FalsePass: t.yn, FalseFail: t.ny, Kappa: Kappa(t.yy, t.yn, t.ny, t.nn)}
	if n > 0 {
		a.Rate = float64(a.Agree) / float64(n)
	}
	return a
}

func Build(ctx context.Context, st *store.Store, runID string) (*Report, error) {
	run, err := st.Run(ctx, runID)
	if err != nil {
		return nil, err
	}
	ds, err := st.Dataset(ctx, run.DatasetID)
	if err != nil {
		return nil, err
	}
	items, err := st.Items(ctx, run.DatasetID)
	if err != nil {
		return nil, err
	}
	results, err := st.Results(ctx, run.ID, store.Filter{})
	if err != nil {
		return nil, err
	}
	judgments, err := st.Judgments(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	prog, err := st.Progress(ctx, run)
	if err != nil {
		return nil, err
	}
	byID := map[string]dataset.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	cfg := map[string]config.Model{}
	for _, m := range run.ModelConfig {
		cfg[m.Name] = m
	}
	rep := &Report{Run: *run, Dataset: *ds, Progress: prog, Flags: map[string]int{}, Generated: time.Now().UTC()}

	// A run stores its final verdicts at the end; until then, score what is there.
	runner.Provisional(byID, results)

	// --- per model
	byModel := map[string][]store.Result{}
	for _, r := range results {
		byModel[r.Model] = append(byModel[r.Model], r)
		for _, f := range r.Flags {
			rep.Flags[f]++
		}
	}
	for _, name := range run.Models {
		m := cfg[name]
		row := ModelRow{Model: name, Provider: m.Provider, ModelID: m.Model, Local: m.Local}
		var lat []int64
		var latSum int64
		var scoreSum float64
		var scoreN int
		var cost float64
		priced := m.Price != nil
		for _, r := range byModel[name] {
			row.Items++
			it := byID[r.ItemID]
			if r.Correct != nil {
				row.Scored++
				if *r.Correct {
					row.Correct++
				}
			}
			if r.ReviewCorrect != nil {
				row.Overridden++
			}
			if r.Review == "bad" {
				row.TaggedBad++
			}
			if r.Error != "" {
				row.Errors++
				continue
			}
			if r.FinishReason == "length" {
				row.Truncated++
			}
			lat = append(lat, r.LatencyMS)
			latSum += r.LatencyMS
			row.InputTokens += r.InputTokens
			row.OutputTokens += r.OutputTokens
			if r.CostUSD != nil {
				cost += *r.CostUSD
			}
			if r.JudgeScore != nil {
				scoreSum += *r.JudgeScore
				scoreN++
			}
			if (it.Probe || it.Grader.Type == "abstain") && r.JudgeHallucination != nil {
				row.Probes++
				if *r.JudgeHallucination {
					row.Hallucinated++
				}
			}
		}
		if row.Scored > 0 {
			row.Accuracy = float64(row.Correct) / float64(row.Scored)
			row.CILow, row.CIHigh = Wilson(row.Correct, row.Scored)
		}
		if scoreN > 0 {
			v := scoreSum / float64(scoreN)
			row.MeanJudgeScore = &v
		}
		if row.Probes > 0 {
			v := float64(row.Hallucinated) / float64(row.Probes)
			row.HallucinationRate = &v
		}
		row.LatencyP50MS, row.LatencyP95MS = percentile(lat, 0.5), percentile(lat, 0.95)
		if latSum > 0 {
			v := float64(row.OutputTokens) / (float64(latSum) / 1000)
			row.TokensPerSec = &v
		}
		if priced {
			c := cost
			row.CostUSD = &c
			if row.Correct > 0 {
				per := cost / float64(row.Correct)
				row.CostPerRight = &per
			}
		}
		rep.Models = append(rep.Models, row)
	}

	// --- per category, in dataset order
	var cats []string
	seenCat := map[string]bool{}
	catItems := map[string]int{}
	for _, it := range items {
		if !seenCat[it.Category] {
			seenCat[it.Category] = true
			cats = append(cats, it.Category)
		}
		catItems[it.Category]++
	}
	cells := map[string]map[string]Cell{}
	for _, r := range results {
		if cells[r.Category] == nil {
			cells[r.Category] = map[string]Cell{}
		}
		c := cells[r.Category][r.Model]
		if r.Correct != nil {
			c.Scored++
			if *r.Correct {
				c.Correct++
			}
		}
		cells[r.Category][r.Model] = c
	}
	for _, c := range cats {
		rep.Categories = append(rep.Categories, CategoryRow{Category: c, Items: catItems[c], Cells: cells[c]})
	}

	// --- judges
	judgeNames := []string{run.Judge}
	for _, js := range judgments {
		for _, j := range js {
			if !containsStr(judgeNames, j.Judge) {
				judgeNames = append(judgeNames, j.Judge)
			}
		}
	}
	sort.Strings(judgeNames[1:])
	verdicts := map[string]map[int64]bool{} // judge -> result -> majority pass
	for _, jn := range judgeNames {
		jrow := JudgeRow{Judge: jn, Primary: jn == run.Judge, ByModel: map[string]Agreement{}}
		jm, ok := cfg[jn]
		jrow.Local = ok && jm.Local
		var vs tally
		perModel := map[string]*tally{}
		var lat []int64
		var cost float64
		var passes, maxSamples, multi int
		verdicts[jn] = map[int64]bool{}
		for _, r := range results {
			if r.Error != "" {
				continue
			}
			mine := []store.Judgment{}
			for _, j := range judgments[r.ID] {
				if j.Judge != jn {
					continue
				}
				mine = append(mine, j)
				if j.Error != "" {
					jrow.Errors++
				}
				lat = append(lat, j.LatencyMS)
				if j.CostUSD != nil {
					cost += *j.CostUSD
				}
			}
			maj, ok := runner.Combine(mine, jn)
			if !ok {
				continue
			}
			jrow.Judged++
			verdicts[jn][r.ID] = maj.Pass
			if maj.Pass {
				passes++
			}
			maxSamples = max(maxSamples, maj.N)
			if maj.N > 1 {
				multi++
				if maj.Agreement < 1 {
					jrow.Split++
				}
			}
			if byID[r.ItemID].Grader.Objective() && r.GraderPass != nil {
				vs.add(maj.Pass, *r.GraderPass)
				if perModel[r.Model] == nil {
					perModel[r.Model] = &tally{}
				}
				perModel[r.Model].add(maj.Pass, *r.GraderPass)
			}
		}
		jrow.VsGrader = vs.agreement()
		for m, t := range perModel {
			jrow.ByModel[m] = t.agreement()
		}
		jrow.Samples = maxSamples
		if multi > 0 {
			v := 1 - float64(jrow.Split)/float64(multi)
			jrow.Consistent = &v
		}
		if jrow.Judged > 0 {
			jrow.PassRate = float64(passes) / float64(jrow.Judged)
		}
		if ok && jm.Price != nil {
			c := cost
			jrow.CostUSD = &c
		}
		jrow.LatencyP50 = percentile(lat, 0.5)
		rep.Judges = append(rep.Judges, jrow)
	}
	for i := 0; i < len(judgeNames); i++ {
		for k := i + 1; k < len(judgeNames); k++ {
			a, b := judgeNames[i], judgeNames[k]
			var t tally
			objective := 0
			for _, r := range results {
				va, okA := verdicts[a][r.ID]
				vb, okB := verdicts[b][r.ID]
				if okA && okB {
					t.add(va, vb)
					if byID[r.ItemID].Grader.Objective() {
						objective++
					}
				}
			}
			ag := t.agreement()
			rep.Pairs = append(rep.Pairs, JudgePair{A: a, B: b, Judges: []string{a, b}, N: ag.N, Agree: ag.Agree, Rate: ag.Rate, Kappa: ag.Kappa, Objective: objective})
		}
	}
	return rep, nil
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
