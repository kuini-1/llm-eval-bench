"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { api, ApiError, since, type Dataset, type ModelInfo, type RunSummary } from "@/lib/api";
import { Badge, button, Card, ErrorBox, input, primaryButton, ProgressBar, Section, statusTone } from "@/components/ui";

export default function RunsPage() {
  const [runs, setRuns] = useState<RunSummary[] | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(() => {
    api.runs().then(setRuns).catch((e) => setError(e.message));
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // Poll while something is running.
  const busy = runs?.some((r) => r.active || r.status === "running");
  useEffect(() => {
    if (!busy) return;
    const t = setInterval(load, 2000);
    return () => clearInterval(t);
  }, [busy, load]);

  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-xl font-semibold">Runs</h1>
        <p className="text-sm text-muted">Run one prompt dataset across several models, then compare accuracy, hallucination rate, speed and cost.</p>
      </div>
      <NewRun onStarted={load} />
      <Section title="Recent runs">
        <ErrorBox message={error} />
        {runs && runs.length === 0 && <p className="text-sm text-muted">No runs yet.</p>}
        <div className="space-y-2">
          {runs?.map((r) => (
            <Link key={r.id} href={`/runs/${r.id}`} className="block">
              <Card className="px-4 py-3 hover:border-accent">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <span className="font-medium">{r.name}</span>
                  <Badge tone={statusTone[r.status]}>{r.status}</Badge>
                  <span className="text-sm text-muted">
                    {r.models.join(" · ")} · judged by {r.judge}
                    {r.judge_samples > 1 ? ` ×${r.judge_samples}` : ""}
                  </span>
                  <span className="ml-auto text-xs text-muted">{since(r.created_at)}</span>
                </div>
                {r.status !== "done" && (
                  <div className="mt-2 grid gap-3 sm:grid-cols-2">
                    <ProgressBar value={r.progress.answered} max={r.progress.total} label={`answered ${r.progress.answered}/${r.progress.total}`} />
                    <ProgressBar value={r.progress.judged} max={r.progress.total - r.progress.errors} label={`judged ${r.progress.judged}/${r.progress.total - r.progress.errors}`} />
                  </div>
                )}
                {r.error && r.status === "failed" && <p className="mt-1 text-sm text-bad">{r.error}</p>}
              </Card>
            </Link>
          ))}
        </div>
      </Section>
    </div>
  );
}

function NewRun({ onStarted }: { onStarted: () => void }) {
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [datasets, setDatasets] = useState<Dataset[] | null>(null);
  const [picked, setPicked] = useState<string[]>([]);
  const [datasetID, setDatasetID] = useState("");
  const [judge, setJudge] = useState("");
  const [samples, setSamples] = useState(1);
  const [name, setName] = useState("");
  const [error, setError] = useState<{ message: string; details?: string[] }>({ message: "" });
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    Promise.all([api.models(), api.datasets()])
      .then(([m, d]) => {
        setModels(m);
        setDatasets(d);
        setDatasetID((cur) => cur || d[0]?.id || "");
        setJudge((cur) => cur || m.find((x) => x.default_judge)?.name || "");
      })
      .catch((e) => setError({ message: e.message }));
  }, []);

  const toggle = (n: string) => setPicked((p) => (p.includes(n) ? p.filter((x) => x !== n) : [...p, n]));

  async function start() {
    setBusy(true);
    setError({ message: "" });
    try {
      await api.createRun({ name, dataset_id: datasetID, models: picked, judge, judge_samples: samples });
      setPicked([]);
      setName("");
      onStarted();
    } catch (e) {
      setError({ message: (e as Error).message, details: e instanceof ApiError ? e.details : [] });
    } finally {
      setBusy(false);
    }
  }

  const judgeModel = models.find((m) => m.name === judge);
  return (
    <Section title="New run" hint="Models answer one after another; the judge grades everything once all answers are in.">
      <Card className="space-y-4 p-4">
        {datasets === null ? (
          <p className="text-sm text-muted">Loading...</p>
        ) : datasets.length === 0 ? (
          <p className="text-sm text-muted">
            Import a dataset first on the <Link href="/datasets" className="text-accent underline">Datasets</Link> page.
          </p>
        ) : (
          <>
            <div className="grid gap-4 sm:grid-cols-3">
              <label className="space-y-1 text-sm">
                <span className="text-muted">Dataset</span>
                <select className={`${input} w-full`} value={datasetID} onChange={(e) => setDatasetID(e.target.value)}>
                  {datasets.map((d) => (
                    <option key={d.id} value={d.id}>
                      {d.name} ({d.item_count} items)
                    </option>
                  ))}
                </select>
              </label>
              <label className="space-y-1 text-sm">
                <span className="text-muted">Judge</span>
                <select className={`${input} w-full`} value={judge} onChange={(e) => setJudge(e.target.value)}>
                  {models.map((m) => (
                    <option key={m.name} value={m.name} disabled={!m.ready}>
                      {m.name}
                      {m.ready ? "" : ` (${m.reason})`}
                    </option>
                  ))}
                </select>
              </label>
              <label className="space-y-1 text-sm">
                <span className="text-muted">Verdicts per answer</span>
                <select className={`${input} w-full`} value={samples} onChange={(e) => setSamples(Number(e.target.value))}>
                  {[1, 2, 3, 5].map((n) => (
                    <option key={n} value={n}>
                      {n === 1 ? "1" : `${n} (majority vote, measures consistency)`}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <div className="space-y-1 text-sm">
              <span className="text-muted">Models to compare</span>
              <div className="flex flex-wrap gap-2">
                {models.map((m) => (
                  <button
                    key={m.name}
                    disabled={!m.ready}
                    onClick={() => toggle(m.name)}
                    title={m.ready ? `${m.provider} · ${m.model}` : m.reason}
                    className={`rounded-md border px-3 py-1.5 text-sm disabled:cursor-not-allowed disabled:opacity-40 ${
                      picked.includes(m.name) ? "border-accent bg-accent text-accent-ink" : "border-line bg-surface hover:bg-sunken"
                    }`}
                  >
                    {m.name}
                    <span className="ml-1.5 text-xs opacity-70">{m.local ? "local" : m.provider}</span>
                  </button>
                ))}
              </div>
              {judgeModel && picked.includes(judgeModel.name) && (
                <p className="text-xs text-warn">The judge is also a candidate, so it will grade its own answers. The report shows whether it favours them.</p>
              )}
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <input className={`${input} w-72`} placeholder="Run name (optional)" value={name} onChange={(e) => setName(e.target.value)} />
              <button className={primaryButton} disabled={busy || picked.length === 0 || !datasetID || !judge} onClick={start}>
                {busy ? "Starting..." : picked.length === 0 ? "Pick models to run" : `Run ${picked.length} model${picked.length === 1 ? "" : "s"}`}
              </button>
              <button className={button} onClick={() => setPicked(models.filter((m) => m.ready).map((m) => m.name))}>
                Select all available
              </button>
            </div>
          </>
        )}
        <ErrorBox message={error.message} details={error.details} />
      </Card>
    </Section>
  );
}
