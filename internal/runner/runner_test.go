package runner

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kuini-1/llm-eval-bench/internal/config"
	"github.com/kuini-1/llm-eval-bench/internal/dataset"
	"github.com/kuini-1/llm-eval-bench/internal/provider"
	"github.com/kuini-1/llm-eval-bench/internal/store"
)

func ptr[T any](v T) *T { return &v }

var testItems = []dataset.Item{
	{ID: "n1", Category: "math", Prompt: "6*7?", Reference: "42", Grader: dataset.Grader{Type: "number"}},
	{ID: "x1", Category: "knowledge", Prompt: "Capital of Australia?", Reference: "Canberra", Grader: dataset.Grader{Type: "exact"}},
	{ID: "p1", Category: "probe", Prompt: "Why did the Eiffel Tower move?", Reference: "It never moved.", Grader: dataset.Grader{Type: "abstain"}, Probe: true},
	{ID: "w1", Category: "writing", Prompt: "Write a haiku.", Rubric: "Three lines.", Grader: dataset.Grader{Type: "judge"}},
}

// fake answers from a function and counts calls per model.
type fake struct {
	fn    func(req provider.Request, call int) (*provider.Response, error)
	calls atomic.Int64
}

func (f *fake) Complete(ctx context.Context, req provider.Request) (*provider.Response, error) {
	n := int(f.calls.Add(1))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.fn(req, n)
}

func text(s string) *provider.Response {
	return &provider.Response{Text: s, FinishReason: "stop", InputTokens: 10, OutputTokens: 5, Latency: 20 * time.Millisecond}
}

func setup(t *testing.T, providers map[string]provider.Provider, cfg *config.Config) (*Runner, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.CreateDataset(context.Background(), "test", "", testItems); err != nil {
		t.Fatal(err)
	}
	rn := New(st, cfg)
	rn.Backoff = time.Millisecond
	rn.Clients = func(m config.Model) (provider.Provider, error) { return providers[m.Name], nil }
	return rn, st
}

func testConfig(models ...string) *config.Config {
	cfg := &config.Config{Judge: config.Judge{Model: "judge", Samples: 1, SampleTemperature: 0.8, Concurrency: 2}}
	for _, m := range append(models, "judge") {
		cfg.Models = append(cfg.Models, config.Model{Name: m, Provider: "mock", Model: m, MaxTokens: 100, Concurrency: 2, Price: &config.Price{Input: 1, Output: 2}})
	}
	return cfg
}

