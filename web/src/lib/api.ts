// Types mirror the Go API (assets/openapi.yaml).

export interface ModelInfo {
  name: string;
  provider: string;
  model: string;
  base_url?: string;
  max_tokens: number;
  effort?: string;
  temperature?: number;
  local: boolean;
  price?: { input: number; output: number };
  ready: boolean;
  reason?: string;
  default_judge: boolean;
}

export interface Dataset {
  id: string;
  name: string;
  description: string;
  item_count: number;
  sha256: string;
  created_at: string;
  categories?: Record<string, number>;
}

export interface Grader {
  type: string;
  aliases?: string[];
  tolerance?: number;
  must_include?: string[];
  must_not_include?: string[];
  pattern?: string;
  expected?: Record<string, unknown>;
}

export interface Item {
  id: string;
  category: string;
  prompt: string;
  system?: string;
  context?: string;
  reference?: string;
  grader: Grader;
  rubric?: string;
  hallucination_probe?: boolean;
  tags?: string[];
}

export interface Progress {
  total: number;
  answered: number;
  errors: number;
  judgeable: number;
  judged: number;
}

export interface Run {
  id: string;
  name: string;
  dataset_id: string;
  models: string[];
  judge: string;
  judge_samples: number;
  status: "queued" | "running" | "done" | "failed" | "cancelled" | "interrupted";
  error?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
}

export interface RunSummary extends Run {
  progress: Progress;
  active: boolean;
}

export interface ModelRow {
  model: string;
  provider: string;
  model_id: string;
  local: boolean;
  items: number;
  scored: number;
  correct: number;
  accuracy: number;
  ci_low: number;
  ci_high: number;
  mean_judge_score: number | null;
  probes: number;
  hallucinated: number;
  hallucination_rate: number | null;
  errors: number;
  truncated: number;
  latency_p50_ms: number;
  latency_p95_ms: number;
  output_tokens_per_sec: number | null;
  input_tokens: number;
  output_tokens: number;
  cost_usd: number | null;
  cost_per_correct_usd: number | null;
  tagged_bad: number;
  overridden: number;
}

export interface Agreement {
  n: number;
  agree: number;
  false_pass: number;
  false_fail: number;
  rate: number;
  kappa: number | null;
}

export interface JudgeRow {
  judge: string;
  primary: boolean;
  local: boolean;
  judged: number;
  errors: number;
  vs_code_grader: Agreement;
  by_model: Record<string, Agreement>;
  samples: number;
  split: number;
  consistency: number | null;
  pass_rate: number;
  cost_usd: number | null;
}

export interface JudgePair {
  judges: string[];
  n: number;
  agree: number;
  rate: number;
  kappa: number | null;
}

export interface Report {
  run: Run;
  dataset: Dataset;
  progress: Progress;
  models: ModelRow[];
  categories: { category: string; items: number; cells: Record<string, { correct: number; scored: number }> | null }[];
  judges: JudgeRow[];
  judge_pairs: JudgePair[] | null;
  flags: Record<string, number>;
}

export interface Result {
  id: number;
  run_id: string;
  item_id: string;
  category: string;
  model: string;
  output: string;
  error?: string;
  error_kind?: string;
  finish_reason: string;
  latency_ms: number;
  input_tokens: number;
  output_tokens: number;
  cost_usd: number | null;
  attempts: number;
  grader_pass: boolean | null;
  grader_detail: string;
  judge_pass: boolean | null;
  judge_score: number | null;
  judge_hallucination: boolean | null;
  judge_reasoning: string;
  judge_agreement: number | null;
  auto_correct: boolean | null;
  correct: boolean | null;
  flags: string[];
  review: "" | "bad" | "ok";
  review_note: string;
  review_correct: boolean | null;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public details: string[] = [],
  ) {
    super(message);
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, cache: "no-store" };
  if (body instanceof FormData) {
    init.body = body;
  } else if (body !== undefined) {
    init.body = JSON.stringify(body);
    init.headers = { "Content-Type": "application/json" };
  }
  let res = await fetch(path, init).catch(() => null);
  // Reads are retried once: right after the API restarts, the dev proxy's
  // first request can fail on a stale keep-alive connection.
  if (method === "GET" && (res === null || res.status >= 502 || res.status === 500)) {
    await new Promise((r) => setTimeout(r, 400));
    res = await fetch(path, init);
  }
  if (res === null) throw new ApiError(0, "The API can't be reached.");
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    let details: string[] = [];
    try {
      const e = await res.json();
      msg = e.error?.message ?? msg;
      details = e.error?.details ?? [];
    } catch {
      /* not JSON */
    }
    throw new ApiError(res.status, msg, details);
  }
  return res.json() as Promise<T>;
}

export type ResultFilter = Partial<Record<"model" | "category" | "item" | "flag" | "review" | "correct", string>>;

export const api = {
  models: () => call<ModelInfo[]>("GET", "/api/models"),
  datasets: () => call<Dataset[]>("GET", "/api/datasets"),
  dataset: (id: string) => call<{ dataset: Dataset; items: Item[] }>("GET", `/api/datasets/${encodeURIComponent(id)}`),
  importDataset: (form: FormData) => call<Dataset>("POST", "/api/datasets", form),
  runs: () => call<RunSummary[]>("GET", "/api/runs"),
  createRun: (r: { name: string; dataset_id: string; models: string[]; judge: string; judge_samples: number }) =>
    call<Run>("POST", "/api/runs", r),
  run: (id: string) => call<{ report: Report; active: boolean }>("GET", `/api/runs/${id}`),
  cancel: (id: string) => call<unknown>("POST", `/api/runs/${id}/cancel`),
  resume: (id: string) => call<unknown>("POST", `/api/runs/${id}/resume`),
  rejudge: (id: string, judge: string, samples: number) => call<unknown>("POST", `/api/runs/${id}/judge`, { judge, samples }),
  results: (id: string, f: ResultFilter = {}) => {
    const q = new URLSearchParams(Object.entries(f).filter(([, v]) => v) as [string, string][]);
    return call<Result[]>("GET", `/api/runs/${id}/results?${q}`);
  },
  review: (id: number, body: { review: string; note: string; correct: boolean | null }) => call<Result>("PATCH", `/api/results/${id}`, body),
};

export function exportURL(id: string, format: "md" | "csv", detail?: "results") {
  return `/api/runs/${id}/export?format=${format}${detail ? `&detail=${detail}` : ""}`;
}

export const pct = (v: number | null | undefined, digits = 0) => (v == null ? "-" : `${(100 * v).toFixed(digits)}%`);
export const secs = (ms: number) => `${(ms / 1000).toFixed(1)} s`;

export function usd(v: number | null | undefined, local = false) {
  if (v == null) return local ? "local" : "n/a";
  if (v > 0 && v < 0.01) return `$${v.toFixed(5)}`;
  return `$${v.toFixed(4)}`;
}

export function since(iso?: string) {
  if (!iso) return "";
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return new Date(iso).toLocaleDateString();
}
