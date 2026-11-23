// Package server is the JSON API behind the web UI. See assets/openapi.yaml.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kuini-1/llm-eval-bench/assets"
	"github.com/kuini-1/llm-eval-bench/internal/config"
	"github.com/kuini-1/llm-eval-bench/internal/dataset"
	"github.com/kuini-1/llm-eval-bench/internal/report"
	"github.com/kuini-1/llm-eval-bench/internal/runner"
	"github.com/kuini-1/llm-eval-bench/internal/store"
)

type Server struct {
	Store  *store.Store
	Runner *runner.Runner
	Config *config.Config
	Log    *log.Logger

	ctx    context.Context
	mu     sync.Mutex
	active map[string]context.CancelFunc
}

func New(ctx context.Context, st *store.Store, rn *runner.Runner, cfg *config.Config, logger *log.Logger) *Server {
	return &Server{Store: st, Runner: rn, Config: cfg, Log: logger, ctx: ctx, active: map[string]context.CancelFunc{}}
}

// Routes lists every route; a test checks each one is in the OpenAPI spec.
var Routes = []string{
	"GET /api/health",
	"GET /api/models",
	"GET /api/datasets",
	"POST /api/datasets",
	"GET /api/datasets/{id}",
	"GET /api/runs",
	"POST /api/runs",
	"GET /api/runs/{id}",
	"POST /api/runs/{id}/cancel",
	"POST /api/runs/{id}/resume",
	"POST /api/runs/{id}/judge",
	"GET /api/runs/{id}/results",
	"GET /api/runs/{id}/export",
	"GET /api/results/{id}",
	"PATCH /api/results/{id}",
	"GET /openapi.yaml",
}

func (s *Server) Handler() http.Handler {
	h := map[string]http.HandlerFunc{
		"GET /api/health":            s.health,
		"GET /api/models":            s.models,
		"GET /api/datasets":          s.listDatasets,
		"POST /api/datasets":         s.importDataset,
		"GET /api/datasets/{id}":     s.getDataset,
		"GET /api/runs":              s.listRuns,
		"POST /api/runs":             s.createRun,
		"GET /api/runs/{id}":         s.getRun,
		"POST /api/runs/{id}/cancel": s.cancelRun,
		"POST /api/runs/{id}/resume": s.resumeRun,
		"POST /api/runs/{id}/judge":  s.rejudge,
		"GET /api/runs/{id}/results": s.results,
		"GET /api/runs/{id}/export":  s.export,
		"GET /api/results/{id}":      s.getResult,
		"PATCH /api/results/{id}":    s.review,
		"GET /openapi.yaml": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			w.Write(assets.OpenAPI)
		},
	}
	mux := http.NewServeMux()
	for _, route := range Routes {
		mux.HandleFunc(route, h[route])
	}
	return s.logRequests(mux)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if r.URL.Path != "/api/health" {
			s.Log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
}

func writeErr(w http.ResponseWriter, status int, code, msg string, details ...string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg, Details: details}})
}

