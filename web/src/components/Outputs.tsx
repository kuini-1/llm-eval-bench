"use client";

import { useEffect, useMemo, useState } from "react";
import { api, type Item, type Report, type Result, type ResultFilter } from "@/lib/api";
import { Badge, button, Card, ErrorBox, input, Verdict } from "@/components/ui";

const flagHelp: Record<string, string> = {
  judge_disagrees: "The judge and the code grader disagree. The code grader decides; check whether it was too strict or the judge too lenient.",
  heuristic_disagrees: "On a hallucination probe, the phrase check and the judge disagree about whether the model declined.",
  hallucination: "The judge says the answer states false or unsupported facts.",
  judge_split: "The judge's repeated verdicts on this answer disagreed.",
  unjudged: "The judge's reply couldn't be read, so an open-ended answer has no score.",
  truncated: "The answer hit the token limit.",
  error: "The call failed or the model refused; it counts as wrong.",
};

const PAGE = 30;

export function Outputs({ runID, report, onChange, reviewQueue = false }: { runID: string; report: Report; onChange: () => void; reviewQueue?: boolean }) {
  const [items, setItems] = useState<Record<string, Item>>({});
  const [results, setResults] = useState<Result[] | null>(null);
  const [filter, setFilter] = useState<ResultFilter>(reviewQueue ? { flag: "any", review: "unreviewed" } : {});
  const [shown, setShown] = useState(PAGE);
  const [error, setError] = useState("");

  useEffect(() => {
    api.dataset(report.dataset.id).then((d) => setItems(Object.fromEntries(d.items.map((it) => [it.id, it]))));
  }, [report.dataset.id]);

  useEffect(() => {
    api
      .results(runID, filter)
      .then((r) => {
        setResults(r);
        setShown(PAGE);
      })
      .catch((e) => setError(e.message));
  }, [runID, filter]);

  const groups = useMemo(() => {
    const out: { itemID: string; results: Result[] }[] = [];
    for (const r of results ?? []) {
      const last = out[out.length - 1];
      if (last && last.itemID === r.item_id) last.results.push(r);
      else out.push({ itemID: r.item_id, results: [r] });
    }
    return out;
  }, [results]);

  const set = (k: keyof ResultFilter, v: string) => setFilter((f) => ({ ...f, [k]: v }));
  const replace = (r: Result) => {
    setResults((rs) => rs?.map((x) => (x.id === r.id ? r : x)) ?? null);
    onChange();
  };

  return (
    <div className="space-y-4">
      {reviewQueue && (
        <p className="text-sm text-muted">
          Answers the pipeline flagged and nobody has reviewed yet. Tag each one, and override the verdict where the grader or judge got it
          wrong. Overrides change the accuracy figures.
        </p>
      )}
      <div className="flex flex-wrap gap-2 text-sm">
        <select className={input} value={filter.category ?? ""} onChange={(e) => set("category", e.target.value)}>
          <option value="">All categories</option>
          {report.categories.map((c) => (
            <option key={c.category} value={c.category}>
              {c.category}
            </option>
          ))}
        </select>
        <select className={input} value={filter.model ?? ""} onChange={(e) => set("model", e.target.value)}>
          <option value="">All models</option>
          {report.run.models.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
        <select className={input} value={filter.flag ?? ""} onChange={(e) => set("flag", e.target.value)}>
          <option value="">Any or no flag</option>
          <option value="any">Flagged</option>
          {Object.keys(report.flags ?? {}).map((f) => (
            <option key={f} value={f}>
              {f} ({report.flags[f]})
            </option>
          ))}
        </select>
        <select className={input} value={filter.correct ?? ""} onChange={(e) => set("correct", e.target.value)}>
          <option value="">Correct or not</option>
          <option value="true">Correct</option>
          <option value="false">Wrong</option>
        </select>
        <select className={input} value={filter.review ?? ""} onChange={(e) => set("review", e.target.value)}>
          <option value="">Reviewed or not</option>
          <option value="unreviewed">Not reviewed</option>
          <option value="reviewed">Reviewed</option>
          <option value="bad">Tagged bad</option>
          <option value="ok">Tagged ok</option>
        </select>
        <span className="self-center text-muted">
          {results ? `${results.length} answers to ${groups.length} items` : "Loading..."}
        </span>
      </div>
      <ErrorBox message={error} />
      {results?.length === 0 && <p className="text-sm text-muted">{reviewQueue ? "Nothing left to review." : "No answers match."}</p>}
      {groups.slice(0, shown).map((g) => (
        <ItemGroup key={g.itemID} item={items[g.itemID]} itemID={g.itemID} results={g.results} onSaved={replace} />
      ))}
      {groups.length > shown && (
        <button className={button} onClick={() => setShown((n) => n + PAGE)}>
          Show more ({groups.length - shown} items left)
        </button>
      )}
    </div>
  );
}

function ItemGroup({ item, itemID, results, onSaved }: { item?: Item; itemID: string; results: Result[]; onSaved: (r: Result) => void }) {
  const [showContext, setShowContext] = useState(false);
  return (
    <Card>
      <div className="space-y-2 border-b border-line px-4 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-xs text-muted">{itemID}</span>
          {item && <Badge>{item.category}</Badge>}
          {item && <Badge tone={item.grader.type === "judge" ? "warn" : item.grader.type === "abstain" ? "bad" : "neutral"}>{item.grader.type}</Badge>}
          {item?.hallucination_probe && <Badge tone="bad">hallucination probe</Badge>}
        </div>
        {item && (
          <>
            <p className="text-sm whitespace-pre-wrap">{item.prompt}</p>
            {item.context && (
              <button className="text-xs text-accent" onClick={() => setShowContext((s) => !s)}>
                {showContext ? "Hide context" : "Show context"}
              </button>
            )}
            {showContext && <p className="rounded bg-sunken px-3 py-2 text-xs whitespace-pre-wrap text-muted">{item.context}</p>}
            {(item.reference || item.rubric) && (
              <p className="text-xs text-muted">
                <span className="font-medium">Reference:</span> {item.reference}
                {item.rubric && (
                  <>
                    {" "}
                    <span className="font-medium">Notes:</span> {item.rubric}
                  </>
                )}
              </p>
            )}
          </>
        )}
      </div>
      <div className="grid gap-px bg-line md:grid-cols-2 xl:grid-cols-3">
        {results.map((r) => (
          <Answer key={r.id} r={r} onSaved={onSaved} />
        ))}
      </div>
    </Card>
  );
}

function Answer({ r, onSaved }: { r: Result; onSaved: (r: Result) => void }) {
  const [note, setNote] = useState(r.review_note);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  async function save(review: Result["review"], correct: boolean | null) {
    setSaving(true);
    setError("");
    try {
      onSaved(await api.review(r.id, { review, note, correct }));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="space-y-2 bg-surface p-4">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{r.model}</span>
        <Verdict value={r.correct} />
        {r.review_correct != null && <Badge tone="accent">overridden</Badge>}
        {r.review === "bad" && <Badge tone="bad">tagged bad</Badge>}
        {r.review === "ok" && <Badge tone="ok">tagged ok</Badge>}
        <span className="ml-auto text-xs text-muted num">
          {(r.latency_ms / 1000).toFixed(1)} s · {r.output_tokens} tok
        </span>
      </div>
      {r.error ? (
        <p className="text-sm text-bad">{r.error}</p>
      ) : (
        <pre className="max-h-64 overflow-auto rounded bg-sunken px-3 py-2 font-sans text-sm whitespace-pre-wrap">{r.output || "(empty)"}</pre>
      )}
      <div className="space-y-1 text-xs">
        {r.grader_pass != null && (
          <div className="flex gap-2">
            <span className="w-14 shrink-0 text-muted">Code</span>
            <span>
              <Verdict value={r.grader_pass} labels={["pass", "fail"]} /> <span className="text-muted">{r.grader_detail}</span>
            </span>
          </div>
        )}
        {r.judge_pass != null && (
          <div className="flex gap-2">
            <span className="w-14 shrink-0 text-muted">Judge</span>
            <span>
              <Verdict value={r.judge_pass} labels={["pass", "fail"]} /> <span className="num">{r.judge_score?.toFixed(1)}/5</span>
              {r.judge_hallucination && (
                <>
                  {" "}
                  <Badge tone="bad">hallucination</Badge>
                </>
              )}
              {r.judge_agreement != null && r.judge_agreement < 1 && <span className="text-warn"> · verdicts split</span>}
              <span className="block text-muted">{r.judge_reasoning}</span>
            </span>
          </div>
        )}
      </div>
      {r.flags.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {r.flags.map((f) => (
            <Badge key={f} tone="warn" title={flagHelp[f]}>
              {f}
            </Badge>
          ))}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-1.5 border-t border-line pt-2">
        <input className={`${input} min-w-0 flex-1 py-1 text-xs`} placeholder="Note" value={note} onChange={(e) => setNote(e.target.value)} />
        <button className={`${button} py-1 text-xs`} disabled={saving} onClick={() => save("bad", false)} title="Tag bad and count it as wrong">
          Bad
        </button>
        <button className={`${button} py-1 text-xs`} disabled={saving} onClick={() => save("ok", null)} title="Tag ok and keep the automatic verdict">
          OK
        </button>
        <button className={`${button} py-1 text-xs`} disabled={saving} onClick={() => save("ok", true)} title="Tag ok and count it as correct">
          OK, correct
        </button>
        {(r.review || r.review_correct != null) && (
          <button className={`${button} py-1 text-xs`} disabled={saving} onClick={() => save("", null)}>
            Clear
          </button>
        )}
      </div>
      {error && <p className="text-xs text-bad">{error}</p>}
    </div>
  );
}
