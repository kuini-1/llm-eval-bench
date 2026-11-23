// Package runner executes a benchmark run in three phases:
//
//  1. Answer: each model answers every item. Models run one after another,
//     each with its own concurrency, so a local judge or another local model
//     never competes for the machine while latency is being measured.
//  2. Grade: the code grader checks every answer that has an objective check.
//  3. Judge: the LLM judge grades every answer, one or more times.
//
// Work that is already done is skipped, so an interrupted run can resume.
package runner

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/kuini-1/llm-eval-bench/internal/config"
	"github.com/kuini-1/llm-eval-bench/internal/dataset"
	"github.com/kuini-1/llm-eval-bench/internal/grade"
	"github.com/kuini-1/llm-eval-bench/internal/provider"
	"github.com/kuini-1/llm-eval-bench/internal/store"
)

type Runner struct {
	Store  *store.Store
	Config *config.Config
	Log    func(format string, args ...any)
	// Attempts per call for retryable errors, with exponential backoff.
	Attempts int
	Backoff  time.Duration
	// Clients builds providers; tests replace it.
	Clients func(config.Model) (provider.Provider, error)

	mu      sync.Mutex
	clients map[string]provider.Provider
}

func New(st *store.Store, cfg *config.Config) *Runner {
	return &Runner{Store: st, Config: cfg, Attempts: 3, Backoff: 2 * time.Second, Clients: provider.New, Log: func(string, ...any) {}}
}

func (r *Runner) client(m config.Model) (provider.Provider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.clients == nil {
		r.clients = map[string]provider.Provider{}
	}
	if c, ok := r.clients[m.Name]; ok {
		return c, nil
	}
	c, err := r.Clients(m)
	if err != nil {
		return nil, err
	}
	r.clients[m.Name] = c
	return c, nil
}

// Create validates a run request and stores it as queued.
func (r *Runner) Create(ctx context.Context, name, datasetID string, models []string, judge string, samples int) (*store.Run, error) {
	if _, err := r.Store.Dataset(ctx, datasetID); err != nil {
		return nil, fmt.Errorf("dataset %q: %w", datasetID, err)
	}
	if len(models) == 0 {
		return nil, errors.New("pick at least one model")
	}
	if judge == "" {
		judge = r.Config.Judge.Model
	}
	if samples <= 0 {
		samples = r.Config.Judge.Samples
	}
	if samples > 5 {
		return nil, errors.New("at most 5 judge samples per answer")
	}
	listed := map[string]bool{}
	for _, name := range models {
		if listed[name] {
			return nil, fmt.Errorf("model %q is listed twice", name)
		}
		listed[name] = true
	}
	// Snapshot the settings of every model involved (keys are never serialized).
	var snapshot []config.Model
	for _, name := range models {
		m := r.Config.Find(name)
		if m == nil {
			return nil, fmt.Errorf("model %q is not configured", name)
		}
		if ok, why := m.Ready(); !ok {
			return nil, fmt.Errorf("model %q: %s", name, why)
		}
		snapshot = append(snapshot, *m)
	}
	if jm := r.Config.Find(judge); jm == nil {
		return nil, fmt.Errorf("judge %q is not configured", judge)
	} else if ok, why := jm.Ready(); !ok {
		return nil, fmt.Errorf("judge %q: %s", judge, why)
	} else if !listed[judge] {
		snapshot = append(snapshot, *jm)
	}
	if name == "" {
		name = fmt.Sprintf("%s, %d models", datasetID, len(models))
	}
	run := &store.Run{ID: newID(), Name: name, DatasetID: datasetID, Models: models, ModelConfig: snapshot, Judge: judge, JudgeSamples: samples}
	return run, r.Store.CreateRun(ctx, run)
}

func newID() string {
	return fmt.Sprintf("run_%s_%04x", time.Now().UTC().Format("20060102_150405"), rand.IntN(0x10000))
}

