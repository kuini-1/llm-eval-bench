"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const links = [
  { href: "/", label: "Runs" },
  { href: "/datasets", label: "Datasets" },
];

export function Nav() {
  const path = usePathname();
  const active = (href: string) => (href === "/" ? path === "/" || path.startsWith("/runs") : path.startsWith(href));
  return (
    <header className="border-b border-line bg-surface">
      <div className="mx-auto flex h-14 max-w-7xl items-center gap-6 px-4 sm:px-6">
        <Link href="/" className="font-semibold tracking-tight">
          Eval Bench
        </Link>
        <nav className="flex gap-1 text-sm">
          {links.map((l) => (
            <Link
              key={l.href}
              href={l.href}
              className={`rounded-md px-3 py-1.5 ${active(l.href) ? "bg-sunken font-medium" : "text-muted hover:text-ink"}`}
            >
              {l.label}
            </Link>
          ))}
        </nav>
        <a href="/openapi.yaml" className="ml-auto text-sm text-muted hover:text-ink" title="API reference">
          API
        </a>
      </div>
    </header>
  );
}
