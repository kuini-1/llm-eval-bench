// Package store keeps datasets, runs, results and judgments in SQLite.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kuini-1/llm-eval-bench/internal/config"
	"github.com/kuini-1/llm-eval-bench/internal/dataset"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)", path))
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1) // each connection would get its own empty database
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	// Databases created before heartbeat_at existed get the column added.
	var has int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('runs') WHERE name = 'heartbeat_at'`).Scan(&has); err != nil {
		db.Close()
		return nil, err
	}
	if has == 0 {
		if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN heartbeat_at TEXT`); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func parseTime(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s.String)
	if err != nil {
		return nil
	}
	return &t
}

func boolPtr(v sql.NullInt64) *bool {
	if !v.Valid {
		return nil
	}
	b := v.Int64 == 1
	return &b
}

func nullBool(b *bool) any {
	if b == nil {
		return nil
	}
	if *b {
		return 1
	}
	return 0
}

func floatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

// --- datasets ------------------------------------------------------------

type Dataset struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	ItemCount   int            `json:"item_count"`
	SHA256      string         `json:"sha256"`
	CreatedAt   time.Time      `json:"created_at"`
	Categories  map[string]int `json:"categories,omitempty"`
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// CreateDataset stores imported items. The id is a slug of the name, with
// a suffix if that name was imported before.
func (s *Store) CreateDataset(ctx context.Context, name, description string, items []dataset.Item) (*Dataset, error) {
	base := slug(name)
	if base == "" {
		return nil, errors.New("the dataset needs a name")
	}
	canon, _ := json.Marshal(items)
	sum := sha256.Sum256(canon)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	id := base
	for n := 2; ; n++ {
		var x int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM datasets WHERE id = ?`, id).Scan(&x); errors.Is(err, sql.ErrNoRows) {
			break
		} else if err != nil {
			return nil, err
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
	d := &Dataset{ID: id, Name: name, Description: description, ItemCount: len(items), SHA256: hex.EncodeToString(sum[:]), CreatedAt: time.Now().UTC()}
	if _, err := tx.ExecContext(ctx, `INSERT INTO datasets (id, name, description, item_count, sha256, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.Name, d.Description, d.ItemCount, d.SHA256, d.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return nil, err
	}
	for i, it := range items {
		b, _ := json.Marshal(it)
		if _, err := tx.ExecContext(ctx, `INSERT INTO items (dataset_id, id, idx, category, data) VALUES (?, ?, ?, ?, ?)`, id, it.ID, i, it.Category, string(b)); err != nil {
			return nil, err
		}
	}
	return d, tx.Commit()
}

func (s *Store) Datasets(ctx context.Context) ([]Dataset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, description, item_count, sha256, created_at FROM datasets ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dataset
	for rows.Next() {
		var d Dataset
		var created string
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.ItemCount, &d.SHA256, &created); err != nil {
			return nil, err
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) Dataset(ctx context.Context, id string) (*Dataset, error) {
	var d Dataset
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, description, item_count, sha256, created_at FROM datasets WHERE id = ?`, id).
		Scan(&d.ID, &d.Name, &d.Description, &d.ItemCount, &d.SHA256, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	d.Categories = map[string]int{}
	rows, err := s.db.QueryContext(ctx, `SELECT category, COUNT(*) FROM items WHERE dataset_id = ? GROUP BY category`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		var n int
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		d.Categories[c] = n
	}
	return &d, rows.Err()
}

func (s *Store) Items(ctx context.Context, datasetID string) ([]dataset.Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM items WHERE dataset_id = ? ORDER BY idx`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dataset.Item
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var it dataset.Item
		if err := json.Unmarshal([]byte(data), &it); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// --- runs ----------------------------------------------------------------

type Run struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	DatasetID    string         `json:"dataset_id"`
	Models       []string       `json:"models"`
	ModelConfig  []config.Model `json:"model_config"`
	Judge        string         `json:"judge"`
	JudgeSamples int            `json:"judge_samples"`
	Status       string         `json:"status"`
	Error        string         `json:"error,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	StartedAt    *time.Time     `json:"started_at,omitempty"`
	FinishedAt   *time.Time     `json:"finished_at,omitempty"`
}

func (s *Store) CreateRun(ctx context.Context, r *Run) error {
	models, _ := json.Marshal(r.Models)
	cfg, _ := json.Marshal(r.ModelConfig) // config.Model omits the API key in JSON
	r.Status = "queued"
	r.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO runs (id, name, dataset_id, models, model_config, judge, judge_samples, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Name, r.DatasetID, string(models), string(cfg), r.Judge, r.JudgeSamples, r.Status, r.CreatedAt.Format(time.RFC3339Nano))
	return err
}

const runCols = `id, name, dataset_id, models, model_config, judge, judge_samples, status, error, created_at, started_at, finished_at`

func scanRun(sc interface{ Scan(...any) error }) (*Run, error) {
	var r Run
	var models, cfg, created string
	var started, finished sql.NullString
	if err := sc.Scan(&r.ID, &r.Name, &r.DatasetID, &models, &cfg, &r.Judge, &r.JudgeSamples, &r.Status, &r.Error, &created, &started, &finished); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(models), &r.Models)
	_ = json.Unmarshal([]byte(cfg), &r.ModelConfig)
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	r.StartedAt, r.FinishedAt = parseTime(started), parseTime(finished)
	return &r, nil
}

func (s *Store) Run(ctx context.Context, id string) (*Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) Runs(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runCols+` FROM runs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) SetRunStatus(ctx context.Context, id, status, errMsg string) error {
	q := `UPDATE runs SET status = ?, error = ? WHERE id = ?`
	switch status {
	case "running":
		q = `UPDATE runs SET status = ?, error = ?, started_at = COALESCE(started_at, '` + now() + `'), finished_at = NULL WHERE id = ?`
	case "done", "failed", "cancelled":
		q = `UPDATE runs SET status = ?, error = ?, finished_at = '` + now() + `' WHERE id = ?`
	}
	_, err := s.db.ExecContext(ctx, q, status, errMsg, id)
	return err
}

// Heartbeat records that this process is still working on a run.
func (s *Store) Heartbeat(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET heartbeat_at = ? WHERE id = ?`, now(), id)
	return err
}

// MarkInterrupted flags runs left "running" by a process that died: their
// heartbeat stopped more than staleAfter ago. A run another live process is
// working on (say, the CLI while the server starts) keeps its status. Runs
// marked interrupted can be resumed; finished work is kept and skipped.
func (s *Store) MarkInterrupted(ctx context.Context, staleAfter time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-staleAfter).Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE runs SET status = 'interrupted'
WHERE status IN ('running', 'queued') AND (heartbeat_at IS NULL OR heartbeat_at < ?)`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- results -------------------------------------------------------------

type Result struct {
	ID                 int64      `json:"id"`
	RunID              string     `json:"run_id"`
	ItemID             string     `json:"item_id"`
	Category           string     `json:"category"`
	Model              string     `json:"model"`
	Output             string     `json:"output"`
	Error              string     `json:"error,omitempty"`
	ErrorKind          string     `json:"error_kind,omitempty"`
	FinishReason       string     `json:"finish_reason"`
	ServedModel        string     `json:"served_model"`
	LatencyMS          int64      `json:"latency_ms"`
	InputTokens        int64      `json:"input_tokens"`
	OutputTokens       int64      `json:"output_tokens"`
	CostUSD            *float64   `json:"cost_usd"`
	Attempts           int        `json:"attempts"`
	GraderPass         *bool      `json:"grader_pass"`
	GraderDetail       string     `json:"grader_detail"`
	JudgePass          *bool      `json:"judge_pass"`
	JudgeScore         *float64   `json:"judge_score"`
	JudgeHallucination *bool      `json:"judge_hallucination"`
	JudgeReasoning     string     `json:"judge_reasoning"`
	JudgeAgreement     *float64   `json:"judge_agreement"`
	AutoCorrect        *bool      `json:"auto_correct"`
	Correct            *bool      `json:"correct"` // review override if set, else auto_correct
	Flags              []string   `json:"flags"`
	Review             string     `json:"review"`
	ReviewNote         string     `json:"review_note"`
	ReviewCorrect      *bool      `json:"review_correct"`
	ReviewedAt         *time.Time `json:"reviewed_at,omitempty"`
}

// SaveResult records a model's answer. Running the same item again (a
// resumed run) replaces the answer and clears its grades, but keeps any
// review.
func (s *Store) SaveResult(ctx context.Context, r *Result) error {
	var cost any
	if r.CostUSD != nil {
		cost = *r.CostUSD
	}
	err := s.db.QueryRowContext(ctx, `
INSERT INTO results (run_id, item_id, model, output, error, error_kind, finish_reason, served_model, latency_ms, input_tokens, output_tokens, cost_usd, attempts, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (run_id, item_id, model) DO UPDATE SET
  output = excluded.output, error = excluded.error, error_kind = excluded.error_kind, finish_reason = excluded.finish_reason,
  served_model = excluded.served_model, latency_ms = excluded.latency_ms, input_tokens = excluded.input_tokens,
  output_tokens = excluded.output_tokens, cost_usd = excluded.cost_usd, attempts = excluded.attempts,
  grader_pass = NULL, grader_detail = '', judge_pass = NULL, judge_score = NULL, judge_hallucination = NULL,
  judge_reasoning = '', judge_agreement = NULL, auto_correct = NULL, flags = '[]'
RETURNING id`,
		r.RunID, r.ItemID, r.Model, r.Output, r.Error, r.ErrorKind, r.FinishReason, r.ServedModel, r.LatencyMS, r.InputTokens, r.OutputTokens, cost, r.Attempts, now()).Scan(&r.ID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM judgments WHERE result_id = ?`, r.ID)
	return err
}

type Filter struct {
	Model    string
	Category string
	ItemID   string
	Flag     string // a flag name, or "any"
	Review   string // bad | ok | reviewed | unreviewed
	Correct  string // true | false
}

const resultCols = `r.id, r.run_id, r.item_id, i.category, r.model, r.output, r.error, r.error_kind, r.finish_reason, r.served_model, r.latency_ms,
r.input_tokens, r.output_tokens, r.cost_usd, r.attempts, r.grader_pass, r.grader_detail, r.judge_pass, r.judge_score, r.judge_hallucination,
r.judge_reasoning, r.judge_agreement, r.auto_correct, r.flags, r.review, r.review_note, r.review_correct, r.reviewed_at`

func scanResult(sc interface{ Scan(...any) error }) (*Result, error) {
	var r Result
	var cost, jscore, jagree sql.NullFloat64
	var gp, jp, jh, ac, rc sql.NullInt64
	var flags string
	var reviewed sql.NullString
	if err := sc.Scan(&r.ID, &r.RunID, &r.ItemID, &r.Category, &r.Model, &r.Output, &r.Error, &r.ErrorKind, &r.FinishReason, &r.ServedModel,
		&r.LatencyMS, &r.InputTokens, &r.OutputTokens, &cost, &r.Attempts, &gp, &r.GraderDetail, &jp, &jscore, &jh, &r.JudgeReasoning,
		&jagree, &ac, &flags, &r.Review, &r.ReviewNote, &rc, &reviewed); err != nil {
		return nil, err
	}
	r.CostUSD, r.JudgeScore, r.JudgeAgreement = floatPtr(cost), floatPtr(jscore), floatPtr(jagree)
	r.GraderPass, r.JudgePass, r.JudgeHallucination, r.AutoCorrect, r.ReviewCorrect = boolPtr(gp), boolPtr(jp), boolPtr(jh), boolPtr(ac), boolPtr(rc)
	r.ReviewedAt = parseTime(reviewed)
	_ = json.Unmarshal([]byte(flags), &r.Flags)
	if r.Flags == nil {
		r.Flags = []string{}
	}
	r.Correct = r.AutoCorrect
	if r.ReviewCorrect != nil {
		r.Correct = r.ReviewCorrect
	}
	return &r, nil
}

func (s *Store) Results(ctx context.Context, runID string, f Filter) ([]Result, error) {
	q := `SELECT ` + resultCols + ` FROM results r JOIN runs ru ON ru.id = r.run_id
JOIN items i ON i.dataset_id = ru.dataset_id AND i.id = r.item_id WHERE r.run_id = ?`
	args := []any{runID}
	if f.Model != "" {
		q += ` AND r.model = ?`
		args = append(args, f.Model)
	}
	if f.Category != "" {
		q += ` AND i.category = ?`
		args = append(args, f.Category)
	}
	if f.ItemID != "" {
		q += ` AND r.item_id = ?`
		args = append(args, f.ItemID)
	}
	switch f.Flag {
	case "":
	case "any":
		q += ` AND r.flags != '[]'`
	default:
		q += ` AND EXISTS (SELECT 1 FROM json_each(r.flags) WHERE value = ?)`
		args = append(args, f.Flag)
	}
	switch f.Review {
	case "bad", "ok":
		q += ` AND r.review = ?`
		args = append(args, f.Review)
	case "reviewed":
		q += ` AND r.review != ''`
	case "unreviewed":
		q += ` AND r.review = ''`
	}
	switch f.Correct {
	case "true":
		q += ` AND COALESCE(r.review_correct, r.auto_correct) = 1`
	case "false":
		q += ` AND COALESCE(r.review_correct, r.auto_correct) = 0`
	}
	q += ` ORDER BY i.idx, r.model`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Result
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) Result(ctx context.Context, id int64) (*Result, error) {
	r, err := scanResult(s.db.QueryRowContext(ctx, `SELECT `+resultCols+` FROM results r JOIN runs ru ON ru.id = r.run_id
JOIN items i ON i.dataset_id = ru.dataset_id AND i.id = r.item_id WHERE r.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) SetGrade(ctx context.Context, id int64, pass *bool, detail string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE results SET grader_pass = ?, grader_detail = ? WHERE id = ?`, nullBool(pass), detail, id)
	return err
}

func (s *Store) SetJudgeSummary(ctx context.Context, id int64, pass *bool, score *float64, halluc *bool, reasoning string, agreement *float64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE results SET judge_pass = ?, judge_score = ?, judge_hallucination = ?, judge_reasoning = ?, judge_agreement = ? WHERE id = ?`,
		nullBool(pass), score, nullBool(halluc), reasoning, agreement, id)
	return err
}

func (s *Store) SetFinal(ctx context.Context, id int64, correct *bool, flags []string) error {
	if flags == nil {
		flags = []string{}
	}
	b, _ := json.Marshal(flags)
	_, err := s.db.ExecContext(ctx, `UPDATE results SET auto_correct = ?, flags = ? WHERE id = ?`, nullBool(correct), string(b), id)
	return err
}

// SetReview records a reviewer's tag ("bad", "ok" or "" to clear), a note,
// and optionally an override of whether the answer counts as correct.
func (s *Store) SetReview(ctx context.Context, id int64, review, note string, correct *bool) (*Result, error) {
	switch review {
	case "", "bad", "ok":
	default:
		return nil, fmt.Errorf("review must be \"bad\", \"ok\" or empty")
	}
	var at any
	if review != "" || note != "" || correct != nil {
		at = now()
	}
	res, err := s.db.ExecContext(ctx, `UPDATE results SET review = ?, review_note = ?, review_correct = ?, reviewed_at = ? WHERE id = ?`,
		review, note, nullBool(correct), at, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.Result(ctx, id)
}

// --- judgments -----------------------------------------------------------

type Judgment struct {
	ResultID      int64    `json:"result_id"`
	Judge         string   `json:"judge"`
	Sample        int      `json:"sample"`
	Pass          *bool    `json:"pass"`
	Score         *int     `json:"score"`
	Hallucination *bool    `json:"hallucination"`
	Reasoning     string   `json:"reasoning"`
	Error         string   `json:"error,omitempty"`
	LatencyMS     int64    `json:"latency_ms"`
	InputTokens   int64    `json:"input_tokens"`
	OutputTokens  int64    `json:"output_tokens"`
	CostUSD       *float64 `json:"cost_usd"`
}

func (s *Store) SaveJudgment(ctx context.Context, j *Judgment) error {
	var score, cost any
	if j.Score != nil {
		score = *j.Score
	}
	if j.CostUSD != nil {
		cost = *j.CostUSD
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO judgments (result_id, judge, sample, pass, score, hallucination, reasoning, error, latency_ms, input_tokens, output_tokens, cost_usd, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (result_id, judge, sample) DO UPDATE SET pass = excluded.pass, score = excluded.score, hallucination = excluded.hallucination,
  reasoning = excluded.reasoning, error = excluded.error, latency_ms = excluded.latency_ms, input_tokens = excluded.input_tokens,
  output_tokens = excluded.output_tokens, cost_usd = excluded.cost_usd, created_at = excluded.created_at`,
		j.ResultID, j.Judge, j.Sample, nullBool(j.Pass), score, nullBool(j.Hallucination), j.Reasoning, j.Error, j.LatencyMS, j.InputTokens, j.OutputTokens, cost, now())
	return err
}

// Judgments returns every judgment in a run, by result id, in sample order.
func (s *Store) Judgments(ctx context.Context, runID string) (map[int64][]Judgment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT j.result_id, j.judge, j.sample, j.pass, j.score, j.hallucination, j.reasoning, j.error, j.latency_ms,
j.input_tokens, j.output_tokens, j.cost_usd FROM judgments j JOIN results r ON r.id = j.result_id WHERE r.run_id = ? ORDER BY j.result_id, j.judge, j.sample`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]Judgment{}
	for rows.Next() {
		var j Judgment
		var pass, halluc, score sql.NullInt64
		var cost sql.NullFloat64
		if err := rows.Scan(&j.ResultID, &j.Judge, &j.Sample, &pass, &score, &halluc, &j.Reasoning, &j.Error, &j.LatencyMS, &j.InputTokens, &j.OutputTokens, &cost); err != nil {
			return nil, err
		}
		j.Pass, j.Hallucination, j.CostUSD = boolPtr(pass), boolPtr(halluc), floatPtr(cost)
		if score.Valid {
			v := int(score.Int64)
			j.Score = &v
		}
		out[j.ResultID] = append(out[j.ResultID], j)
	}
	return out, rows.Err()
}

// Progress counts answered and judged results for a run.
type Progress struct {
	Total     int `json:"total"`
	Answered  int `json:"answered"`
	Judged    int `json:"judged"`
	Errors    int `json:"errors"`
	Judgeable int `json:"judgeable"`
}

func (s *Store) Progress(ctx context.Context, run *Run) (Progress, error) {
	var p Progress
	var items int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE dataset_id = ?`, run.DatasetID).Scan(&items); err != nil {
		return p, err
	}
	p.Total = items * len(run.Models)
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(error != ''), 0), COALESCE(SUM(error = ''), 0),
COALESCE(SUM(error = '' AND EXISTS (SELECT 1 FROM judgments j WHERE j.result_id = results.id AND j.judge = ?)), 0)
FROM results WHERE run_id = ?`, run.Judge, run.ID).Scan(&p.Answered, &p.Errors, &p.Judgeable, &p.Judged)
	return p, err
}
