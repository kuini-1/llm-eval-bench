"use client";

import { useEffect, useState } from "react";
import { api, pct, secs, usd, type ModelInfo, type Report } from "@/lib/api";
import { AccuracyBar, button, Card, input, Section } from "@/components/ui";

const th = "px-3 py-2 text-left text-xs font-medium text-muted whitespace-nowrap";
const td = "px-3 py-2 whitespace-nowrap num";

function kappa(v: number | null) {
  return v == null ? "-" : v.toFixed(2);
}

// Background shade for an accuracy cell: from bad (0%) to ok (100%).
function heat(v: number) {
  if (v >= 0.8) return "bg-ok-bg";
  if (v >= 0.5) return "bg-warn-bg";
  return "bg-bad-bg";
}

export function Comparison({ report, onRejudge, busy }: { report: Report; onRejudge: (judge: string) => void; busy: boolean }) {
  const { models, categories, judges } = report;
  return (
    <div className="space-y-8">
      <Section
        title="Results"
        hint="Accuracy counts code-graded items by their code grader and everything else by the judge. The shaded band is the 95% confidence interval: when two models' bands overlap, the gap may be noise."
      >
        <Card className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-sunken">
              <tr>
                <th className={th}>Model</th>
                <th className={th}>Accuracy</th>
                <th className={th} />
                <th className={th}>Judge score</th>
                <th className={th}>Hallucination rate</th>
                <th className={th}>p50 / p95 latency</th>
                <th className={th}>Output tok/s</th>
                <th className={th}>Cost</th>
                <th className={th}>Per correct</th>
                <th className={th}>Errors</th>
              </tr>
            </thead>
            <tbody>
              {models.map((m) => (
                <tr key={m.model} className="border-t border-line">
                  <td className="px-3 py-2">
                    <div className="font-medium">{m.model}</div>
                    <div className="text-xs text-muted">
                      {m.provider} · {m.model_id}
                    </div>
                  </td>
                  {m.scored === 0 ? (
                    <td className={`${td} text-muted`} colSpan={2}>
                      nothing scored yet
                    </td>
                  ) : (
                    <>
                      <td className={td}>
                        <span className="font-medium">{pct(m.accuracy)}</span>{" "}
                        <span className="text-xs text-muted">
                          {m.correct}/{m.scored} · {pct(m.ci_low)}-{pct(m.ci_high)}
                        </span>
                      </td>
                      <td className="px-3 py-2">
                        <AccuracyBar value={m.accuracy} low={m.ci_low} high={m.ci_high} />
                      </td>
                    </>
                  )}
                  <td className={td}>{m.mean_judge_score?.toFixed(2) ?? "-"}</td>
                  <td className={td}>
                    {m.hallucination_rate == null ? (
                      "-"
                    ) : (
                      <>
                        {pct(m.hallucination_rate)}{" "}
                        <span className="text-xs text-muted">
                          {m.hallucinated}/{m.probes}
                        </span>
                      </>
                    )}
                  </td>
                  <td className={td}>
                    {secs(m.latency_p50_ms)} / {secs(m.latency_p95_ms)}
                  </td>
                  <td className={td}>{m.output_tokens_per_sec?.toFixed(0) ?? "-"}</td>
                  <td className={td}>{usd(m.cost_usd, m.local)}</td>
                  <td className={td}>{usd(m.cost_per_correct_usd, m.local)}</td>
                  <td className={td}>{m.errors}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      </Section>

      {categories.length > 0 && (
        <Section title="Accuracy by category">
          <Card className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-sunken">
                <tr>
                  <th className={th}>Category</th>
                  <th className={th}>Items</th>
                  {models.map((m) => (
                    <th key={m.model} className={th}>
                      {m.model}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {categories.map((c) => (
                  <tr key={c.category} className="border-t border-line">
                    <td className="px-3 py-2">{c.category}</td>
                    <td className={td}>{c.items}</td>
                    {models.map((m) => {
                      const cell = c.cells?.[m.model];
                      if (!cell || cell.scored === 0) return <td key={m.model} className={td}>-</td>;
                      const v = cell.correct / cell.scored;
                      return (
                        <td key={m.model} className={`${td} ${heat(v)}`}>
                          {pct(v)}{" "}
                          <span className="text-xs text-muted">
                            {cell.correct}/{cell.scored}
                          </span>
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
        </Section>
      )}

      <Section
        title="How far to trust the judge"
        hint="On items with an objective answer, the code grader is ground truth, so the judge's mistakes there are measurable. Kappa corrects for agreement by chance: 1 is perfect, 0 is no better than chance."
        action={<Rejudge report={report} onRejudge={onRejudge} busy={busy} />}
      >
        <Card className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-sunken">
              <tr>
                <th className={th}>Judge</th>
                <th className={th}>Agrees with code grader</th>
                <th className={th}>Passed wrong answers</th>
                <th className={th}>Failed right answers</th>
                <th className={th}>Kappa</th>
                <th className={th}>Consistency</th>
                <th className={th}>Unreadable verdicts</th>
                <th className={th}>Cost</th>
              </tr>
            </thead>
            <tbody>
              {judges.map((j) => (
                <tr key={j.judge} className="border-t border-line">
                  <td className="px-3 py-2 font-medium">
                    {j.judge}
                    {j.primary && <span className="ml-1.5 text-xs font-normal text-muted">primary</span>}
                  </td>
                  <td className={td}>
                    {pct(j.vs_code_grader.rate)}{" "}
                    <span className="text-xs text-muted">
                      {j.vs_code_grader.agree}/{j.vs_code_grader.n}
                    </span>
                  </td>
                  <td className={td}>{j.vs_code_grader.false_pass}</td>
                  <td className={td}>{j.vs_code_grader.false_fail}</td>
                  <td className={td}>{kappa(j.vs_code_grader.kappa)}</td>
                  <td className={td} title="Share of answers where every verdict agreed">
                    {j.consistency == null ? "1 verdict each" : `${pct(j.consistency)} (${j.split} split)`}
                  </td>
                  <td className={td}>{j.errors}</td>
                  <td className={td}>{usd(j.cost_usd, j.local)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
        {judges.map((j) =>
          Object.keys(j.by_model ?? {}).length ? (
            <div key={j.judge} className="space-y-2">
              <p className="text-sm text-muted">{j.judge}: agreement with the code grader, by whose answers it was grading</p>
              <div className="flex flex-wrap gap-2">
                {Object.entries(j.by_model)
                  .sort(([a], [b]) => a.localeCompare(b))
                  .map(([m, a]) => (
                    <Card key={m} className="px-3 py-2 text-sm">
                      <div className="font-medium">
                        {m}
                        {m === j.judge && <span className="ml-1 text-xs text-warn">itself</span>}
                      </div>
                      <div className="num text-xs text-muted">
                        {pct(a.rate)} agree · passed {a.false_pass} wrong · failed {a.false_fail} right
                      </div>
                    </Card>
                  ))}
              </div>
            </div>
          ) : null,
        )}
        {report.judge_pairs && report.judge_pairs.length > 0 && (
          <Card className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-sunken">
                <tr>
                  <th className={th}>Judges compared</th>
                  <th className={th}>Same verdict</th>
                  <th className={th}>Kappa</th>
                </tr>
              </thead>
              <tbody>
                {report.judge_pairs.map((p) => (
                  <tr key={p.judges.join()} className="border-t border-line">
                    <td className="px-3 py-2">{p.judges.join(" vs ")}</td>
                    <td className={td}>
                      {pct(p.rate)}{" "}
                      <span className="text-xs text-muted">
                        {p.agree}/{p.n}
                      </span>
                    </td>
                    <td className={td}>{kappa(p.kappa)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
        )}
      </Section>
    </div>
  );
}

function Rejudge({ report, onRejudge, busy }: { report: Report; onRejudge: (judge: string) => void; busy: boolean }) {
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [judge, setJudge] = useState("");
  useEffect(() => {
    api.models().then(setModels);
  }, []);
  const used = new Set(report.judges.map((j) => j.judge));
  const options = models.filter((m) => m.ready && !used.has(m.name));
  if (report.run.status !== "done" || options.length === 0) return null;
  return (
    <div className="flex items-center gap-2">
      <select className={input} value={judge} onChange={(e) => setJudge(e.target.value)}>
        <option value="">Second judge...</option>
        {options.map((m) => (
          <option key={m.name} value={m.name}>
            {m.name}
          </option>
        ))}
      </select>
      <button className={button} disabled={!judge || busy} onClick={() => onRejudge(judge)}>
        Re-judge
      </button>
    </div>
  );
}
