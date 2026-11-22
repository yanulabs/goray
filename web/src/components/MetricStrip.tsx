import type { ReportCounts } from "../lib/derive";
import { formatInt, formatPercent } from "../lib/format";

function Metric({
  label,
  value,
  tone = "",
  title,
}: {
  label: string;
  value: string;
  tone?: string;
  title?: string;
}) {
  return (
    <div className="flex shrink-0 items-center gap-1.5 px-2.5" title={title}>
      <span className="font-mono text-[9px] uppercase tracking-widest text-zinc-400 dark:text-zinc-500">
        {label}
      </span>
      <span className={`font-mono text-[11px] leading-none tabular-nums ${tone || "text-zinc-800 dark:text-zinc-200"}`}>
        {value}
      </span>
    </div>
  );
}

function Divider() {
  return <span className="h-3 w-px shrink-0 bg-zinc-200 dark:bg-zinc-800" />;
}

export function MetricStrip({ counts }: { counts: ReportCounts }) {
  const cycleTone =
    counts.cycles > 0
      ? "text-red-600 dark:text-red-400 font-semibold"
      : "text-zinc-800 dark:text-zinc-200";
  const untestedTone =
    counts.untested > 0 ? "text-red-600 dark:text-red-400" : "text-emerald-700 dark:text-emerald-400";
  const coverageTone =
    counts.coveragePercent >= 90
      ? "text-emerald-700 dark:text-emerald-400"
      : counts.coveragePercent >= 50
        ? "text-amber-700 dark:text-amber-400"
        : counts.coveragePercent > 0
          ? "text-red-700 dark:text-red-400"
          : "text-zinc-500 dark:text-zinc-400";

  return (
    <div className="flex h-8 shrink-0 items-center overflow-x-auto border-b border-zinc-200 bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-900/50">
      <Metric label="modules" value={formatInt(counts.modules)} />
      <Divider />
      <Metric label="packages" value={formatInt(counts.packages)} />
      <Divider />
      <Metric label="files" value={formatInt(counts.files)} />
      <Divider />
      <Metric label="symbols" value={formatInt(counts.symbols)} />
      <Divider />
      <Metric
        label="cycles"
        value={formatInt(counts.cycles)}
        tone={cycleTone}
        title={counts.cycles > 0 ? "Cross-module dependency cycles detected" : "No cross-module cycles"}
      />
      <Divider />
      <Metric label="hotspots" value={formatInt(counts.hotspots)} title="Top production risk symbols" />
      <Divider />
      <Metric
        label="untested"
        value={formatInt(counts.untested)}
        tone={untestedTone}
        title="Production functions and methods with no static test reachability"
      />
      <Divider />
      <Metric
        label="summary"
        value={formatPercent(counts.coveragePercent)}
        tone={coverageTone}
        title="Synced summaries as a percentage of eligible symbols"
      />
      <div className="flex-1" />
      {counts.hasUntracked ? (
        <div className="hidden shrink-0 items-center gap-1.5 pr-3 sm:flex" title="Untracked files are present in the index">
          <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
          <span className="font-mono text-[9px] uppercase tracking-widest text-amber-700 dark:text-amber-400">
            untracked
          </span>
        </div>
      ) : null}
    </div>
  );
}