func (s *Server) storeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	s.Log.Printf("error: %v", err)
	writeErr(w, http.StatusInternalServerError, "internal", "internal error")
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type modelInfo struct {
	config.Model
	Ready   bool   `json:"ready"`
	Reason  string `json:"reason,omitempty"`
	IsJudge bool   `json:"default_judge"`
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	out := []modelInfo{}
	for _, m := range s.Config.Models {
		ok, why := m.Ready()
		out = append(out, modelInfo{Model: m, Ready: ok, Reason: why, IsJudge: m.Name == s.Config.Judge.Model})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- datasets --------------------------------------------------------------

func (s *Server) listDatasets(w http.ResponseWriter, r *http.Request) {
	ds, err := s.Store.Datasets(r.Context())
	if err != nil {
		s.storeErr(w, err)
		return
	}
	if ds == nil {
		ds = []store.Dataset{}
	}
	writeJSON(w, http.StatusOK, ds)
}

// importDataset takes a multipart upload (file, name, description) or a
// JSON body {name, description, format, content}.
func (s *Server) importDataset(w http.ResponseWriter, r *http.Request) {
	var name, desc, format string
	var body io.Reader
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(20 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "could not read the upload: "+err.Error())
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "the upload needs a file field")
			return
		}
		defer f.Close()
		body, format = f, dataset.FormatFor(hdr.Filename)
		name, desc = r.FormValue("name"), r.FormValue("description")
		if name == "" {
			name = strings.TrimSuffix(strings.TrimSuffix(hdr.Filename, ".jsonl"), ".csv")
		}
	} else {
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Format      string `json:"format"`
			Content     string `json:"content"`
		}
		if err := decode(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid JSON: "+err.Error())
			return
		}
		name, desc, format, body = req.Name, req.Description, req.Format, strings.NewReader(req.Content)
		if format == "" {
			format = "jsonl"
		}
	}
	items, errs := dataset.Parse(body, format)
	if len(errs) > 0 {
		details := make([]string, 0, len(errs))
		for _, e := range errs {
			details = append(details, e.Error())
		}
		writeErr(w, http.StatusUnprocessableEntity, "invalid_dataset", fmt.Sprintf("%d problem(s) in the file; nothing was imported", len(errs)), details...)
		return
	}
	d, err := s.Store.CreateDataset(r.Context(), name, desc, items)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) getDataset(w http.ResponseWriter, r *http.Request) {
	d, err := s.Store.Dataset(r.Context(), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	items, err := s.Store.Items(r.Context(), d.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dataset": d, "items": items})
}

// --- runs ------------------------------------------------------------------

type runSummary struct {
	store.Run
	Progress store.Progress `json:"progress"`
	Active   bool           `json:"active"`
}

func (s *Server) isActive(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.active[id]
	return ok
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Store.Runs(r.Context())
	if err != nil {
		s.storeErr(w, err)
		return
	}
	out := []runSummary{}
	for _, run := range runs {
		p, err := s.Store.Progress(r.Context(), &run)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		out = append(out, runSummary{Run: run, Progress: p, Active: s.isActive(run.ID)})
	}
	writeJSON(w, http.StatusOK, out)
}

// start runs fn in the background, at most once per run at a time.
func (s *Server) start(id string, fn func(ctx context.Context) error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.active[id]; busy {
		return false
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.active[id] = cancel
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.active, id)
			s.mu.Unlock()
			cancel()
		}()
		if err := fn(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.Log.Printf("run %s: %v", id, err)
		}
	}()
	return true
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string   `json:"name"`
		DatasetID    string   `json:"dataset_id"`
		Models       []string `json:"models"`
		Judge        string   `json:"judge"`
		JudgeSamples int      `json:"judge_samples"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid JSON: "+err.Error())
		return
	}
	run, err := s.Runner.Create(r.Context(), req.Name, req.DatasetID, req.Models, req.Judge, req.JudgeSamples)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.start(run.ID, func(ctx context.Context) error { return s.Runner.Execute(ctx, run.ID) })
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	rep, err := report.Build(r.Context(), s.Store, r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": rep, "active": s.isActive(rep.Run.ID)})
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	cancel, ok := s.active[id]
	s.mu.Unlock()
	if !ok {
		writeErr(w, http.StatusConflict, "not_running", "this run isn't running in this server")
		return
	}
	cancel()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
}

func (s *Server) resumeRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.Store.Run(r.Context(), id)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	if run.Status == "done" {
		writeErr(w, http.StatusConflict, "already_done", "this run has finished")
		return
	}
	if !s.start(id, func(ctx context.Context) error { return s.Runner.Execute(ctx, id) }) {
		writeErr(w, http.StatusConflict, "already_running", "this run is already running")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "resuming"})
}

// rejudge grades a finished run again with another judge, for comparison.
// The primary judge's verdicts, and so the scores, stay as they are.
func (s *Server) rejudge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Judge   string `json:"judge"`
		Samples int    `json:"samples"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid JSON: "+err.Error())
		return
	}
	run, err := s.Store.Run(r.Context(), id)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	if run.Status != "done" {
		writeErr(w, http.StatusConflict, "not_done", "a run can be re-judged once it has finished")
		return
	}
	if req.Judge == run.Judge {
		writeErr(w, http.StatusBadRequest, "invalid_request", "that is already this run's primary judge")
		return
	}
	jm := s.Config.Find(req.Judge)
	if jm == nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("judge %q is not configured", req.Judge))
		return
	}
	if ok, why := jm.Ready(); !ok {
		writeErr(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("judge %q: %s", req.Judge, why))
		return
	}
	samples := max(req.Samples, 1)
	if !s.start(id, func(ctx context.Context) error { return s.Runner.Judge(ctx, id, req.Judge, samples, false) }) {
		writeErr(w, http.StatusConflict, "already_running", "this run is busy")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "judging", "judge": req.Judge})
}

