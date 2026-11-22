import { ArrowDown, Search, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import type { EntityIndex } from "../lib/derive";
import { isHotspotCandidate } from "../lib/derive";
import {
  formatInt,
  kindLabel,
  riskScore,
  syncLabel,
  truncateMiddle,
} from "../lib/format";
import type { Report, ReportSymbol } from "../types/report";
import { Badge, EmptyState, SyncDot } from "./ui";

type SortKey = "risk" | "complexity" | "loc" | "fanIn" | "name" | "module" | "file";
type SortDirection = "asc" | "desc";

interface Row {
  symbol: ReportSymbol;
  risk: number;
}

function SortHeader({
  label,
  column,
  active,
  direction,
  onSort,
  align = "right",
}: {
  label: string;
  column: SortKey;
  active: boolean;
  direction: SortDirection;
  onSort: (column: SortKey) => void;
  align?: "left" | "right";
}) {
  return (
    <th
      scope="col"
      className={`sticky top-0 z-10 h-7 border-b border-zinc-200 bg-zinc-50 px-2 font-mono text-[9px] font-medium uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400 ${
        align === "right" ? "text-right" : "text-left"
      }`}
    >
      <button
        type="button"
        onClick={() => onSort(column)}
        className={`inline-flex items-center gap-0.5 hover:text-zinc-900 dark:hover:text-zinc-100 ${
          active ? "text-zinc-900 dark:text-zinc-100" : ""
        }`}
      >
        {label}
        {active ? (
          <ArrowDown
            size={10}
            className={direction === "asc" ? "rotate-180 text-indigo-500" : "text-indigo-500"}
          />
        ) : null}
      </button>
    </th>
  );
}

function FilterPill({
  active,
  onClick,
  children,
  tone = "indigo",
  title,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
  tone?: "indigo" | "red";
  title?: string;
}) {
  const activeClass =
    tone === "red"
      ? "border-red-300 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950 dark:text-red-300"
      : "border-indigo-300 bg-indigo-50 text-indigo-700 dark:border-indigo-800 dark:bg-indigo-950 dark:text-indigo-300";
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      title={title}
      className={`inline-flex h-6 items-center rounded border px-2 font-mono text-[10px] transition-colors ${
        active
          ? activeClass
          : "border-zinc-200 bg-white text-zinc-500 hover:bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400 dark:hover:bg-zinc-900"
      }`}
    >
      {children}
    </button>
  );
}

