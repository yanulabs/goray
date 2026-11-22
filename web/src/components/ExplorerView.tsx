import { ChevronRight, Search, X } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import type { EntityIndex } from "../lib/derive";
import { formatCompact, syncLabel, truncateMiddle } from "../lib/format";
import type { Report, ReportTreeNode } from "../types/report";
import { EmptyState, KindBadge, SyncDot } from "./ui";

function metricHint(node: ReportTreeNode, index: EntityIndex): string {
  const entity = index.entitiesById.get(node.id);
  if (!entity) {
    return "";
  }
  switch (node.kind) {
    case "module": {
      const card = entity.moduleCard;
      return card ? `${card.packages} pkgs · ${formatCompact(card.prod_loc)} loc` : "";
    }
    case "package": {
      const card = entity.packageCard;
      return card ? `${formatCompact(card.prod_loc)} loc · in ${card.fan_in}` : "";
    }
    case "func":
    case "method":
      return `cc ${entity.metrics.complexity} · ${entity.metrics.loc} loc`;
    case "file":
      return `${formatCompact(entity.metrics.loc)} loc`;
    case "type":
    case "struct":
    case "interface":
      return entity.metrics.loc > 0 ? `${formatCompact(entity.metrics.loc)} loc` : "";
    default:
      return "";
  }
}

export function ExplorerView({
  report,
  index,
  onSelect,
}: {
  report: Report;
  index: EntityIndex;
  onSelect: (id: string) => void;
}) {
  const [search, setSearch] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(
    () => new Set(report.tree.filter((node) => node.kind === "module").map((node) => node.id)),
  );
  const query = search.trim();

  const visible = useMemo(() => {
    if (query === "") {
      return null;
    }
    const matched = new Set<string>();
    const needle = query.toLowerCase();
    const visit = (node: ReportTreeNode): boolean => {
      const self = `${node.label} ${node.kind} ${node.id}`.toLowerCase().includes(needle);
      let childMatch = false;
      for (const child of node.children ?? []) {
        if (visit(child)) {
          childMatch = true;
        }
      }
      if (self || childMatch) {
        matched.add(node.id);
      }
      return self || childMatch;
    };
    for (const node of report.tree) {
      visit(node);
    }
    return matched;
  }, [report.tree, query]);

  const toggle = (id: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  const renderNodes = (nodes: ReportTreeNode[], depth: number): ReactNode[] =>
    nodes.flatMap((node) => {
      if (visible !== null && !visible.has(node.id)) {
        return [];
      }
      const hasChildren = (node.children?.length ?? 0) > 0;
      const open = query !== "" || expanded.has(node.id);
      const entity = index.entitiesById.get(node.id);
      const hint = metricHint(node, index);
      const row = (
        <div
          key={`row:${node.id}`}
          className="group flex h-[26px] min-w-0 items-center border-b border-transparent hover:border-zinc-100 hover:bg-zinc-50 dark:hover:border-zinc-900 dark:hover:bg-zinc-900/60"
          style={{ paddingLeft: 6 + depth * 14 }}
        >
          <button
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              if (hasChildren) {
                toggle(node.id);
              }
            }}
            aria-label={hasChildren ? (open ? "Collapse" : "Expand") : "No children"}
            className={`inline-flex h-4 w-4 shrink-0 items-center justify-center rounded text-zinc-400 ${
              hasChildren ? "hover:bg-zinc-200 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-200" : "opacity-0"
            }`}
            tabIndex={hasChildren ? 0 : -1}
          >
            <ChevronRight
              size={11}
              className={`transition-transform ${open && hasChildren ? "rotate-90" : ""}`}
            />
          </button>
          <button
            type="button"
            onClick={() => onSelect(node.id)}
            className="flex h-full min-w-0 flex-1 items-center gap-1.5 pr-2 text-left outline-none focus-visible:bg-indigo-50 dark:focus-visible:bg-indigo-950"
            title={`${node.label}${entity?.file ? ` · ${entity.file}:${entity.line}` : ""}`}
          >
            <KindBadge kind={node.kind} />
            <SyncDot state={node.sync_state} label={syncLabel(node.sync_state)} />
            <span className="min-w-0 truncate font-mono text-[11px] text-zinc-800 dark:text-zinc-100">
              {node.kind === "file" ? truncateMiddle(node.label, 80) : node.label}
            </span>
            {entity?.untracked ? (
              <span className="shrink-0 font-mono text-[8px] uppercase tracking-wide text-amber-600 dark:text-amber-400">
                untracked
              </span>
            ) : null}
            {hint !== "" ? (
              <span className="ml-auto hidden shrink-0 font-mono text-[9px] tabular-nums text-zinc-400 sm:inline dark:text-zinc-500">
                {hint}
              </span>
            ) : null}
          </button>
        </div>
      );
      if (!open || !hasChildren) {
        return [row];
      }
      return [row, ...renderNodes(node.children ?? [], depth + 1)];
    });

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-zinc-200 px-2 py-1.5 dark:border-zinc-800">
        <div className="relative">
          <Search
            size={12}
            className="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-zinc-400"
          />
          <input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Filter modules, packages, files, symbols…"
            className="h-6 w-72 rounded border border-zinc-200 bg-white pl-6 pr-6 font-mono text-[11px] text-zinc-800 placeholder:text-zinc-400 focus:border-indigo-500 focus:outline-none dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200 dark:placeholder:text-zinc-600"
          />
          {search !== "" ? (
            <button
              type="button"
              onClick={() => setSearch("")}
              className="absolute right-1 top-1/2 -translate-y-1/2 rounded p-0.5 text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-200"
              aria-label="Clear filter"
            >
              <X size={11} />
            </button>
          ) : null}
        </div>
        <span className="font-mono text-[10px] text-zinc-400 dark:text-zinc-500">
          {query !== "" ? `${visible?.size ?? 0} matching nodes` : `${report.tree.length} roots`}
        </span>
        <div className="ml-auto flex items-center gap-2">
          <button
            type="button"
            onClick={() =>
              setExpanded(new Set(report.tree.filter((node) => node.kind === "module").map((node) => node.id)))
            }
            className="h-6 rounded border border-zinc-200 bg-white px-1.5 font-mono text-[10px] text-zinc-500 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400 dark:hover:bg-zinc-900"
          >
            collapse
          </button>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-auto py-1">
        {query !== "" && (visible?.size ?? 0) === 0 ? (
          <EmptyState title="No matching nodes" detail="Clear the filter to restore the hierarchy." />
        ) : (
          renderNodes(report.tree, 0)
        )}
      </div>
    </div>
  );
}