// Execute runs (or resumes) every phase, and records the final status.
func (r *Runner) Execute(ctx context.Context, runID string) error {
	run, err := r.Store.Run(ctx, runID)
	if err != nil {
		return err
	}
	items, err := r.Store.Items(ctx, run.DatasetID)
	if err != nil {
		return err
	}
	if err := r.Store.SetRunStatus(ctx, run.ID, "running", ""); err != nil {
		return err
	}
	stop := r.heartbeat(ctx, run.ID)
	err = r.execute(ctx, run, items)
	stop()
	status, msg := "done", ""
	switch {
	case errors.Is(err, context.Canceled):
		status, msg = "cancelled", "cancelled"
	case err != nil:
		status, msg = "failed", err.Error()
	}
	// Record the outcome even if ctx is cancelled.
	if serr := r.Store.SetRunStatus(context.WithoutCancel(ctx), run.ID, status, msg); serr != nil && err == nil {
		err = serr
	}
	r.Log("%s: %s", run.ID, status)
	return err
}

func (r *Runner) execute(ctx context.Context, run *store.Run, items []dataset.Item) error {
	for _, name := range run.Models {
		if err := r.answer(ctx, run, name, items); err != nil {
			return err
		}
	}
	byID := map[string]dataset.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if err := r.grade(ctx, run, byID); err != nil {
		return err
	}
	if err := r.Judge(ctx, run.ID, run.Judge, run.JudgeSamples, true); err != nil {
		return err
	}
	return r.Finalize(ctx, run.ID)
}

// HeartbeatEvery is how often a working process refreshes a run's heartbeat.
const HeartbeatEvery = 10 * time.Second