export function HotspotsView({
  report,
  index,
  onSelect,
}: {
  report: Report;
  index: EntityIndex;
  onSelect: (id: string) => void;
}) {
  const hasUntracked = useMemo(() => report.symbols.some((symbol) => symbol.untracked), [report.symbols]);
  const [search, setSearch] = useState("");
  const [moduleFilter, setModuleFilter] = useState("");
  const [untestedOnly, setUntestedOnly] = useState(false);
  const [hideGenerated, setHideGenerated] = useState(true);
  const [hideUntracked, setHideUntracked] = useState(hasUntracked);
  const [sortKey, setSortKey] = useState<SortKey>("risk");
  const [sortDirection, setSortDirection] = useState<SortDirection>("desc");
  const seenUntracked = useRef(hasUntracked);

  useEffect(() => {
    if (hasUntracked && !seenUntracked.current) {
      seenUntracked.current = true;
      setHideUntracked(true);
    }
  }, [hasUntracked]);

  const rows = useMemo<Row[]>(() => {
    const query = search.trim().toLowerCase();
    const result: Row[] = [];
    for (const symbol of report.symbols) {
      if (!isHotspotCandidate(symbol)) {
        continue;
      }
      if (hideGenerated && symbol.is_generated) {
        continue;
      }
      if (hideUntracked && symbol.untracked) {
        continue;
      }
      if (moduleFilter !== "" && symbol.module !== moduleFilter) {
        continue;
      }
      if (
        untestedOnly &&
        !((symbol.kind === "func" || symbol.kind === "method") && symbol.test_covered === false)
      ) {
        continue;
      }
      if (query !== "") {
        const haystack = `${symbol.name} ${symbol.file ?? ""} ${symbol.module ?? ""} ${symbol.pkg_path ?? ""}`.toLowerCase();
        if (!haystack.includes(query)) {
          continue;
        }
      }
      result.push({ symbol, risk: riskScore(symbol) });
    }

    const direction = sortDirection === "asc" ? 1 : -1;
    result.sort((left, right) => {
      let comparison = 0;
      switch (sortKey) {
        case "risk":
          comparison = left.risk - right.risk;
          break;
        case "complexity":
          comparison = left.symbol.complexity - right.symbol.complexity;
          break;
        case "loc":
          comparison = left.symbol.loc - right.symbol.loc;
          break;
        case "fanIn":
          comparison = left.symbol.fan_in - right.symbol.fan_in;
          break;
        case "module":
          comparison = (left.symbol.module ?? "").localeCompare(right.symbol.module ?? "");
          break;
        case "file":
          comparison = (left.symbol.file ?? "").localeCompare(right.symbol.file ?? "");
          break;
        case "name":
          comparison = left.symbol.name.localeCompare(right.symbol.name);
          break;
      }
      if (comparison !== 0) {
        return comparison * direction;
      }
      return left.symbol.id.localeCompare(right.symbol.id);
    });
    return result;
  }, [report.symbols, search, moduleFilter, untestedOnly, hideGenerated, hideUntracked, sortKey, sortDirection]);

  const maxRisk = rows.reduce((maximum, row) => Math.max(maximum, row.risk), 1);
  const total = useMemo(
    () => report.symbols.filter(isHotspotCandidate).length,
    [report.symbols],
  );

  const handleSort = (column: SortKey) => {
    if (column === sortKey) {
      setSortDirection((current) => (current === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(column);
      setSortDirection(column === "name" || column === "module" || column === "file" ? "asc" : "desc");
    }
  };

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
            placeholder="Filter symbols, files, packages…"
            className="h-6 w-64 rounded border border-zinc-200 bg-white pl-6 pr-6 font-mono text-[11px] text-zinc-800 placeholder:text-zinc-400 focus:border-indigo-500 focus:outline-none dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200 dark:placeholder:text-zinc-600"
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

        <select
          value={moduleFilter}
          onChange={(event) => setModuleFilter(event.target.value)}
          aria-label="Filter by module"
          className="h-6 rounded border border-zinc-200 bg-white px-1.5 font-mono text-[10px] text-zinc-600 focus:border-indigo-500 focus:outline-none dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300"
        >
          <option value="">all modules</option>
          {index.moduleNames.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>

        <FilterPill
          active={untestedOnly}
          onClick={() => setUntestedOnly((current) => !current)}
          tone="red"
          title="Show only functions and methods with no static test reachability"
        >
          untested only
        </FilterPill>
        <FilterPill
          active={hideGenerated}
          onClick={() => setHideGenerated((current) => !current)}
          title="Hide generated declarations"
        >
          hide generated
        </FilterPill>
        {hasUntracked ? (
          <FilterPill
            active={hideUntracked}
            onClick={() => setHideUntracked((current) => !current)}
            title="Hide files that git reports as untracked"
          >
            hide untracked
          </FilterPill>
        ) : null}

        <div className="ml-auto font-mono text-[10px] tabular-nums text-zinc-400 dark:text-zinc-500">
          {formatInt(rows.length)} / {formatInt(total)} rows
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-auto">
        {rows.length === 0 ? (
          <EmptyState
            title="No matching hotspots"
            detail="Relax the filters or clear the search to see production risk symbols."
          />
        ) : (
          <table className="w-full min-w-[780px] border-collapse text-left">
            <thead>
              <tr>
                <SortHeader label="Risk" column="risk" active={sortKey === "risk"} direction={sortDirection} onSort={handleSort} />
                <SortHeader label="CC" column="complexity" active={sortKey === "complexity"} direction={sortDirection} onSort={handleSort} />
                <SortHeader label="LOC" column="loc" active={sortKey === "loc"} direction={sortDirection} onSort={handleSort} />
                <SortHeader label="Fan-in" column="fanIn" active={sortKey === "fanIn"} direction={sortDirection} onSort={handleSort} />
                <SortHeader label="Name" column="name" active={sortKey === "name"} direction={sortDirection} onSort={handleSort} align="left" />
                <SortHeader label="Module" column="module" active={sortKey === "module"} direction={sortDirection} onSort={handleSort} align="left" />
                <SortHeader label="File:Line" column="file" active={sortKey === "file"} direction={sortDirection} onSort={handleSort} align="left" />
              </tr>
            </thead>
            <tbody>
              {rows.map(({ symbol, risk }) => {
                const untested =
                  (symbol.kind === "func" || symbol.kind === "method") && symbol.test_covered === false;
                const barTone =
                  risk / maxRisk > 0.66
                    ? "bg-red-500"
                    : risk / maxRisk > 0.33
                      ? "bg-amber-500"
                      : "bg-zinc-300 dark:bg-zinc-600";
                return (
                  <tr
                    key={symbol.id}
                    tabIndex={0}
                    onClick={() => onSelect(symbol.id)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        onSelect(symbol.id);
                      }
                    }}
                    className="h-[30px] cursor-pointer border-b border-zinc-100 outline-none hover:bg-zinc-50 focus-visible:bg-indigo-50 dark:border-zinc-900 dark:hover:bg-zinc-900 dark:focus-visible:bg-indigo-950"
                  >
                    <td className="w-[120px] px-2 py-1">
                      <div className="flex items-center justify-end gap-2">
                        <span className="font-mono text-[10px] tabular-nums text-zinc-600 dark:text-zinc-300">
                          {risk}
                        </span>
                        <span className="h-1 w-12 overflow-hidden rounded-full bg-zinc-100 dark:bg-zinc-800">
                          <span
                            className={`block h-full rounded-full ${barTone}`}
                            style={{ width: `${Math.max(4, (risk / maxRisk) * 100)}%` }}
                          />
                        </span>
                      </div>
                    </td>
                    <td className="w-[60px] px-2 py-1 text-right font-mono text-[10px] tabular-nums text-zinc-700 dark:text-zinc-300">
                      {symbol.complexity}
                    </td>
                    <td className="w-[60px] px-2 py-1 text-right font-mono text-[10px] tabular-nums text-zinc-700 dark:text-zinc-300">
                      {symbol.loc}
                    </td>
                    <td className="w-[70px] px-2 py-1 text-right font-mono text-[10px] tabular-nums text-zinc-700 dark:text-zinc-300">
                      {symbol.fan_in}
                    </td>
                    <td className="min-w-0 px-2 py-1">
                      <div className="flex min-w-0 items-center gap-1.5">
                        <SyncDot state={symbol.sync_state} label={syncLabel(symbol.sync_state)} />
                        <span className="truncate font-mono text-[11px] font-medium text-zinc-800 dark:text-zinc-100">
                          {symbol.name}
                        </span>
                        <span className="shrink-0 font-mono text-[9px] uppercase text-zinc-400 dark:text-zinc-500">
                          {kindLabel(symbol.kind)}
                        </span>
                        {untested ? <Badge tone="red">untested</Badge> : null}
                        {symbol.is_generated ? <Badge tone="amber">gen</Badge> : null}
                        {symbol.untracked ? <Badge tone="amber">untracked</Badge> : null}
                      </div>
                    </td>
                    <td className="w-[130px] px-2 py-1">
                      <span className="block truncate font-mono text-[10px] text-zinc-500 dark:text-zinc-400">
                        {symbol.module ?? ""}
                      </span>
                    </td>
                    <td className="px-2 py-1">
                      <span
                        className="block truncate font-mono text-[10px] text-zinc-500 dark:text-zinc-400"
                        title={symbol.file ? `${symbol.file}:${symbol.start_line ?? 0}` : ""}
                      >
                        {symbol.file
                          ? truncateMiddle(`${symbol.file}:${symbol.start_line ?? 0}`, 64)
                          : ""}
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
