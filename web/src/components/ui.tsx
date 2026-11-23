import type { ReactNode } from "react";

export function Badge({ tone = "neutral", children, title }: { tone?: "ok" | "bad" | "warn" | "neutral" | "accent"; children: ReactNode; title?: string }) {
  const tones = {
    ok: "bg-ok-bg text-ok",
    bad: "bg-bad-bg text-bad",
    warn: "bg-warn-bg text-warn",
    neutral: "bg-sunken text-muted",
    accent: "bg-accent text-accent-ink",
  };
  return (
    <span title={title} className={`inline-flex items-center rounded px-1.5 py-0.5 text-xs font-medium whitespace-nowrap ${tones[tone]}`}>
      {children}
    </span>
  );
}

export function Verdict({ value, labels = ["correct", "wrong"] }: { value: boolean | null | undefined; labels?: [string, string] }) {
  if (value == null) return <Badge>unscored</Badge>;
  return value ? <Badge tone="ok">{labels[0]}</Badge> : <Badge tone="bad">{labels[1]}</Badge>;
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`rounded-lg border border-line bg-surface ${className}`}>{children}</div>;
}

export function Section({ title, hint, children, action }: { title: string; hint?: ReactNode; children: ReactNode; action?: ReactNode }) {
  return (
    <section className="space-y-3">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h2 className="font-semibold">{title}</h2>
          {hint && <p className="text-sm text-muted">{hint}</p>}
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

export function ProgressBar({ value, max, label }: { value: number; max: number; label?: string }) {
  const p = max > 0 ? Math.min(100, (100 * value) / max) : 0;
  return (
    <div>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-sunken">
        <div className="h-full rounded-full bg-accent transition-all" style={{ width: `${p}%` }} />
      </div>
      {label && <div className="mt-1 text-xs text-muted num">{label}</div>}
    </div>
  );
}

// An accuracy bar with its 95% interval drawn behind it.
export function AccuracyBar({ value, low, high }: { value: number; low: number; high: number }) {
  return (
    <div className="relative h-2 w-28 rounded-full bg-sunken" title={`95% CI ${(100 * low).toFixed(0)}-${(100 * high).toFixed(0)}%`}>
      <div className="absolute inset-y-0 rounded-full bg-accent/25" style={{ left: `${100 * low}%`, width: `${100 * (high - low)}%` }} />
      <div className="absolute inset-y-0 left-0 rounded-full bg-accent" style={{ width: `${100 * value}%`, opacity: 0.85 }} />
    </div>
  );
}

export const button =
  "rounded-md border border-line bg-surface px-3 py-1.5 text-sm hover:bg-sunken disabled:cursor-not-allowed disabled:opacity-50";
export const primaryButton = "rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink hover:opacity-90 disabled:opacity-50";
export const input = "rounded-md border border-line bg-surface px-2.5 py-1.5 text-sm outline-none focus:border-accent";

export function ErrorBox({ message, details }: { message: string; details?: string[] }) {
  if (!message) return null;
  return (
    <div className="rounded-md border border-bad/30 bg-bad-bg px-3 py-2 text-sm text-bad">
      <div>{message}</div>
      {details && details.length > 0 && (
        <ul className="mt-1 list-disc pl-5 font-mono text-xs">
          {details.map((d) => (
            <li key={d}>{d}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

export const statusTone: Record<string, "ok" | "bad" | "warn" | "neutral" | "accent"> = {
  done: "ok",
  running: "accent",
  queued: "neutral",
  failed: "bad",
  cancelled: "warn",
  interrupted: "warn",
};
