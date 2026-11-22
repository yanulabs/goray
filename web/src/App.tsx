import { AlertTriangle, RotateCcw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { CommandPalette } from "./components/CommandPalette";
import { ExplorerView } from "./components/ExplorerView";
import { GraphView } from "./components/GraphView";
import { HotspotsView } from "./components/HotspotsView";
import { Inspector } from "./components/Inspector";
import { InterfacesView } from "./components/InterfacesView";
import { MetricStrip } from "./components/MetricStrip";
import { Topbar, type LiveStatus } from "./components/Topbar";
import { buildEntityIndex, reportCounts, type ReportCounts } from "./lib/derive";
import { fetchLiveReport, readEmbeddedReport, type ReportSource } from "./lib/data";
import type { GraphScope, ViewMode } from "./types/report";

const embeddedSource = readEmbeddedReport();

const EMPTY_COUNTS: ReportCounts = {
  modules: 0,
  packages: 0,
  files: 0,
  symbols: 0,
  cycles: 0,
  hotspots: 0,
  untested: 0,
  coveragePercent: 0,
  hasUntracked: false,
};

function readTheme(): "light" | "dark" {
  try {
    const stored = localStorage.getItem("goray-theme");
    if (stored === "light" || stored === "dark") {
      return stored;
    }
  } catch {
    // Fall back to the system preference.
  }
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export default function App() {
  const [source, setSource] = useState<ReportSource | null>(embeddedSource);
  const [liveStatus, setLiveStatus] = useState<LiveStatus>(
    embeddedSource === null ? "connecting" : embeddedSource.live ? "live" : "static",
  );
  const [retry, setRetry] = useState(0);
  const [theme, setTheme] = useState<"light" | "dark">(readTheme);
  const [view, setView] = useState<ViewMode>("graph");
  const [scope, setScope] = useState<GraphScope>("modules");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [focusId, setFocusId] = useState<string | null>(null);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [inspectorWidth, setInspectorWidth] = useState(384);

  const report = source?.report ?? null;
  const index = useMemo(() => (report ? buildEntityIndex(report) : null), [report]);
  const counts = useMemo(() => (report ? reportCounts(report) : EMPTY_COUNTS), [report]);

  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
    try {
      localStorage.setItem("goray-theme", theme);
    } catch {
      // Theme persistence is best effort.
    }
  }, [theme]);

  useEffect(() => {
    if (source !== null) {
      return;
    }
    const controller = new AbortController();
    let cancelled = false;
    fetchLiveReport(controller.signal)
      .then((next) => {
        if (cancelled) {
          return;
        }
        setSource({ report: next, live: true });
        setLiveStatus("live");
      })
      .catch(() => {
        if (!cancelled) {
          setLiveStatus("stale");
        }
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [retry, source === null]);

  useEffect(() => {
    if (source === null || !source.live) {
      return;
    }
    let cancelled = false;
    const timer = window.setInterval(() => {
      if (document.hidden) {
        return;
      }
      fetchLiveReport()
        .then((next) => {
          if (cancelled) {
            return;
          }
          setSource((previous) => (previous ? { ...previous, report: next } : { report: next, live: true }));
          setLiveStatus("live");
        })
        .catch(() => {
          if (!cancelled) {
            setLiveStatus("stale");
          }
        });
    }, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [source === null, source?.live]);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPaletteOpen((current) => !current);
        return;
      }
      if (event.key === "Escape" && !paletteOpen) {
        if (focusId !== null) {
          setFocusId(null);
        } else if (selectedId !== null) {
          setSelectedId(null);
        }
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [focusId, paletteOpen, selectedId]);

  const selectFromPalette = (id: string) => {
    setSelectedId(id);
    if (!index) {
      return;
    }
    if (index.graphNodeIds.has(id)) {
      setFocusId(id);
      if (view === "graph") {
        if (index.moduleNodeIds.has(id)) {
          setScope("modules");
        } else if (index.packageNodeIds.has(id)) {
          setScope("packages");
        }
      }
    }
  };

  if (!report || !index) {
    return (
      <div className="flex h-screen items-center justify-center px-6">
        <div className="w-full max-w-md rounded border border-zinc-200 bg-white p-5 dark:border-zinc-800 dark:bg-zinc-950">
          <div className="flex items-center gap-2">
            <span className="inline-flex h-5 w-5 items-center justify-center rounded-sm border border-zinc-300 bg-zinc-900 font-mono text-[10px] font-semibold text-white dark:border-zinc-700 dark:bg-zinc-100 dark:text-zinc-900">
              G
            </span>
            <span className="font-mono text-[11px] font-semibold">goray</span>
            {liveStatus === "stale" ? <AlertTriangle size={13} className="ml-auto text-amber-500" /> : null}
          </div>
          <h1 className="mt-3 text-sm font-semibold">No report payload</h1>
          <p className="mt-1 text-xs leading-5 text-zinc-500 dark:text-zinc-400">
            This build has no embedded data and could not reach <code className="font-mono">/api/report</code>.
            Start the local server, then retry.
          </p>
          <pre className="mt-3 overflow-x-auto rounded border border-zinc-200 bg-zinc-50 p-2 font-mono text-[10px] leading-5 text-zinc-600 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
{`go run ./cmd/goray serve .
cd web && npm run dev`}
          </pre>
          <button
            type="button"
            onClick={() => {
              setLiveStatus("connecting");
              setRetry((current) => current + 1);
            }}
            className="mt-3 inline-flex h-7 items-center gap-1.5 rounded border border-zinc-300 bg-white px-2 font-mono text-[10px] text-zinc-700 hover:bg-zinc-50 dark:border-zinc-700 dark:bg-zinc-950 dark:text-zinc-300 dark:hover:bg-zinc-900"
          >
            <RotateCcw size={11} />
            retry
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-zinc-50 text-zinc-900 dark:bg-zinc-950 dark:text-zinc-100">
      <Topbar
        modulePath={report.module_path}
        view={view}
        onView={setView}
        counts={counts}
        liveStatus={liveStatus}
        theme={theme}
        onToggleTheme={() => setTheme((current) => (current === "dark" ? "light" : "dark"))}
        onOpenPalette={() => setPaletteOpen(true)}
      />
      <MetricStrip counts={counts} />

      <div className="flex min-h-0 flex-1">
        <main className="min-w-0 flex-1 overflow-hidden">
          {view === "graph" ? (
            <GraphView
              report={report}
              index={index}
              scope={scope}
              onScope={setScope}
              selectedId={selectedId}
              focusId={focusId}
              onSelect={setSelectedId}
              onFocus={setFocusId}
            />
          ) : null}
          {view === "hotspots" ? (
            <HotspotsView report={report} index={index} onSelect={setSelectedId} />
          ) : null}
          {view === "interfaces" ? (
            <InterfacesView report={report} index={index} onSelect={setSelectedId} />
          ) : null}
          {view === "explorer" ? (
            <ExplorerView report={report} index={index} onSelect={setSelectedId} />
          ) : null}
        </main>

        {selectedId !== null ? (
          <Inspector
            selectedId={selectedId}
            entity={index.entitiesById.get(selectedId)}
            index={index}
            width={inspectorWidth}
            onWidth={setInspectorWidth}
            onClose={() => setSelectedId(null)}
            onSelect={setSelectedId}
          />
        ) : null}
      </div>

      <CommandPalette
        open={paletteOpen}
        report={report}
        onClose={() => setPaletteOpen(false)}
        onSelect={selectFromPalette}
      />
    </div>
  );
}