// A judge that passes an answer if it contains the reference text, and
// flags a hallucination when a probe answer doesn't decline.
func refJudge() *fake {
	return &fake{fn: func(req provider.Request, _ int) (*provider.Response, error) {
		it := req.Item
		pass := it.Reference != "" && strings.Contains(strings.ToLower(req.Answer), strings.ToLower(it.Reference))
		halluc := false
		if it.Grader.Type == "abstain" {
			pass = strings.Contains(req.Answer, "never")
			halluc = !pass
		}
		if it.Grader.Type == "judge" {
			pass = true
		}
		score := 2
		if pass {
			score = 5
		}
		return text(`{"reasoning":"ok","pass":` + boolStr(pass) + `,"score":` + string(rune('0'+score)) + `,"hallucination":` + boolStr(halluc) + `}`), nil
	}}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestRunScoresAndFlags(t *testing.T) {
	good := &fake{fn: func(req provider.Request, _ int) (*provider.Response, error) {
		switch req.Item.ID {
		case "n1":
			return text("6*7 = 42\nAnswer: 42"), nil
		case "x1":
			return text("Canberra"), nil
		case "p1":
			return text("It never moved; it is in Paris."), nil
		}
		return text("leaves fall\nwind\nsnow"), nil
	}}
	bad := &fake{fn: func(req provider.Request, _ int) (*provider.Response, error) {
		switch req.Item.ID {
		case "n1":
			return text("Answer: 41"), nil
		case "x1":
			return text("Sydney is the capital, I believe, though some say Canberra."), nil
		case "p1":
			return text("It moved to Lyon in 1998 after a flood."), nil
		}
		return text("a poem"), nil
	}}
	rn, st := setup(t, map[string]provider.Provider{"good": good, "bad": bad, "judge": refJudge()}, testConfig("good", "bad"))
	ctx := context.Background()
	run, err := rn.Create(ctx, "", "test", []string{"good", "bad"}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := rn.Execute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Run(ctx, run.ID)
	if got.Status != "done" {
		t.Fatalf("status %s (%s)", got.Status, got.Error)
	}
	res, err := st.Results(ctx, run.ID, store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]store.Result{}
	for _, r := range res {
		by[r.Model+"/"+r.ItemID] = r
	}
	expectCorrect := map[string]bool{"good/n1": true, "good/x1": true, "good/p1": true, "good/w1": true, "bad/n1": false, "bad/x1": false, "bad/p1": false, "bad/w1": true}
	for k, want := range expectCorrect {
		r := by[k]
		if r.Correct == nil || *r.Correct != want {
			t.Errorf("%s: correct = %v, want %v", k, r.Correct, want)
		}
	}
	// The judge passed "Sydney ... though some say Canberra" (it contains the
	// reference) while the code grader failed it: a disagreement to review.
	if !hasFlag(by["bad/x1"], "judge_disagrees") {
		t.Errorf("bad/x1 flags = %v, want judge_disagrees", by["bad/x1"].Flags)
	}
	if !hasFlag(by["bad/p1"], "hallucination") {
		t.Errorf("bad/p1 flags = %v, want hallucination", by["bad/p1"].Flags)
	}
	if c := by["good/n1"].CostUSD; c == nil || *c != (10*1+5*2)/1e6 {
		t.Errorf("cost = %v", c)
	}
}

func hasFlag(r store.Result, f string) bool {
	for _, x := range r.Flags {
		if x == f {
			return true
		}
	}
	return false
}

func TestRetriesRetryableErrorsOnly(t *testing.T) {
	flaky := &fake{fn: func(req provider.Request, call int) (*provider.Response, error) {
		if req.Item.ID == "n1" && call <= 2 {
			return nil, &provider.Error{Kind: provider.Retryable, Status: 529, Message: "overloaded"}
		}
		if req.Item.ID == "x1" {
			return nil, &provider.Error{Kind: provider.Permanent, Status: 400, Message: "bad request"}
		}
		if req.Item.ID == "p1" {
			return nil, &provider.Error{Kind: provider.Refused, Message: "the model declined"}
		}
		return text("Answer: 42"), nil
	}}
	cfg := testConfig("m")
	cfg.Models[0].Concurrency = 1 // so the first two calls are n1's
	rn, st := setup(t, map[string]provider.Provider{"m": flaky, "judge": refJudge()}, cfg)
	ctx := context.Background()
	run, _ := rn.Create(ctx, "", "test", []string{"m"}, "", 0)
	if err := rn.Execute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	res, _ := st.Results(ctx, run.ID, store.Filter{})
	by := map[string]store.Result{}
	for _, r := range res {
		by[r.ItemID] = r
	}
	if r := by["n1"]; r.Error != "" || r.Attempts != 3 {
		t.Errorf("n1: error %q after %d attempts, want success on attempt 3", r.Error, r.Attempts)
	}
	if r := by["x1"]; r.ErrorKind != "failed" || r.Attempts != 1 || !hasFlag(r, "error") || *r.Correct {
		t.Errorf("x1: %+v, want one attempt, failed, counted wrong", r)
	}
	if r := by["p1"]; r.ErrorKind != "refused" || r.Attempts != 1 {
		t.Errorf("p1: kind %q after %d attempts, want refused after 1", r.ErrorKind, r.Attempts)
	}
}

func TestResumeSkipsFinishedWork(t *testing.T) {
	model := &fake{fn: func(req provider.Request, _ int) (*provider.Response, error) { return text("Answer: 42"), nil }}
	judge := refJudge()
	rn, st := setup(t, map[string]provider.Provider{"m": model, "judge": judge}, testConfig("m"))
	ctx := context.Background()
	run, _ := rn.Create(ctx, "", "test", []string{"m"}, "", 0)
	if err := rn.Execute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	m1, j1 := model.calls.Load(), judge.calls.Load()
	if err := rn.Execute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if model.calls.Load() != m1 || judge.calls.Load() != j1 {
		t.Fatalf("resume made new calls: model %d -> %d, judge %d -> %d", m1, model.calls.Load(), j1, judge.calls.Load())
	}
	if r, _ := st.Run(ctx, run.ID); r.Status != "done" {
		t.Fatalf("status %s", r.Status)
	}
}

func TestUnreadableVerdictIsRetriedOnce(t *testing.T) {
	model := &fake{fn: func(req provider.Request, _ int) (*provider.Response, error) { return text("Answer: 42"), nil }}
	var mu sync.Mutex
	seen := map[string]int{}
	judge := &fake{fn: func(req provider.Request, _ int) (*provider.Response, error) {
		mu.Lock()
		seen[req.Item.ID]++
		n := seen[req.Item.ID]
		mu.Unlock()
		if req.Item.ID == "n1" && n == 1 {
			return text("I think this passes."), nil // no JSON
		}
		if req.Item.ID == "x1" || req.Item.ID == "w1" {
			return text("not json either"), nil
		}
		return text(`{"reasoning":"r","pass":true,"score":4,"hallucination":false}`), nil
	}}
	rn, st := setup(t, map[string]provider.Provider{"m": model, "judge": judge}, testConfig("m"))
	ctx := context.Background()
	run, _ := rn.Create(ctx, "", "test", []string{"m"}, "", 0)
	if err := rn.Execute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	res, _ := st.Results(ctx, run.ID, store.Filter{})
	for _, r := range res {
		switch r.ItemID {
		case "n1":
			if r.JudgePass == nil {
				t.Error("n1 should have a verdict after one retry")
			}
		case "x1":
			// Code-graded: the code grader still decides, and a missing
			// verdict isn't worth a reviewer's time.
			if r.JudgePass != nil || hasFlag(r, "unjudged") || r.Correct == nil || *r.Correct {
				t.Errorf("x1: judge_pass %v correct %v flags %v", r.JudgePass, r.Correct, r.Flags)
			}
		case "w1":
			// Judge-only: no verdict means unscored, and flagged.
			if r.Correct != nil || !hasFlag(r, "unjudged") {
				t.Errorf("w1: correct %v flags %v, want unscored and unjudged", r.Correct, r.Flags)
			}
		}
	}
	if seen["x1"] != 2 {
		t.Errorf("x1 judged %d times, want 2 (one retry)", seen["x1"])
	}
}

func TestRescoreRecomputesWithoutCallingModels(t *testing.T) {
	model := &fake{fn: func(req provider.Request, _ int) (*provider.Response, error) { return text("Answer: 42"), nil }}
	judge := refJudge()
	rn, st := setup(t, map[string]provider.Provider{"m": model, "judge": judge}, testConfig("m"))
	ctx := context.Background()
	run, _ := rn.Create(ctx, "", "test", []string{"m"}, "", 0)
	if err := rn.Execute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := st.Run(ctx, run.ID)
	res, _ := st.Results(ctx, run.ID, store.Filter{ItemID: "n1"})
	// Simulate stale stored verdicts, e.g. from an older grader.
	if err := st.SetGrade(ctx, res[0].ID, ptr(false), "stale"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFinal(ctx, res[0].ID, ptr(false), []string{"stale"}); err != nil {
		t.Fatal(err)
	}
	calls := model.calls.Load() + judge.calls.Load()
	if err := rn.Rescore(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	res, _ = st.Results(ctx, run.ID, store.Filter{ItemID: "n1"})
	if r := res[0]; !*r.Correct || r.GraderDetail == "stale" || hasFlag(r, "stale") {
		t.Errorf("rescore didn't recompute: %+v", r)
	}
	if model.calls.Load()+judge.calls.Load() != calls {
		t.Error("rescore must not call any model")
	}
	after, _ := st.Run(ctx, run.ID)
	if after.Status != "done" || !after.FinishedAt.Equal(*before.FinishedAt) {
		t.Errorf("rescore changed the run: %s, finished %v -> %v", after.Status, before.FinishedAt, after.FinishedAt)
	}
}

func TestCancelKeepsWorkAndResumeFinishes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	model := &fake{fn: func(req provider.Request, call int) (*provider.Response, error) {
		if call == 2 {
			cancel()
			return nil, context.Canceled
		}
		return text("Answer: 42"), nil
	}}
	cfg := testConfig("m")
	cfg.Models[0].Concurrency = 1
	rn, st := setup(t, map[string]provider.Provider{"m": model, "judge": refJudge()}, cfg)
	run, _ := rn.Create(context.Background(), "", "test", []string{"m"}, "", 0)
	err := rn.Execute(ctx, run.ID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	r, _ := st.Run(context.Background(), run.ID)
	if r.Status != "cancelled" {
		t.Fatalf("status %s", r.Status)
	}
	if err := rn.Execute(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	res, _ := st.Results(context.Background(), run.ID, store.Filter{})
	if len(res) != len(testItems) {
		t.Fatalf("%d results after resume, want %d", len(res), len(testItems))
	}
}

func TestCombine(t *testing.T) {
	j := func(sample int, pass, halluc bool, score int) store.Judgment {
		return store.Judgment{Judge: "j", Sample: sample, Pass: &pass, Hallucination: &halluc, Score: &score, Reasoning: "s" + string(rune('0'+sample))}
	}
	m, ok := Combine([]store.Judgment{j(0, true, false, 5), j(1, false, false, 2), j(2, true, false, 4)}, "j")
	if !ok || !m.Pass || m.Agreement != 2.0/3 || m.Score != 11.0/3 || m.Reasoning != "s0" {
		t.Fatalf("majority = %+v", m)
	}
	m, _ = Combine([]store.Judgment{j(0, false, true, 1), j(1, true, false, 5)}, "j")
	if m.Pass || !m.Hallucination || m.Agreement != 0.5 {
		t.Fatalf("a tie should follow the first sample: %+v", m)
	}
	errored := store.Judgment{Judge: "j", Sample: 1, Error: "bad json"}
	m, _ = Combine([]store.Judgment{j(0, true, false, 5), errored, {Judge: "other", Pass: ptr(false)}}, "j")
	if m.N != 1 || m.Agreement != 1 {
		t.Fatalf("errors and other judges should be ignored: %+v", m)
	}
	if _, ok := Combine(nil, "j"); ok {
		t.Fatal("no judgments should mean no verdict")
	}
}

func TestDecide(t *testing.T) {
	probe := dataset.Item{Grader: dataset.Grader{Type: "abstain"}, Probe: true}
	// Heuristic says it declined, judge says it hallucinated: flag it.
	c, flags := Decide(probe, store.Result{GraderPass: ptr(true), JudgePass: ptr(false), JudgeHallucination: ptr(true), JudgeAgreement: ptr(1.0)})
	if *c || strings.Join(flags, ",") != "hallucination,heuristic_disagrees" {
		t.Fatalf("correct %v flags %v", *c, flags)
	}
	// Objective item: the code grader decides even when the judge disagrees.
	c, flags = Decide(dataset.Item{Grader: dataset.Grader{Type: "number"}}, store.Result{GraderPass: ptr(false), JudgePass: ptr(true), JudgeAgreement: ptr(0.5), FinishReason: "length"})
	if *c || strings.Join(flags, ",") != "truncated,judge_split,judge_disagrees" {
		t.Fatalf("correct %v flags %v", *c, flags)
	}
	// Judge-only item with no verdict is unscored.
	c, flags = Decide(dataset.Item{Grader: dataset.Grader{Type: "judge"}}, store.Result{})
	if c != nil || strings.Join(flags, ",") != "unjudged" {
		t.Fatalf("correct %v flags %v", c, flags)
	}
}

func TestProvisionalScoresUnfinishedResults(t *testing.T) {
	items := map[string]dataset.Item{"n": {Grader: dataset.Grader{Type: "number"}}}
	results := []store.Result{
		{ItemID: "n", GraderPass: ptr(true), JudgePass: ptr(false)},                                                                    // not finalized
		{ItemID: "n", GraderPass: ptr(true), AutoCorrect: ptr(false), Correct: ptr(false), Flags: []string{}},                          // finalized: left alone
		{ItemID: "n", GraderPass: ptr(true), JudgePass: ptr(false), ReviewCorrect: ptr(false), Correct: ptr(false), Flags: []string{}}, // reviewer said wrong
	}
	Provisional(items, results)
	if c := results[0].Correct; c == nil || !*c || strings.Join(results[0].Flags, ",") != "judge_disagrees" {
		t.Errorf("unfinished: correct %v flags %v", c, results[0].Flags)
	}
	if *results[1].Correct || len(results[1].Flags) != 0 {
		t.Errorf("finalized result changed: %+v", results[1])
	}
	if *results[2].Correct || strings.Join(results[2].Flags, ",") != "judge_disagrees" {
		t.Errorf("a review override must win: %+v", results[2])
	}
}

func TestCreateValidates(t *testing.T) {
	rn, _ := setup(t, nil, testConfig("m"))
	ctx := context.Background()
	for _, c := range []struct {
		dataset string
		models  []string
		judge   string
		want    string
	}{
		{"nope", []string{"m"}, "", "dataset"},
		{"test", nil, "", "at least one model"},
		{"test", []string{"m", "m"}, "", "listed twice"},
		{"test", []string{"ghost"}, "", "not configured"},
		{"test", []string{"m"}, "ghost", "judge"},
	} {
		if _, err := rn.Create(ctx, "", c.dataset, c.models, c.judge, 0); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Create(%s, %v, %q) = %v, want error containing %q", c.dataset, c.models, c.judge, err, c.want)
		}
	}
	cfg := testConfig("m")
	cfg.Models[0].Provider = "anthropic" // no API key
	rn2, _ := setup(t, nil, cfg)
	if _, err := rn2.Create(ctx, "", "test", []string{"m"}, "", 0); err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Errorf("err = %v, want no API key", err)
	}
}
