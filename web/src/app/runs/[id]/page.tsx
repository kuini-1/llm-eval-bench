"use client";

import { useParams } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { api, exportURL, type Report } from "@/lib/api";
import { Badge, button, ErrorBox, ProgressBar, statusTone } from "@/components/ui";
import { Comparison } from "@/components/Comparison";
import { Outputs } from "@/components/Outputs";

const tabs = [
  { id: "compare", label: "Comparison" },
  { id: "outputs", label: "Outputs" },
  { id: "review", label: "Review queue" },
] as const;

export default function RunPage() {
  const { id } = useParams<{ id: string }>();
  const [report, setReport] = useState<Report | null>(null);
  const [active, setActive] = useState(false);
  const [tab, setTab] = useState<(typeof tabs)[number]["id"]>("compare");
  const [error, setError] = useState("");

  const load = useCallback(() => {
    api
      .run(id)
      .then((r) => {
        setReport(r.report);
        setActive(r.active);
      })
      .catch((e) => setError(e.message));
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  const running = active || report?.run.status === "running";
  useEffect(() => {
    if (!running) return;
    const t = setInterval(load, 2000);
    return () => clearInterval(t);
  }, [running, load]);

  if (error) return <ErrorBox message={error} />;
  if (!report) return <p className="text-sm text-muted">Loading...</p>;
  const { run, progress } = report;
  const took = run.started_at && run.finished_at ? Math.round((+new Date(run.finished_at) - +new Date(run.started_at)) / 1000) : null;
  const flagged = Object.values(report.flags ?? {}).reduce((a, b) => a + b, 0);

  async function act(fn: () => Promise<unknown>) {
    try {
      await fn();
      setTimeout(load, 300);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <div className="flex items-center gap-3">
            <h1 className="text-xl font-semibold">{run.name}</h1>
            <Badge tone={statusTone[run.status]}>{active && run.status === "done" ? "judging" : run.status}</Badge>
          </div>
          <p className="text-sm text-muted">
            {report.dataset.name} ({report.dataset.item_count} items) · {run.models.length} models · judged by {run.judge}
            {run.judge_samples > 1 ? `, ${run.judge_samples} verdicts per answer` : ""}
            {took != null ? ` · took ${took >= 60 ? `${Math.floor(took / 60)} min ${took % 60} s` : `${took} s`}` : ""}
          </p>
          {run.error && run.status === "failed" && <p className="text-sm text-bad">{run.error}</p>}
        </div>
        <div className="flex flex-wrap gap-2">
          {running && (
            <button className={button} onClick={() => act(() => api.cancel(id))}>
              Cancel
            </button>
          )}
          {!running && ["cancelled", "failed", "interrupted"].includes(run.status) && (
            <button className={button} onClick={() => act(() => api.resume(id))}>
              Resume
            </button>
          )}
          <a className={button} href={exportURL(id, "md")}>
            Markdown
          </a>
          <a className={button} href={exportURL(id, "csv")}>
            CSV summary
          </a>
          <a className={button} href={exportURL(id, "csv", "results")}>
            CSV, every answer
          </a>
        </div>
      </div>

      {(running || run.status !== "done") && (
        <div className="grid gap-4 sm:grid-cols-2">
          <ProgressBar value={progress.answered} max={progress.total} label={`answered ${progress.answered} of ${progress.total} (${progress.errors} errors)`} />
          <ProgressBar value={progress.judged} max={progress.total - progress.errors} label={`judged ${progress.judged} of ${progress.total - progress.errors}`} />
        </div>
      )}

      <div className="flex gap-1 border-b border-line">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`-mb-px border-b-2 px-3 py-2 text-sm ${tab === t.id ? "border-accent font-medium" : "border-transparent text-muted hover:text-ink"}`}
          >
            {t.label}
            {t.id === "review" && flagged > 0 && <span className="ml-1.5 text-xs text-muted">{flagged}</span>}
          </button>
        ))}
      </div>

      {tab === "compare" && <Comparison report={report} onRejudge={(judge) => act(() => api.rejudge(id, judge, 1))} busy={running} />}
      {tab === "outputs" && <Outputs runID={id} report={report} onChange={load} />}
      {tab === "review" && <Outputs runID={id} report={report} onChange={load} reviewQueue />}
    </div>
  );
}
