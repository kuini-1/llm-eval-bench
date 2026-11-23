"use client";

import { useEffect, useState } from "react";
import { api, ApiError, since, type Dataset, type Item } from "@/lib/api";
import { Badge, Card, ErrorBox, input, primaryButton, Section } from "@/components/ui";

export default function DatasetsPage() {
  const [datasets, setDatasets] = useState<Dataset[] | null>(null);
  const [open, setOpen] = useState<string>("");
  const [error, setError] = useState("");

  const load = () => api.datasets().then(setDatasets).catch((e) => setError(e.message));
  useEffect(() => {
    load();
  }, []);

  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-xl font-semibold">Datasets</h1>
        <p className="text-sm text-muted">
          JSONL or CSV, one prompt per line or row. Each item says how its answer is checked: in code (exact, number, choice, contains, regex,
          json), by the judge, or as a hallucination probe (abstain).
        </p>
      </div>
      <Import onImported={load} />
      <Section title="Imported">
        <ErrorBox message={error} />
        {datasets?.length === 0 && <p className="text-sm text-muted">Nothing imported yet.</p>}
        <div className="space-y-2">
          {datasets?.map((d) => (
            <Card key={d.id}>
              <button className="flex w-full flex-wrap items-center gap-3 px-4 py-3 text-left" onClick={() => setOpen(open === d.id ? "" : d.id)}>
                <span className="font-medium">{d.name}</span>
                <span className="text-sm text-muted">{d.item_count} items</span>
                {d.description && <span className="text-sm text-muted">· {d.description}</span>}
                <span className="font-mono text-xs text-muted" title="SHA-256 of the items">
                  {d.sha256.slice(0, 12)}
                </span>
                <span className="ml-auto text-xs text-muted">{since(d.created_at)}</span>
              </button>
              {open === d.id && <Items id={d.id} />}
            </Card>
          ))}
        </div>
      </Section>
    </div>
  );
}

function Import({ onImported }: { onImported: () => void }) {
  const [file, setFile] = useState<File | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState("");
  const [error, setError] = useState<{ message: string; details?: string[] }>({ message: "" });

  async function submit() {
    if (!file) return;
    setBusy(true);
    setDone("");
    setError({ message: "" });
    const form = new FormData();
    form.set("file", file);
    form.set("name", name);
    form.set("description", description);
    try {
      const d = await api.importDataset(form);
      setDone(`Imported ${d.item_count} items as "${d.id}".`);
      setFile(null);
      setName("");
      setDescription("");
      onImported();
    } catch (e) {
      setError({ message: (e as Error).message, details: e instanceof ApiError ? e.details : [] });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Section title="Import" hint="Every item is validated. If any item is invalid, nothing is imported and every problem is listed.">
      <Card className="space-y-3 p-4">
        <div className="flex flex-wrap items-center gap-3">
          <input type="file" accept=".jsonl,.json,.csv" onChange={(e) => setFile(e.target.files?.[0] ?? null)} className="text-sm" />
          <input className={`${input} w-56`} placeholder="Name (default: file name)" value={name} onChange={(e) => setName(e.target.value)} />
          <input className={`${input} w-72`} placeholder="Description" value={description} onChange={(e) => setDescription(e.target.value)} />
          <button className={primaryButton} disabled={!file || busy} onClick={submit}>
            {busy ? "Importing..." : "Import"}
          </button>
        </div>
        {done && <p className="text-sm text-ok">{done}</p>}
        <ErrorBox message={error.message} details={error.details} />
      </Card>
    </Section>
  );
}

function Items({ id }: { id: string }) {
  const [items, setItems] = useState<Item[] | null>(null);
  const [cats, setCats] = useState<Record<string, number>>({});
  useEffect(() => {
    api.dataset(id).then((r) => {
      setItems(r.items);
      setCats(r.dataset.categories ?? {});
    });
  }, [id]);
  if (!items) return <p className="px-4 pb-3 text-sm text-muted">Loading...</p>;
  return (
    <div className="border-t border-line">
      <div className="flex flex-wrap gap-2 px-4 py-2">
        {Object.entries(cats).map(([c, n]) => (
          <Badge key={c}>
            {c} {n}
          </Badge>
        ))}
      </div>
      <div className="max-h-[32rem] overflow-auto">
        <table className="w-full text-sm">
          <thead className="sticky top-0 bg-sunken text-left text-xs text-muted">
            <tr>
              <th className="px-4 py-1.5 font-medium">ID</th>
              <th className="px-2 py-1.5 font-medium">Grader</th>
              <th className="px-2 py-1.5 font-medium">Prompt</th>
              <th className="px-4 py-1.5 font-medium">Reference</th>
            </tr>
          </thead>
          <tbody>
            {items.map((it) => (
              <tr key={it.id} className="border-t border-line align-top">
                <td className="px-4 py-1.5 font-mono text-xs whitespace-nowrap">{it.id}</td>
                <td className="px-2 py-1.5 whitespace-nowrap">
                  <Badge tone={it.grader.type === "judge" ? "warn" : it.grader.type === "abstain" ? "bad" : "neutral"}>{it.grader.type}</Badge>
                </td>
                <td className="max-w-md px-2 py-1.5">
                  <div className="line-clamp-2">{it.prompt}</div>
                  {it.context && <div className="text-xs text-muted">with context ({it.context.length} chars)</div>}
                </td>
                <td className="max-w-xs px-4 py-1.5 text-muted">
                  <div className="line-clamp-2">{it.reference || it.rubric}</div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
