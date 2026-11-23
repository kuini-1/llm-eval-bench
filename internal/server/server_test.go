package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kuini-1/llm-eval-bench/assets"
	"github.com/kuini-1/llm-eval-bench/internal/config"
	"github.com/kuini-1/llm-eval-bench/internal/runner"
	"github.com/kuini-1/llm-eval-bench/internal/store"
	"go.yaml.in/yaml/v3"
)

func newServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	cfg := &config.Config{
		Models: []config.Model{
			{Name: "strong", Provider: "mock", Model: "mock-strong", MaxTokens: 100, Concurrency: 4, Price: &config.Price{Input: 3, Output: 15}},
			{Name: "weak", Provider: "mock", Model: "mock-weak", MaxTokens: 100, Concurrency: 4},
			{Name: "judge", Provider: "mock", Model: "mock-judge", MaxTokens: 100, Concurrency: 4},
			{Name: "judge2", Provider: "mock", Model: "mock-judge", MaxTokens: 100, Concurrency: 4},
			{Name: "keyless", Provider: "anthropic", Model: "claude-opus-5-5", MaxTokens: 100, Concurrency: 1},
		},
		Judge: config.Judge{Model: "judge", Samples: 1, SampleTemperature: 0.8, Concurrency: 4},
	}
	st, err := store.Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := New(ctx, st, runner.New(st, cfg), cfg, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		cancel()
		ts.Close()
		st.Close()
	})
	return ts, st
}

