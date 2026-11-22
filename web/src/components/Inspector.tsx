import { Check, Copy, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import type { Entity, EntityIndex } from "../lib/derive";
import { symbolName } from "../lib/derive";
import {
  clamp,
  copyText,
  fileLine,
  formatInt,
  kindLabel,
  syncLabel,
} from "../lib/format";
import type { ReportSummary } from "../types/report";
import { Badge, KindBadge, SyncDot } from "./ui";

const sectionTitle =
  "font-mono text-[9px] font-medium uppercase tracking-widest text-zinc-400 dark:text-zinc-500";

function MetricCell({ label, value, tone = "" }: { label: string; value: string; tone?: string }) {
  return (
    <div className="border-r border-b border-zinc-200 px-2 py-1.5 last:border-r-0 dark:border-zinc-800">
      <div className="font-mono text-[8px] uppercase tracking-wider text-zinc-400 dark:text-zinc-500">
        {label}
      </div>
      <div className={`font-mono text-[11px] leading-4 tabular-nums ${tone || "text-zinc-800 dark:text-zinc-200"}`}>
        {value}
      </div>
    </div>
  );
}

function Chips({ values }: { values: string[] }) {
  if (values.length === 0) {
    return <span className="text-[11px] text-zinc-400 dark:text-zinc-500">None</span>;
  }
  return (
    <div className="flex flex-wrap gap-1">
      {values.map((value) => (
        <Badge key={value}>{value}</Badge>
      ))}
    </div>
  );
}

function SummaryBlock({ summary }: { summary: ReportSummary | undefined }) {
  if (!summary) {
    return (
      <div className="rounded border border-zinc-200 bg-zinc-50 p-2.5 dark:border-zinc-800 dark:bg-zinc-900/50">
        <div className="font-mono text-[10px] text-zinc-500 dark:text-zinc-400">
          No cached LLM summary
        </div>
        <div className="mt-1 text-[10px] leading-4 text-zinc-400 dark:text-zinc-500">
          Populate summaries with the goray summary tools; this panel stays empty until then.
        </div>
      </div>
    );
  }
  const invariants = [...(summary.invariants ?? []), ...(summary.key_invariants ?? [])];
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-1.5">
        <SyncDot state={summary.sync_state} label={syncLabel(summary.sync_state)} />
        <span className="font-mono text-[9px] uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
          {summary.sync_state.replace("_", " ")}
        </span>
        {summary.deps_changed ? <Badge tone="amber">deps changed</Badge> : null}
        {summary.model_id ? (
          <span className="ml-auto truncate font-mono text-[9px] text-zinc-400 dark:text-zinc-500" title={summary.model_id}>
            {summary.model_id}
          </span>
        ) : null}
      </div>

      {summary.intent ? (
        <p className="text-[11px] leading-5 text-zinc-700 dark:text-zinc-300">{summary.intent}</p>
      ) : null}

      {summary.concurrency ? (
        <div>
          <div className={sectionTitle}>Concurrency</div>
          <p className="mt-0.5 text-[11px] leading-4 text-zinc-600 dark:text-zinc-400">
            {summary.concurrency}
          </p>
        </div>
      ) : null}

      <div>
        <div className={sectionTitle}>Side effects</div>
        <div className="mt-1">
          <Chips values={summary.side_effects ?? []} />
        </div>
      </div>

      {invariants.length > 0 ? (
        <div>
          <div className={sectionTitle}>Invariants</div>
          <ul className="mt-1 space-y-1">
            {invariants.map((invariant, index) => (
              <li
                key={`${invariant}:${index}`}
                className="flex gap-1.5 text-[11px] leading-4 text-zinc-600 dark:text-zinc-400"
              >
                <span className="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-zinc-400 dark:bg-zinc-600" />
                <span>{invariant}</span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {(summary.boundaries ?? []).length > 0 ? (
        <div>
          <div className={sectionTitle}>Boundaries</div>
          <div className="mt-1">
            <Chips values={summary.boundaries ?? []} />
          </div>
        </div>
      ) : null}
    </div>
  );
}

function transitiveCallerCount(index: EntityIndex, startID: string): number {
  const seen = new Set<string>();
  const queue: string[] = [startID];
  for (let cursor = 0; cursor < queue.length; cursor += 1) {
    const current = queue[cursor];
    if (current === undefined) {
      continue;
    }
    const symbol = index.symbolsById.get(current);
    for (const caller of symbol?.callers ?? []) {
      if (caller === startID || seen.has(caller)) {
        continue;
      }
      seen.add(caller);
      queue.push(caller);
    }
  }
  return seen.size;
}

function RefList({
  title,
  ids,
  index,
  onSelect,
}: {
  title: string;
  ids: string[];
  index: EntityIndex;
  onSelect: (id: string) => void;
}) {
  return (
    <div>
      <div className="flex items-center justify-between">
        <div className={sectionTitle}>
          {title} <span className="tabular-nums">({formatInt(ids.length)})</span>
        </div>
      </div>
      {ids.length === 0 ? (
        <div className="mt-1 font-mono text-[10px] text-zinc-400 dark:text-zinc-500">None indexed</div>
      ) : (
        <div className="mt-1 max-h-52 overflow-auto rounded border border-zinc-200 dark:border-zinc-800">
          {ids.map((id) => {
            const target = index.entitiesById.get(id);
            const location = target && target.file ? `${target.file}:${target.line}` : "";
            return (
              <button
                key={id}
                type="button"
                onClick={() => onSelect(id)}
                disabled={!target}
                title={id}
                className="flex h-6 w-full min-w-0 items-center gap-2 border-b border-zinc-100 px-1.5 text-left last:border-b-0 enabled:hover:bg-zinc-50 disabled:cursor-default dark:border-zinc-900 dark:enabled:hover:bg-zinc-900"
              >
                <span className="min-w-0 truncate font-mono text-[10px] text-zinc-700 dark:text-zinc-300">
                  {symbolName(index, id)}
                </span>
                <span className="ml-auto shrink-0 font-mono text-[9px] text-zinc-400 dark:text-zinc-600">
                  {location ? location.split("/").pop() : ""}
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

export function Inspector({
  selectedId,
  entity,
  index,
  width,
  onWidth,
  onClose,
  onSelect,
}: {
  selectedId: string;
  entity: Entity | undefined;
  index: EntityIndex;
  width: number;
  onWidth: (width: number) => void;
  onClose: () => void;
  onSelect: (id: string) => void;
}) {
  const [copied, setCopied] = useState<string | null>(null);
  const resize = useRef<{ startX: number; startWidth: number } | null>(null);
  const copyTimer = useRef<number | null>(null);

  const copy = async (value: string, key: string) => {
    const didCopy = await copyText(value);
    if (!didCopy) {
      return;
    }
    setCopied(key);
    if (copyTimer.current !== null) {
      window.clearTimeout(copyTimer.current);
    }
    copyTimer.current = window.setTimeout(() => setCopied(null), 1200);
  };

  useEffect(
    () => () => {
      if (copyTimer.current !== null) {
        window.clearTimeout(copyTimer.current);
      }
    },
    [],
  );

  const blastRadius = useMemo(
    () => (entity?.symbol ? transitiveCallerCount(index, entity.symbol.id) : 0),
    [index, entity],
  );

  if (!entity) {
    return (
      <aside
        className="relative flex h-full shrink-0 flex-col border-l border-zinc-200 bg-white dark:border-zinc-800 dark:bg-zinc-950"
        style={{ width }}
      >
        <div className="flex items-center justify-between border-b border-zinc-200 p-2 dark:border-zinc-800">
          <span className={sectionTitle}>Inspector</span>
          <button
            type="button"
            onClick={onClose}
            className="rounded p-1 text-zinc-400 hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-900 dark:hover:text-zinc-200"
            aria-label="Close inspector"
          >
            <X size={13} />
          </button>
        </div>
        <div className="p-3">
          <div className="font-mono text-[11px] break-all text-zinc-600 dark:text-zinc-400">{selectedId}</div>
          <div className="mt-2 text-[11px] text-zinc-400 dark:text-zinc-500">
            This entity is not present in the current snapshot.
          </div>
        </div>
      </aside>
    );
  }

  const symbol = entity.symbol;
  const moduleCard = entity.moduleCard;
  const packageCard = entity.packageCard;
  const iface = entity.interfaceRef;
  const location = fileLine(entity.file, entity.line);
  const testCovered = entity.metrics.testCovered;
  const testCoveredLabel =
    testCovered === true ? "yes" : testCovered === false ? "no" : "—";
  const testCoveredTone =
    testCovered === true
      ? "text-emerald-700 dark:text-emerald-400"
      : testCovered === false
        ? "text-red-700 dark:text-red-400"
        : "text-zinc-400 dark:text-zinc-500";

  const moduleMetrics: Array<{ label: string; value: string; tone?: string }> = moduleCard
    ? [
        { label: "packages", value: formatInt(moduleCard.packages) },
        { label: "prod loc", value: formatInt(moduleCard.prod_loc) },
        { label: "test loc", value: formatInt(moduleCard.test_loc) },
        { label: "exports", value: formatInt(moduleCard.root_exported) },
        { label: "ports", value: formatInt(moduleCard.ports_calls) },
        { label: "goroutines", value: formatInt(moduleCard.goroutines) },
        { label: "in / out", value: `${moduleCard.in_modules} / ${moduleCard.out_modules}` },
        {
          label: "untracked",
          value: formatInt(moduleCard.untracked_files),
          tone: moduleCard.untracked_files > 0 ? "text-amber-700 dark:text-amber-400" : "",
        },
      ]
    : packageCard
      ? [
          { label: "prod loc", value: formatInt(packageCard.prod_loc) },
          { label: "test loc", value: formatInt(packageCard.test_loc) },
          { label: "exports", value: formatInt(packageCard.exported) },
          { label: "fan-in", value: formatInt(packageCard.fan_in) },
          { label: "fan-out", value: formatInt(packageCard.fan_out) },
          {
            label: "untracked",
            value: formatInt(packageCard.untracked_files ?? 0),
            tone: (packageCard.untracked_files ?? 0) > 0 ? "text-amber-700 dark:text-amber-400" : "",
          },
          {
            label: "generated",
            value: formatInt(packageCard.generated_files ?? 0),
            tone: (packageCard.generated_files ?? 0) > 0 ? "text-amber-700 dark:text-amber-400" : "",
          },
          { label: "module", value: packageCard.module || "—" },
        ]
      : [];

  return (
    <aside
      className="relative flex h-full shrink-0 flex-col border-l border-zinc-200 bg-white dark:border-zinc-800 dark:bg-zinc-950"
      style={{ width, maxWidth: "75vw" }}
      aria-label="Entity inspector"
    >
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize inspector"
        onPointerDown={(event) => {
          event.preventDefault();
          event.currentTarget.setPointerCapture(event.pointerId);
          resize.current = { startX: event.clientX, startWidth: width };
        }}
        onPointerMove={(event) => {
          const current = resize.current;
          if (!current) {
            return;
          }
          onWidth(clamp(current.startWidth + (current.startX - event.clientX), 320, 560));
        }}
        onPointerUp={(event) => {
          resize.current = null;
          if (event.currentTarget.hasPointerCapture(event.pointerId)) {
            event.currentTarget.releasePointerCapture(event.pointerId);
          }
        }}
        className="absolute inset-y-0 left-0 z-10 w-1 cursor-col-resize hover:bg-indigo-400/60"
      />

      <div className="flex items-start gap-2 border-b border-zinc-200 p-2.5 pl-3 dark:border-zinc-800">
        <KindBadge kind={entity.kind} />
        <div className="min-w-0 flex-1">
          <h2 className="break-words font-mono text-[13px] font-semibold leading-5 text-zinc-900 dark:text-zinc-50">
            {entity.name}
          </h2>
          <div className="mt-1 flex flex-wrap items-center gap-1">
            <SyncDot state={entity.syncState} label={syncLabel(entity.syncState)} />
            <span className="font-mono text-[9px] text-zinc-400 dark:text-zinc-500">
              {syncLabel(entity.syncState)}
            </span>
            {symbol?.exported ? <Badge>exported</Badge> : null}
            {symbol?.dead ? <Badge tone="amber">dead</Badge> : null}
            {entity.untracked ? <Badge tone="amber">untracked</Badge> : null}
            {symbol?.is_generated ? <Badge tone="amber">generated</Badge> : null}
            {symbol?.is_test ? <Badge>test</Badge> : null}
            {testCovered === false ? <Badge tone="red">untested</Badge> : null}
            {testCovered === true ? <Badge tone="emerald">tested</Badge> : null}
          </div>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="rounded p-1 text-zinc-400 hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-900 dark:hover:text-zinc-200"
          aria-label="Close inspector"
        >
          <X size={13} />
        </button>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="space-y-1 border-b border-zinc-200 px-3 py-2 dark:border-zinc-800">
          <button
            type="button"
            onClick={() => void copy(entity.id, "id")}
            title={entity.id}
            className="flex w-full min-w-0 items-center gap-1.5 rounded px-1 py-0.5 text-left hover:bg-zinc-50 dark:hover:bg-zinc-900"
          >
            <span className="shrink-0 font-mono text-[8px] uppercase tracking-wider text-zinc-400">id</span>
            <span className="min-w-0 truncate font-mono text-[9px] text-zinc-500 dark:text-zinc-400">
              {entity.id}
            </span>
            <span className="ml-auto shrink-0 text-zinc-400">
              {copied === "id" ? <Check size={11} className="text-emerald-500" /> : <Copy size={11} />}
            </span>
          </button>
          {location !== "" ? (
            <button
              type="button"
              onClick={() => void copy(location, "location")}
              className="flex w-full min-w-0 items-center gap-1.5 rounded px-1 py-0.5 text-left hover:bg-zinc-50 dark:hover:bg-zinc-900"
              title={`Copy ${location}`}
            >
              <span className="shrink-0 font-mono text-[8px] uppercase tracking-wider text-zinc-400">loc</span>
              <span className="min-w-0 truncate font-mono text-[9px] text-zinc-500 dark:text-zinc-400">
                {location}
              </span>
              <span className="ml-auto shrink-0 text-zinc-400">
                {copied === "location" ? (
                  <Check size={11} className="text-emerald-500" />
                ) : (
                  <Copy size={11} />
                )}
              </span>
            </button>
          ) : null}
        </div>

        {entity.signature ? (
          <div className="border-b border-zinc-200 px-3 py-2 dark:border-zinc-800">
            <div className={sectionTitle}>Signature</div>
            <pre className="mt-1 overflow-x-auto whitespace-pre-wrap break-words rounded border border-zinc-200 bg-zinc-50 p-2 font-mono text-[10px] leading-4 text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300">
              {entity.signature}
            </pre>
          </div>
        ) : null}

        {moduleMetrics.length > 0 ? (
          <div className="border-b border-zinc-200 dark:border-zinc-800">
            <div className="grid grid-cols-4">
              {moduleMetrics.map((metric) => (
                <MetricCell key={metric.label} label={metric.label} value={metric.value} tone={metric.tone} />
              ))}
            </div>
          </div>
        ) : (
          <div className="border-b border-zinc-200 dark:border-zinc-800">
            <div className="grid grid-cols-5">
              <MetricCell label="loc" value={formatInt(entity.metrics.loc)} />
              <MetricCell label="cc" value={formatInt(entity.metrics.complexity)} />
              <MetricCell label="fan-in" value={formatInt(entity.metrics.fanIn)} />
              <MetricCell label="fan-out" value={formatInt(entity.metrics.fanOut)} />
              <MetricCell label="tested" value={testCoveredLabel} tone={testCoveredTone} />
            </div>
          </div>
        )}

        {iface ? (
          <div className="space-y-2 border-b border-zinc-200 px-3 py-2.5 dark:border-zinc-800">
            <div className="flex items-center justify-between">
              <div className={sectionTitle}>
                Implementations <span className="tabular-nums">({iface.implementations.length})</span>
              </div>
              <Badge>{iface.methods} methods</Badge>
            </div>
            {iface.implementations.length === 0 ? (
              <div className="font-mono text-[10px] text-amber-600 dark:text-amber-400">
                No go/types-verified implementations
              </div>
            ) : (
              <div className="rounded border border-zinc-200 dark:border-zinc-800">
                {iface.implementations.map((implementation) => (
                  <button
                    key={implementation.id}
                    type="button"
                    onClick={() => onSelect(implementation.id)}
                    className="flex h-7 w-full min-w-0 items-center gap-2 border-b border-zinc-100 px-1.5 text-left last:border-b-0 hover:bg-zinc-50 dark:border-zinc-900 dark:hover:bg-zinc-900"
                    title={`${implementation.pkg_path}.${implementation.name}`}
                  >
                    {implementation.via_ptr ? (
                      <span className="font-mono text-[10px] text-amber-600 dark:text-amber-400">*</span>
                    ) : null}
                    <span className="min-w-0 truncate font-mono text-[10px] text-zinc-700 dark:text-zinc-300">
                      {implementation.name}
                    </span>
                    <span className="ml-auto min-w-0 truncate font-mono text-[9px] text-zinc-400 dark:text-zinc-500">
                      {implementation.pkg_path}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </div>
        ) : null}

        {symbol && (symbol.kind === "func" || symbol.kind === "method") ? (
          <div className="space-y-2.5 border-b border-zinc-200 px-3 py-2.5 dark:border-zinc-800">
            <RefList
              title={`Upstream · callers · blast radius ${formatInt(blastRadius)}`}
              ids={symbol.callers ?? []}
              index={index}
              onSelect={onSelect}
            />
            <RefList
              title="Downstream · callees"
              ids={symbol.callees ?? []}
              index={index}
              onSelect={onSelect}
            />
          </div>
        ) : null}

        <div className="px-3 py-2.5">
          <div className="mb-1.5 flex items-center gap-1.5">
            <div className={sectionTitle}>Cached summary</div>
            {entity.summary ? (
              <SyncDot state={entity.summary.sync_state} label={syncLabel(entity.summary.sync_state)} />
            ) : null}
          </div>
          <SummaryBlock summary={entity.summary} />
        </div>

        {entity.doc ? (
          <div className="border-t border-zinc-200 px-3 py-2.5 dark:border-zinc-800">
            <div className={sectionTitle}>Doc</div>
            <p className="mt-1 whitespace-pre-wrap text-[11px] leading-5 text-zinc-600 dark:text-zinc-400">
              {entity.doc}
            </p>
          </div>
        ) : null}

        <div className="border-t border-zinc-200 px-3 py-2 dark:border-zinc-800">
          <div className="font-mono text-[8px] uppercase tracking-wider text-zinc-400 dark:text-zinc-600">
            {kindLabel(entity.kind)}
            {entity.pkgPath ? ` · ${entity.pkgPath}` : ""}
            {entity.module ? ` · ${entity.module}` : ""}
          </div>
        </div>
      </div>
    </aside>
  );
}