func filterFrom(r *http.Request) store.Filter {
	q := r.URL.Query()
	return store.Filter{Model: q.Get("model"), Category: q.Get("category"), ItemID: q.Get("item"), Flag: q.Get("flag"), Review: q.Get("review"), Correct: q.Get("correct")}
}

func (s *Server) results(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.Store.Run(r.Context(), id); err != nil {
		s.storeErr(w, err)
		return
	}
	// The correctness and flag filters run after provisional scoring, so
	// they also work on runs that haven't finished.
	f := filterFrom(r)
	wantCorrect, wantFlag := f.Correct, f.Flag
	f.Correct, f.Flag = "", ""
	res, err := s.Store.Results(r.Context(), id, f)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	if err := s.provisional(r.Context(), id, res); err != nil {
		s.storeErr(w, err)
		return
	}
	out := []store.Result{}
	for _, x := range res {
		if wantCorrect != "" && (x.Correct == nil || strconv.FormatBool(*x.Correct) != wantCorrect) {
			continue
		}
		if wantFlag == "any" && len(x.Flags) == 0 {
			continue
		}
		if wantFlag != "" && wantFlag != "any" && !slices.Contains(x.Flags, wantFlag) {
			continue
		}
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, out)
}

// provisional scores answers in runs that haven't finished, the same way
// the report does.
func (s *Server) provisional(ctx context.Context, runID string, res []store.Result) error {
	run, err := s.Store.Run(ctx, runID)
	if err != nil {
		return err
	}
	items, err := s.Store.Items(ctx, run.DatasetID)
	if err != nil {
		return err
	}
	byID := make(map[string]dataset.Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	runner.Provisional(byID, res)
	return nil
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	format, detail := r.URL.Query().Get("format"), r.URL.Query().Get("detail")
	rep, err := report.Build(r.Context(), s.Store, id)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	filename := id
	switch {
	case format == "csv" && detail == "results":
		res, err := s.Store.Results(r.Context(), id, filterFrom(r))
		if err != nil {
			s.storeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`-results.csv"`)
		w.Write(report.ResultsCSV(res))
	case format == "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`-summary.csv"`)
		w.Write(rep.SummaryCSV())
	case format == "md" || format == "":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.md"`)
		io.WriteString(w, rep.Markdown())
	default:
		writeErr(w, http.StatusBadRequest, "invalid_request", "format must be md or csv")
	}
}

func (s *Server) resultID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "result id must be a number")
		return 0, false
	}
	return id, true
}

func (s *Server) getResult(w http.ResponseWriter, r *http.Request) {
	id, ok := s.resultID(w, r)
	if !ok {
		return
	}
	res, err := s.Store.Result(r.Context(), id)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	js, err := s.Store.Judgments(r.Context(), res.RunID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	mine := js[res.ID]
	if mine == nil {
		mine = []store.Judgment{}
	}
	one := []store.Result{*res}
	if err := s.provisional(r.Context(), res.RunID, one); err != nil {
		s.storeErr(w, err)
		return
	}
	res = &one[0]
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "judgments": mine})
}

// review tags an answer. "correct" overrides the automatic verdict; send
// null (or leave it out) to go back to the automatic one.
func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	id, ok := s.resultID(w, r)
	if !ok {
		return
	}
	var req struct {
		Review  string `json:"review"`
		Note    string `json:"note"`
		Correct *bool  `json:"correct"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid JSON: "+err.Error())
		return
	}
	res, err := s.Store.SetReview(r.Context(), id, req.Review, strings.TrimSpace(req.Note), req.Correct)
	if errors.Is(err, store.ErrNotFound) {
		s.storeErr(w, err)
		return
	} else if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
