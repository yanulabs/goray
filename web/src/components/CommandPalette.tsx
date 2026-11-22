import { Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { isTestOnlyCard } from "../lib/derive";
import { fuzzyMatch, highlightSegments } from "../lib/fuzzy";
import { formatInt } from "../lib/format";
import type { Report } from "../types/report";
import { Kbd, KindBadge } from "./ui";

interface Candidate {
  id: string;
  name: string;
  detail: string;
  kind: string;
  searchText: string;
}

interface ScoredCandidate {
  candidate: Candidate;
  score: number;
  indices: number[];
}

function buildCandidates(report: Report): Candidate[] {
  const candidates: Candidate[] = [];
  const seen = new Set<string>();
  const push = (candidate: Candidate) => {
    if (seen.has(candidate.id)) {
      return;
    }
    seen.add(candidate.id);
    candidates.push(candidate);
  };

  for (const module of report.modules) {
    push({
      id: module.id,
      name: module.name,
      detail: isTestOnlyCard(module)
        ? `${formatInt(module.packages)} pkgs · ${formatInt(module.test_loc)} test loc`
        : `${formatInt(module.packages)} pkgs · ${formatInt(module.prod_loc)} loc`,
      kind: "module",
      searchText: `${module.name} module${isTestOnlyCard(module) ? " test-only" : ""}`,
    });
  }

  for (const pkg of report.packages) {
    push({
      id: pkg.id,
      name: pkg.rel_path === "." ? pkg.pkg_path : pkg.rel_path,
      detail: isTestOnlyCard(pkg)
        ? `${pkg.pkg_path} · ${formatInt(pkg.test_loc)} test loc`
        : pkg.pkg_path,
      kind: "package",
      searchText: `${pkg.rel_path} ${pkg.pkg_path} package ${pkg.module}${
        isTestOnlyCard(pkg) ? " test-only" : ""
      }`,
    });
  }

  for (const symbol of report.symbols) {
    if (symbol.kind === "file") {
      push({
        id: symbol.id,
        name: symbol.file ?? symbol.name,
        detail: symbol.module ?? "",
        kind: "file",
        searchText: `${symbol.file ?? symbol.name} file ${symbol.module ?? ""}`,
      });
    }
  }

  for (const symbol of report.hotspots) {
    push({
      id: symbol.id,
      name: symbol.name,
      detail: `${symbol.kind} · cc ${symbol.complexity} · fan-in ${symbol.fan_in}`,
      kind: symbol.kind,
      searchText: `${symbol.name} ${symbol.file ?? ""} ${symbol.pkg_path ?? ""} hot spot risk`,
    });
  }

  for (const symbol of report.symbols) {
    if (
      symbol.kind === "module" ||
      symbol.kind === "package" ||
      symbol.kind === "file" ||
      symbol.kind === "interface"
    ) {
      continue;
    }
    push({
      id: symbol.id,
      name: symbol.name,
      detail: `${symbol.kind} · ${symbol.pkg_path ?? ""}`,
      kind: symbol.kind,
      searchText: `${symbol.name} ${symbol.file ?? ""} ${symbol.pkg_path ?? ""} ${symbol.module ?? ""} ${symbol.kind}`,
    });
  }

  for (const iface of report.interfaces) {
    push({
      id: iface.id,
      name: iface.name,
      detail: `${formatInt(iface.implementations.length)} impl · ${iface.pkg_path}`,
      kind: "interface",
      searchText: `${iface.name} ${iface.pkg_path} interface ${iface.module}`,
    });
  }

  return candidates;
}

function Highlighted({ text, indices }: { text: string; indices: number[] }) {
  if (indices.length === 0) {
    return <>{text}</>;
  }
  return (
    <>
      {highlightSegments(text, indices).map((segment, index) =>
        segment.match ? (
          <span
            key={index}
            className="rounded-sm bg-indigo-100 text-indigo-800 dark:bg-indigo-950 dark:text-indigo-200"
          >
            {segment.text}
          </span>
        ) : (
          <span key={index}>{segment.text}</span>
        ),
      )}
    </>
  );
}

export function CommandPalette({
  open,
  report,
  onClose,
  onSelect,
}: {
  open: boolean;
  report: Report;
  onClose: () => void;
  onSelect: (id: string) => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const candidates = useMemo(() => buildCandidates(report), [report]);

  const matches = useMemo<ScoredCandidate[]>(() => {
    const needle = query.trim();
    if (needle === "") {
      return candidates.slice(0, 60).map((candidate) => ({ candidate, score: 0, indices: [] }));
    }
    const scored: ScoredCandidate[] = [];
    for (const candidate of candidates) {
      const nameMatch = fuzzyMatch(needle, candidate.name);
      const textMatch = nameMatch ?? fuzzyMatch(needle, candidate.searchText);
      if (!textMatch) {
        continue;
      }
      scored.push({
        candidate,
        score: textMatch.score + (nameMatch ? 24 : 0),
        indices: nameMatch?.indices ?? [],
      });
    }
    scored.sort((left, right) => {
      if (right.score !== left.score) {
        return right.score - left.score;
      }
      return left.candidate.name.localeCompare(right.candidate.name);
    });
    return scored.slice(0, 60);
  }, [candidates, query]);

  useEffect(() => {
    if (!open) {
      return;
    }
    setQuery("");
    setActive(0);
    const frame = window.requestAnimationFrame(() => inputRef.current?.focus());
    return () => window.cancelAnimationFrame(frame);
  }, [open]);

  useEffect(() => {
    setActive(0);
  }, [query]);

  useEffect(() => {
    if (active >= matches.length) {
      setActive(Math.max(0, matches.length - 1));
    }
  }, [active, matches.length]);

  useEffect(() => {
    const list = listRef.current;
    if (!list) {
      return;
    }
    const element = list.querySelector('[data-active="true"]');
    if (element instanceof HTMLElement) {
      element.scrollIntoView({ block: "nearest" });
    }
  }, [active, matches.length]);

  if (!open) {
    return null;
  }

  const select = (index: number) => {
    const match = matches[index];
    if (match) {
      onSelect(match.candidate.id);
      onClose();
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-zinc-950/30 px-4 pt-[10vh]"
      role="dialog"
      aria-modal="true"
      aria-label="Search the codebase"
      tabIndex={-1}
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) {
          onClose();
        }
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          onClose();
          return;
        }
        if (event.key === "ArrowDown") {
          event.preventDefault();
          setActive((current) => (matches.length === 0 ? 0 : (current + 1) % matches.length));
          return;
        }
        if (event.key === "ArrowUp") {
          event.preventDefault();
          setActive((current) =>
            matches.length === 0 ? 0 : (current - 1 + matches.length) % matches.length,
          );
          return;
        }
        if (event.key === "Enter") {
          event.preventDefault();
          select(active);
        }
      }}
    >
      <div className="w-full max-w-2xl overflow-hidden rounded border border-zinc-300 bg-white dark:border-zinc-700 dark:bg-zinc-950">
        <div className="flex h-11 items-center gap-2 border-b border-zinc-200 px-3 dark:border-zinc-800">
          <Search size={14} className="shrink-0 text-zinc-400" />
          <input
            ref={inputRef}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search modules, packages, files, symbols, interfaces…"
            aria-label="Search query"
            spellCheck={false}
            autoComplete="off"
            className="h-full min-w-0 flex-1 bg-transparent font-mono text-[12px] text-zinc-800 placeholder:text-zinc-400 focus:outline-none dark:text-zinc-100 dark:placeholder:text-zinc-600"
          />
          <Kbd>Esc</Kbd>
        </div>

        <div ref={listRef} className="max-h-[55vh] overflow-y-auto py-1">
          {matches.length === 0 ? (
            <div className="px-3 py-6 text-center font-mono text-[11px] text-zinc-400 dark:text-zinc-500">
              No matching entities
            </div>
          ) : (
            matches.map((match, index) => {
              const isActive = index === active;
              return (
                <button
                  key={`${match.candidate.kind}:${match.candidate.id}`}
                  type="button"
                  data-active={isActive}
                  onMouseMove={() => setActive(index)}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => select(index)}
                  className={`flex h-8 w-full min-w-0 items-center gap-2 px-3 text-left ${
                    isActive
                      ? "bg-indigo-50 dark:bg-indigo-950/60"
                      : "hover:bg-zinc-50 dark:hover:bg-zinc-900"
                  }`}
                >
                  <KindBadge kind={match.candidate.kind} />
                  <span className="min-w-0 shrink truncate font-mono text-[11px] text-zinc-800 dark:text-zinc-100">
                    <Highlighted text={match.candidate.name} indices={match.indices} />
                  </span>
                  <span className="ml-auto min-w-0 truncate font-mono text-[9px] text-zinc-400 dark:text-zinc-500">
                    {match.candidate.detail}
                  </span>
                </button>
              );
            })
          )}
        </div>

        <div className="flex h-7 items-center gap-3 border-t border-zinc-200 px-3 font-mono text-[9px] text-zinc-400 dark:border-zinc-800 dark:text-zinc-500">
          <span className="flex items-center gap-1">
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd> navigate
          </span>
          <span className="flex items-center gap-1">
            <Kbd>↵</Kbd> inspect
          </span>
          <span className="ml-auto tabular-nums">{formatInt(matches.length)} results</span>
        </div>
      </div>
    </div>
  );
}