func do(t *testing.T, ts *httptest.Server, method, path string, body any, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		raw, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: %v\n%s", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

func importBundled(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	data, err := os.ReadFile("../../datasets/evalbench-v1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "evalbench-v1.jsonl")
	fw.Write(data)
	mw.WriteField("description", "bundled")
	mw.Close()
	resp, err := http.Post(ts.URL+"/api/datasets", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var d store.Dataset
	json.NewDecoder(resp.Body).Decode(&d)
	if resp.StatusCode != http.StatusCreated || d.ItemCount != 72 {
		t.Fatalf("import: %d %+v", resp.StatusCode, d)
	}
	return d.ID
}

func waitDone(t *testing.T, ts *httptest.Server, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var out struct {
			Report map[string]any `json:"report"`
			Active bool           `json:"active"`
		}
		do(t, ts, "GET", "/api/runs/"+id, nil, &out)
		run := out.Report["run"].(map[string]any)
		if run["status"] == "done" && !out.Active {
			return out.Report
		}
		if run["status"] == "failed" {
			t.Fatalf("run failed: %v", run["error"])
		}
		if time.Now().After(deadline) {
			t.Fatalf("run still %v", run["status"])
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestEndToEnd(t *testing.T) {
	ts, _ := newServer(t)
	dsID := importBundled(t, ts)

	var run store.Run
	if code := do(t, ts, "POST", "/api/runs", map[string]any{"dataset_id": dsID, "models": []string{"strong", "weak"}, "judge_samples": 3}, &run); code != http.StatusAccepted {
		t.Fatalf("create run: %d", code)
	}
	rep := waitDone(t, ts, run.ID)
	models := rep["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("models: %v", models)
	}
	strong := models[0].(map[string]any)
	if strong["scored"].(float64) != 72 || strong["cost_usd"] == nil {
		t.Fatalf("strong row: %v", strong)
	}
	judges := rep["judges"].([]any)
	if c := judges[0].(map[string]any)["consistency"]; c == nil {
		t.Fatal("3 samples per answer should give a consistency figure")
	}

	// Filters.
	var flagged []store.Result
	do(t, ts, "GET", "/api/runs/"+run.ID+"/results?flag=any&model=weak", nil, &flagged)
	if len(flagged) == 0 {
		t.Fatal("expected flagged answers from the weak model")
	}
	for _, r := range flagged {
		if r.Model != "weak" || len(r.Flags) == 0 {
			t.Fatalf("filter returned %s with flags %v", r.Model, r.Flags)
		}
	}

	// Review: tagging a correct answer as bad flips it and changes accuracy.
	var right []store.Result
	do(t, ts, "GET", "/api/runs/"+run.ID+"/results?model=strong&correct=true", nil, &right)
	before := strong["correct"].(float64)
	var updated store.Result
	if code := do(t, ts, "PATCH", "/api/results/"+itoa(right[0].ID), map[string]any{"review": "bad", "note": "wrong unit", "correct": false}, &updated); code != 200 {
		t.Fatalf("review: %d", code)
	}
	if updated.Correct == nil || *updated.Correct || updated.Review != "bad" || updated.ReviewNote != "wrong unit" {
		t.Fatalf("updated: %+v", updated)
	}
	var after struct {
		Report struct {
			Models []struct {
				Correct    int `json:"correct"`
				TaggedBad  int `json:"tagged_bad"`
				Overridden int `json:"overridden"`
			} `json:"models"`
		} `json:"report"`
	}
	do(t, ts, "GET", "/api/runs/"+run.ID, nil, &after)
	if m := after.Report.Models[0]; float64(m.Correct) != before-1 || m.TaggedBad != 1 || m.Overridden != 1 {
		t.Fatalf("after review: %+v (correct before %v)", m, before)
	}
	if code := do(t, ts, "PATCH", "/api/results/"+itoa(right[0].ID), map[string]any{"review": "meh"}, nil); code != 400 {
		t.Fatalf("invalid review tag: %d", code)
	}

	// A second judge, compared with the first.
	if code := do(t, ts, "POST", "/api/runs/"+run.ID+"/judge", map[string]any{"judge": "judge2"}, nil); code != http.StatusAccepted {
		t.Fatalf("rejudge: %d", code)
	}
	rep = waitDone(t, ts, run.ID)
	if pairs, _ := rep["judge_pairs"].([]any); len(pairs) != 1 {
		t.Fatalf("judge pairs: %v", rep["judge_pairs"])
	}

	// Exports.
	for _, q := range []string{"format=md", "format=csv", "format=csv&detail=results"} {
		resp, err := http.Get(ts.URL + "/api/runs/" + run.ID + "/export?" + q)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || len(body) < 100 {
			t.Fatalf("export %s: %d, %d bytes", q, resp.StatusCode, len(body))
		}
		if q == "format=md" && !strings.Contains(string(body), "## How far to trust the judge") {
			t.Fatal("markdown export has no judge section")
		}
		if q == "format=csv&detail=results" && strings.Count(string(body), "\n") < 145 {
			t.Fatal("results CSV should have a row per answer")
		}
	}
}

func TestRejectsBadRequests(t *testing.T) {
	ts, _ := newServer(t)
	var e struct {
		Error apiError `json:"error"`
	}
	code := do(t, ts, "POST", "/api/datasets", map[string]any{"name": "bad", "content": `{"id":"a","category":"x","prompt":"p","reference":"ten","grader":"number"}` + "\n" + `{"id":"b","category":"x","prompt":"p","grader":"nope"}`}, &e)
	if code != http.StatusUnprocessableEntity || len(e.Error.Details) != 2 {
		t.Fatalf("invalid dataset: %d %+v", code, e)
	}
	code = do(t, ts, "POST", "/api/datasets", map[string]any{"name": "ok", "content": `{"id":"a","category":"x","prompt":"6*7?","reference":"42","grader":"number"}`}, nil)
	if code != http.StatusCreated {
		t.Fatalf("valid dataset: %d", code)
	}
	code = do(t, ts, "POST", "/api/runs", map[string]any{"dataset_id": "ok", "models": []string{"keyless"}}, &e)
	if code != 400 || !strings.Contains(e.Error.Message, "no API key") {
		t.Fatalf("keyless model: %d %+v", code, e)
	}
	if code := do(t, ts, "GET", "/api/runs/nope", nil, nil); code != 404 {
		t.Fatalf("missing run: %d", code)
	}
	if code := do(t, ts, "POST", "/api/runs/nope/cancel", nil, nil); code != 409 {
		t.Fatalf("cancel idle run: %d", code)
	}
}

func TestModelsHideKeys(t *testing.T) {
	ts, _ := newServer(t)
	resp, err := http.Get(ts.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "api_key") {
		t.Fatal("the models endpoint must not include API key fields")
	}
	if !strings.Contains(string(body), `"reason":"no API key set"`) {
		t.Fatalf("keyless model should say why it isn't ready: %s", body)
	}
}

// Every route the server registers must be documented in assets/openapi.yaml.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(assets.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	for _, r := range Routes {
		method, path, _ := strings.Cut(r, " ")
		ops, ok := spec.Paths[path]
		if !ok {
			t.Errorf("%s is not in openapi.yaml", path)
			continue
		}
		if _, ok := ops[strings.ToLower(method)]; !ok {
			t.Errorf("%s %s is not in openapi.yaml", method, path)
		}
	}
	documented := 0
	for _, ops := range spec.Paths {
		documented += len(ops)
	}
	if documented != len(Routes) {
		t.Errorf("openapi.yaml documents %d operations, the server has %d", documented, len(Routes))
	}
}

func TestInterruptedRunsOnlyWhenHeartbeatIsStale(t *testing.T) {
	_, st := newServer(t)
	ctx := context.Background()
	if _, err := st.CreateDataset(ctx, "d", "", nil); err != nil {
		t.Fatal(err)
	}
	live := &store.Run{ID: "live", Name: "l", DatasetID: "d", Models: []string{"strong"}, Judge: "judge", JudgeSamples: 1}
	dead := &store.Run{ID: "dead", Name: "d", DatasetID: "d", Models: []string{"strong"}, Judge: "judge", JudgeSamples: 1}
	for _, r := range []*store.Run{live, dead} {
		st.CreateRun(ctx, r)
		st.SetRunStatus(ctx, r.ID, "running", "")
	}
	st.Heartbeat(ctx, "live")
	n, err := st.MarkInterrupted(ctx, time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("marked %d, err %v; want 1", n, err)
	}
	if r, _ := st.Run(ctx, "live"); r.Status != "running" {
		t.Fatalf("live run is %s", r.Status)
	}
	if r, _ := st.Run(ctx, "dead"); r.Status != "interrupted" {
		t.Fatalf("dead run is %s", r.Status)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