// heartbeat keeps the run's heartbeat fresh until the returned func is called.
func (r *Runner) heartbeat(ctx context.Context, id string) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(HeartbeatEvery)
		defer t.Stop()
		for {
			if err := r.Store.Heartbeat(ctx, id); err != nil && ctx.Err() == nil {
				r.Log("%s: heartbeat: %v", id, err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// call makes one request with retries for retryable errors.
func (r *Runner) call(ctx context.Context, c provider.Provider, req provider.Request) (*provider.Response, int, error) {
	attempts := max(r.Attempts, 1)
	for a := 1; ; a++ {
		resp, err := c.Complete(ctx, req)
		if err == nil {
			return resp, a, nil
		}
		if a >= attempts || !provider.IsRetryable(err) || ctx.Err() != nil {
			return nil, a, err
		}
		wait := r.Backoff * time.Duration(1<<(a-1))
		wait += time.Duration(rand.Int64N(int64(wait)/4 + 1)) // jitter
		select {
		case <-ctx.Done():
			return nil, a, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// warmUp loads a local model into memory so the first timed answer doesn't
// include the load.
func (r *Runner) warmUp(ctx context.Context, m config.Model, c provider.Provider, it dataset.Item) {
	if !m.Local {
		return
	}
	start := time.Now()
	_, err := c.Complete(ctx, provider.Request{Messages: []provider.Message{{Role: "user", Content: "Reply with OK."}}, MaxTokens: 5, Item: &it})
	if err != nil {
		r.Log("%s: warm-up failed: %v", m.Name, err)
		return
	}
	r.Log("%s: warmed up in %s", m.Name, time.Since(start).Round(time.Millisecond))
}

func (r *Runner) answer(ctx context.Context, run *store.Run, name string, items []dataset.Item) error {
	m := r.Config.Find(name)
	if m == nil {
		return fmt.Errorf("model %q is no longer configured", name)
	}
	c, err := r.client(*m)
	if err != nil {
		return err
	}
	done, err := r.Store.Results(ctx, run.ID, store.Filter{Model: name})
	if err != nil {
		return err
	}
	skip := map[string]bool{}
	for _, d := range done {
		if d.Error == "" {
			skip[d.ItemID] = true
		}
	}
	var todo []dataset.Item
	for _, it := range items {
		if !skip[it.ID] {
			todo = append(todo, it)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	r.Log("%s: answering %d items (%d already done)", name, len(todo), len(skip))
	r.warmUp(ctx, *m, c, todo[0])

	return parallel(ctx, m.Concurrency, todo, func(ctx context.Context, it dataset.Item) error {
		req := provider.Request{System: it.System, Messages: []provider.Message{{Role: "user", Content: it.UserMessage()}},
			MaxTokens: m.MaxTokens, Temperature: m.Temperature, Item: &it}
		resp, attempts, err := r.call(ctx, c, req)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res := &store.Result{RunID: run.ID, ItemID: it.ID, Model: name, Attempts: attempts}
		if err != nil {
			res.Error, res.ErrorKind = err.Error(), "failed"
			var pe *provider.Error
			if errors.As(err, &pe) && pe.Kind == provider.Refused {
				res.ErrorKind = "refused"
			}
			r.Log("%s %s: %v", name, it.ID, err)
		} else {
			res.Output, res.FinishReason, res.ServedModel = resp.Text, resp.FinishReason, resp.Model
			res.LatencyMS, res.InputTokens, res.OutputTokens = resp.Latency.Milliseconds(), resp.InputTokens, resp.OutputTokens
			res.CostUSD = m.Cost(resp.InputTokens, resp.OutputTokens)
		}
		return r.Store.SaveResult(ctx, res)
	})
}

// parallel runs fn over items with n workers and stops at the first error.
func parallel[T any](ctx context.Context, n int, items []T, fn func(context.Context, T) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	ch := make(chan T)
	var wg sync.WaitGroup
	for range max(n, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range ch {
				if err := fn(ctx, it); err != nil {
					cancel(err)
					return
				}
			}
		}()
	}
loop:
	for _, it := range items {
		select {
		case ch <- it:
		case <-ctx.Done():
			break loop
		}
	}
	close(ch)
	wg.Wait()
	if err := context.Cause(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return ctx.Err()
}

func (r *Runner) grade(ctx context.Context, run *store.Run, items map[string]dataset.Item) error {
	results, err := r.Store.Results(ctx, run.ID, store.Filter{})
	if err != nil {
		return err
	}
	for _, res := range results {
		if res.Error != "" {
			continue
		}
		o, ok := grade.Check(items[res.ItemID], res.Output)
		var pass *bool
		if ok {
			pass = &o.Pass
		}
		if err := r.Store.SetGrade(ctx, res.ID, pass, o.Detail); err != nil {
			return err
		}
	}
	return nil
}

// Judge grades every answer in a run with the named judge model. The
// primary judge's majority verdict is stored on each result; other judges
// (a re-judge) are stored only as judgments, for comparison.
func (r *Runner) Judge(ctx context.Context, runID, judgeName string, samples int, primary bool) error {
	run, err := r.Store.Run(ctx, runID)
	if err != nil {
		return err
	}
	jm := r.Config.Find(judgeName)
	if jm == nil {
		return fmt.Errorf("judge %q is not configured", judgeName)
	}
	if ok, why := jm.Ready(); !ok {
		return fmt.Errorf("judge %q: %s", judgeName, why)
	}
	c, err := r.client(*jm)
	if err != nil {
		return err
	}
	items, err := r.Store.Items(ctx, run.DatasetID)
	if err != nil {
		return err
	}
	byID := map[string]dataset.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	results, err := r.Store.Results(ctx, run.ID, store.Filter{})
	if err != nil {
		return err
	}
	existing, err := r.Store.Judgments(ctx, run.ID)
	if err != nil {
		return err
	}
	type job struct {
		res     store.Result
		samples []int
	}
	var todo []job
	for _, res := range results {
		if res.Error != "" {
			continue // nothing to judge; it already counts as wrong
		}
		have := map[int]bool{}
		for _, j := range existing[res.ID] {
			if j.Judge == judgeName && j.Error == "" {
				have[j.Sample] = true
			}
		}
		var missing []int
		for s := range samples {
			if !have[s] {
				missing = append(missing, s)
			}
		}
		if len(missing) > 0 {
			todo = append(todo, job{res, missing})
		} else if primary {
			if err := r.summarize(ctx, res.ID, judgeName, existing[res.ID]); err != nil {
				return err
			}
		}
	}
	if len(todo) == 0 {
		return nil
	}
	r.Log("judge %s: %d answers to grade, %d sample(s) each", judgeName, len(todo), samples)
	stop := r.heartbeat(ctx, run.ID)
	defer stop()
	if len(todo) > 0 {
		r.warmUp(ctx, *jm, c, byID[todo[0].res.ItemID])
	}
	return parallel(ctx, r.Config.Judge.Concurrency, todo, func(ctx context.Context, jb job) error {
		it := byID[jb.res.ItemID]
		for _, s := range jb.samples {
			j := r.judgeOnce(ctx, c, *jm, it, jb.res, s)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			j.Judge = judgeName
			if err := r.Store.SaveJudgment(ctx, j); err != nil {
				return err
			}
		}
		if !primary {
			return nil
		}
		all, err := r.Store.Judgments(ctx, run.ID)
		if err != nil {
			return err
		}
		return r.summarize(ctx, jb.res.ID, judgeName, all[jb.res.ID])
	})
}

func (r *Runner) judgeOnce(ctx context.Context, c provider.Provider, jm config.Model, it dataset.Item, res store.Result, sample int) *store.Judgment {
	temp := jm.Temperature
	if sample > 0 {
		t := r.Config.Judge.SampleTemperature
		temp = &t
	}
	req := provider.Request{System: grade.JudgeSystem, Messages: []provider.Message{{Role: "user", Content: grade.JudgePrompt(it, res.Output)}},
		MaxTokens: jm.MaxTokens, Temperature: temp, JSONSchema: grade.VerdictSchema, Item: &it, Answer: res.Output, Sample: sample}
	j := &store.Judgment{ResultID: res.ID, Sample: sample}
	// An unreadable verdict gets one more try; a provider error has already
	// been retried inside call.
	for try := 0; try < 2; try++ {
		resp, _, err := r.call(ctx, c, req)
		if err != nil {
			j.Error = err.Error()
			return j
		}
		j.LatencyMS += resp.Latency.Milliseconds()
		j.InputTokens += resp.InputTokens
		j.OutputTokens += resp.OutputTokens
		v, perr := grade.ParseVerdict(resp.Text)
		if perr != nil {
			j.Error = perr.Error()
			continue
		}
		j.Error = ""
		j.Pass, j.Hallucination, j.Score, j.Reasoning = &v.Pass, &v.Hallucination, &v.Score, v.Reasoning
		break
	}
	j.CostUSD = jm.Cost(j.InputTokens, j.OutputTokens)
	return j
}

// Majority combines a judge's samples into one verdict. A tie goes to the
// first sample. agreement is the share of samples that match the majority.
type Majority struct {
	Pass, Hallucination bool
	Score               float64
	Agreement           float64
	Reasoning           string
	N                   int
}

func Combine(js []store.Judgment, judge string) (Majority, bool) {
	var ok []store.Judgment
	for _, j := range js {
		if j.Judge == judge && j.Error == "" && j.Pass != nil {
			ok = append(ok, j)
		}
	}
	if len(ok) == 0 {
		return Majority{}, false
	}
	sort.Slice(ok, func(a, b int) bool { return ok[a].Sample < ok[b].Sample })
	vote := func(get func(store.Judgment) bool) (bool, int) {
		n := 0
		for _, j := range ok {
			if get(j) {
				n++
			}
		}
		switch {
		case 2*n > len(ok):
			return true, n
		case 2*n < len(ok):
			return false, len(ok) - n
		default:
			v := get(ok[0])
			return v, len(ok) / 2
		}
	}
	m := Majority{N: len(ok)}
	var agree int
	m.Pass, agree = vote(func(j store.Judgment) bool { return *j.Pass })
	m.Hallucination, _ = vote(func(j store.Judgment) bool { return j.Hallucination != nil && *j.Hallucination })
	m.Agreement = float64(agree) / float64(len(ok))
	var sum float64
	for _, j := range ok {
		if j.Score != nil {
			sum += float64(*j.Score)
		}
		if m.Reasoning == "" && *j.Pass == m.Pass {
			m.Reasoning = j.Reasoning
		}
	}
	m.Score = sum / float64(len(ok))
	return m, true
}

func (r *Runner) summarize(ctx context.Context, resultID int64, judge string, js []store.Judgment) error {
	m, ok := Combine(js, judge)
	if !ok {
		return r.Store.SetJudgeSummary(ctx, resultID, nil, nil, nil, "", nil)
	}
	return r.Store.SetJudgeSummary(ctx, resultID, &m.Pass, &m.Score, &m.Hallucination, m.Reasoning, &m.Agreement)
}

// Rescore re-runs the code graders and the scoring rules on a run's stored
// answers and verdicts. No model is called and the run's status and times
// stay as they are, so a grader fix can be applied to a finished run.
func (r *Runner) Rescore(ctx context.Context, runID string) error {
	run, err := r.Store.Run(ctx, runID)
	if err != nil {
		return err
	}
	items, err := r.Store.Items(ctx, run.DatasetID)
	if err != nil {
		return err
	}
	byID := map[string]dataset.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if err := r.grade(ctx, run, byID); err != nil {
		return err
	}
	return r.Finalize(ctx, runID)
}

// Finalize decides which answers count as correct and flags the ones a
// person should look at.
func (r *Runner) Finalize(ctx context.Context, runID string) error {
	run, err := r.Store.Run(ctx, runID)
	if err != nil {
		return err
	}
	items, err := r.Store.Items(ctx, run.DatasetID)
	if err != nil {
		return err
	}
	byID := map[string]dataset.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	results, err := r.Store.Results(ctx, run.ID, store.Filter{})
	if err != nil {
		return err
	}
	for _, res := range results {
		correct, flags := Decide(byID[res.ItemID], res)
		if err := r.Store.SetFinal(ctx, res.ID, correct, flags); err != nil {
			return err
		}
	}
	return nil
}

// Provisional scores and flags results that have no final verdict yet (a
// run still in progress, or stopped part-way) by applying Decide to what is
// there, so partial runs can be compared and reviewed. A reviewer's verdict
// always wins.
func Provisional(items map[string]dataset.Item, results []store.Result) {
	for i, r := range results {
		if r.AutoCorrect != nil {
			continue // finalized
		}
		c, flags := Decide(items[r.ItemID], r)
		if flags == nil {
			flags = []string{}
		}
		results[i].Flags = flags
		if r.ReviewCorrect == nil {
			results[i].Correct = c
		}
	}
}

// Decide applies the scoring rules to one result:
//
//   - a failed or refused call is wrong;
//   - an item with an objective check is decided by the code grader;
//   - everything else (open-ended answers, hallucination probes) by the judge;
//   - no judge verdict means unscored (nil).
//
// Flags mark disagreements and anything else worth a human look.
func Decide(it dataset.Item, res store.Result) (*bool, []string) {
	var flags []string
	if res.Error != "" {
		f := false
		return &f, []string{"error"}
	}
	if res.FinishReason == "length" {
		flags = append(flags, "truncated")
	}
	// A missing verdict matters only where the judge decides correctness;
	// on code-graded items it just drops out of the judge's statistics.
	if res.JudgePass == nil && !(it.Grader.Objective() && res.GraderPass != nil) {
		flags = append(flags, "unjudged")
	}
	if res.JudgeHallucination != nil && *res.JudgeHallucination {
		flags = append(flags, "hallucination")
	}
	if res.JudgeAgreement != nil && *res.JudgeAgreement < 1 {
		flags = append(flags, "judge_split")
	}
	var correct *bool
	switch {
	case it.Grader.Objective() && res.GraderPass != nil:
		correct = res.GraderPass
		if res.JudgePass != nil && *res.JudgePass != *res.GraderPass {
			flags = append(flags, "judge_disagrees")
		}
	default:
		correct = res.JudgePass
		if grade.Heuristic(it) && res.GraderPass != nil && res.JudgePass != nil {
			declined := *res.JudgePass && (res.JudgeHallucination == nil || !*res.JudgeHallucination)
			if declined != *res.GraderPass {
				flags = append(flags, "heuristic_disagrees")
			}
		}
	}
	return correct, flags
}
