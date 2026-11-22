import { Command, GitBranch, Layers3, Moon, Sun } from "lucide-react";

import type { ReportCounts } from "../lib/derive";
import { formatPercent } from "../lib/format";
import type { ViewMode } from "../types/report";
import { Kbd, Segmented } from "./ui";

export type LiveStatus = "static" | "live" | "stale" | "connecting";

export interface TopbarProps {
  modulePath: string;
  view: ViewMode;
  onView: (view: ViewMode) => void;
  counts: ReportCounts;
  liveStatus: LiveStatus;
  theme: "light" | "dark";
  onToggleTheme: () => void;
  onOpenPalette: () => void;
}

const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.userAgent);

function healthTone(counts: ReportCounts): string {
  if (counts.coveragePercent >= 90) {
    return "text-emerald-700 dark:text-emerald-400";
  }
  if (counts.coveragePercent >= 50) {
    return "text-amber-700 dark:text-amber-400";
  }
  return "text-red-700 dark:text-red-400";
}

export function Topbar({
  modulePath,
  view,
  onView,
  counts,
  liveStatus,
  theme,
  onToggleTheme,
  onOpenPalette,
}: TopbarProps) {
  return (
    <header className="grid h-11 shrink-0 grid-cols-[1fr_auto_1fr] items-center gap-3 border-b border-zinc-200 bg-white px-3 dark:border-zinc-800 dark:bg-zinc-950">
      <div className="flex min-w-0 items-center gap-2">
        <span
          aria-hidden="true"
          className="inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-sm border border-zinc-300 bg-zinc-900 font-mono text-[10px] font-semibold text-white dark:border-zinc-700 dark:bg-zinc-100 dark:text-zinc-900"
        >
          G
        </span>
        <span className="shrink-0 font-mono text-[11px] font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">
          goray
        </span>
        <span className="h-3 w-px shrink-0 bg-zinc-200 dark:bg-zinc-800" />
        <span
          className="min-w-0 truncate font-mono text-[11px] text-zinc-500 dark:text-zinc-400"
          title={modulePath}
        >
          {modulePath}
        </span>
        <LiveBadge status={liveStatus} />
      </div>

      <div className="hidden justify-self-center lg:block">
        <Segmented<ViewMode>
          value={view}
          onChange={onView}
          options={[
            { value: "graph", label: "Graph", icon: <GitBranch size={12} /> },
            { value: "hotspots", label: "Hotspots", icon: <Layers3 size={12} /> },
            { value: "interfaces", label: "Interfaces" },
            { value: "explorer", label: "Explorer" },
          ]}
        />
      </div>

      <div className="flex min-w-0 items-center justify-end gap-2">
        <button
          type="button"
          onClick={onOpenPalette}
          className="hidden h-7 items-center gap-2 rounded border border-zinc-200 bg-white px-2 text-[11px] text-zinc-500 hover:border-zinc-300 hover:bg-zinc-50 sm:inline-flex dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400 dark:hover:border-zinc-700 dark:hover:bg-zinc-900"
          title="Search symbols, packages, files, and interfaces"
        >
          <Command size={12} />
          <span>Search</span>
          <span className="ml-2 flex items-center gap-0.5">
            <Kbd>{mac ? "⌘" : "Ctrl"}</Kbd>
            <Kbd>K</Kbd>
          </span>
        </button>

        <div
          className="hidden items-center gap-1.5 rounded border border-zinc-200 bg-zinc-50 px-2 py-1 font-mono text-[10px] tabular-nums sm:flex dark:border-zinc-800 dark:bg-zinc-900"
          title={`Summary coverage: ${counts.coveragePercent.toFixed(1)}% of eligible symbols are synced`}
        >
          <span className={healthTone(counts)}>SUMMARY</span>
          <span className={healthTone(counts)}>{formatPercent(counts.coveragePercent)}</span>
        </div>

        <button
          type="button"
          onClick={onToggleTheme}
          className="inline-flex h-7 w-7 items-center justify-center rounded border border-zinc-200 bg-white text-zinc-600 hover:bg-zinc-100 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400 dark:hover:bg-zinc-900"
          title={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
          aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
        >
          {theme === "dark" ? <Sun size={13} /> : <Moon size={13} />}
        </button>
      </div>
    </header>
  );
}

function LiveBadge({ status }: { status: LiveStatus }) {
  if (status === "static") {
    return (
      <span
        className="inline-flex shrink-0 items-center gap-1 rounded border border-zinc-200 bg-zinc-50 px-1.5 py-px font-mono text-[9px] uppercase tracking-wide text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400"
        title="Static offline report; data is embedded in this file"
      >
        <span className="h-1 w-1 rounded-full bg-zinc-400 dark:bg-zinc-600" />
        static
      </span>
    );
  }
  if (status === "stale") {
    return (
      <span
        className="inline-flex shrink-0 items-center gap-1 rounded border border-amber-300 bg-amber-50 px-1.5 py-px font-mono text-[9px] uppercase tracking-wide text-amber-700 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300"
        title="Live refresh failed; showing the last successful snapshot"
      >
        <span className="h-1 w-1 rounded-full bg-amber-500" />
        stale
      </span>
    );
  }
  return (
    <span
      className="inline-flex shrink-0 items-center gap-1 rounded border border-emerald-300 bg-emerald-50 px-1.5 py-px font-mono text-[9px] uppercase tracking-wide text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-300"
      title="Connected to goray serve; the report refreshes automatically"
    >
      <span className="h-1 w-1 rounded-full bg-emerald-500" />
      live
    </span>
  );
}
