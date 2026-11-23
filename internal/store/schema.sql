CREATE TABLE IF NOT EXISTS datasets (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  item_count  INTEGER NOT NULL,
  sha256      TEXT NOT NULL,           -- of the items as imported, to tell versions apart
  created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS items (
  dataset_id TEXT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
  id         TEXT NOT NULL,
  idx        INTEGER NOT NULL,          -- order in the file
  category   TEXT NOT NULL,
  data       TEXT NOT NULL,             -- the full item as JSON
  PRIMARY KEY (dataset_id, id)
);

CREATE TABLE IF NOT EXISTS runs (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  dataset_id    TEXT NOT NULL REFERENCES datasets(id),
  models        TEXT NOT NULL,          -- JSON array of model names, in run order
  model_config  TEXT NOT NULL,          -- JSON snapshot of their settings, without keys
  judge         TEXT NOT NULL,
  judge_samples INTEGER NOT NULL,
  status        TEXT NOT NULL,          -- queued | running | done | failed | cancelled | interrupted
  error         TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  started_at    TEXT,
  finished_at   TEXT,
  heartbeat_at  TEXT                    -- refreshed while a process is working on the run
);

CREATE TABLE IF NOT EXISTS results (
  id                  INTEGER PRIMARY KEY,
  run_id              TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  item_id             TEXT NOT NULL,
  model               TEXT NOT NULL,
  output              TEXT NOT NULL DEFAULT '',
  error               TEXT NOT NULL DEFAULT '',
  error_kind          TEXT NOT NULL DEFAULT '',  -- '' | refused | failed
  finish_reason       TEXT NOT NULL DEFAULT '',
  served_model        TEXT NOT NULL DEFAULT '',
  latency_ms          INTEGER NOT NULL DEFAULT 0,
  input_tokens        INTEGER NOT NULL DEFAULT 0,
  output_tokens       INTEGER NOT NULL DEFAULT 0,
  cost_usd            REAL,
  attempts            INTEGER NOT NULL DEFAULT 1,
  grader_pass         INTEGER,                   -- code grader; NULL when the item has none
  grader_detail       TEXT NOT NULL DEFAULT '',
  judge_pass          INTEGER,                   -- primary judge, majority over samples
  judge_score         REAL,
  judge_hallucination INTEGER,
  judge_reasoning     TEXT NOT NULL DEFAULT '',
  judge_agreement     REAL,                      -- share of samples that agree with the majority
  auto_correct        INTEGER,                   -- code grader if objective, else judge; errors count as wrong
  flags               TEXT NOT NULL DEFAULT '[]',
  review              TEXT NOT NULL DEFAULT '',  -- '' | bad | ok
  review_note         TEXT NOT NULL DEFAULT '',
  review_correct      INTEGER,                   -- a reviewer's override of auto_correct
  reviewed_at         TEXT,
  created_at          TEXT NOT NULL,
  UNIQUE (run_id, item_id, model)
);
CREATE INDEX IF NOT EXISTS results_by_run ON results(run_id, model);

CREATE TABLE IF NOT EXISTS judgments (
  result_id     INTEGER NOT NULL REFERENCES results(id) ON DELETE CASCADE,
  judge         TEXT NOT NULL,
  sample        INTEGER NOT NULL,
  pass          INTEGER,
  score         INTEGER,
  hallucination INTEGER,
  reasoning     TEXT NOT NULL DEFAULT '',
  error         TEXT NOT NULL DEFAULT '',
  latency_ms    INTEGER NOT NULL DEFAULT 0,
  input_tokens  INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  cost_usd      REAL,
  created_at    TEXT NOT NULL,
  PRIMARY KEY (result_id, judge, sample)
);
