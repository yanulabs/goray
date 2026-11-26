import { AlertTriangle, RotateCcw } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { CommandPalette } from "./components/CommandPalette";
import { ExplorerView } from "./components/ExplorerView";
import { GraphView } from "./components/GraphView";
import { HotspotsView } from "./components/HotspotsView";
import { Inspector } from "./components/Inspector";
import { InterfacesView } from "./components/InterfacesView";
import { MetricStrip } from "./components/MetricStrip";
import { Topbar, type LiveStatus } from "./components/Topbar";
import {
  buildEntityIndex,
  buildGraphNodeMeta,
  reportCounts,
  type GraphNodeMeta,
  type ReportCounts,
} from "./lib/derive";
import { fetchLiveReport, readEmbeddedReport, type ReportSource } from "./lib/data";
import {
  analyzeGraph,
  findHubs,
  type GraphAnalysis,
  type GraphFilterReason,
  type HiddenGraphNode,
} from "./lib/graphLayout";
import type { GraphScope, ReportGraph, ReportGraphNode, ViewMode } from "./types/report";

const embeddedSource = readEmbeddedReport();

const SIMPLIFY_KEY = "goray-graph-simplify";
const HIDE_HUBS_KEY = "goray-graph-hide-hubs";
const HIDE_UNTRACKED_KEY = "goray-graph-hide-untracked";
const HIDE_ISOLATED_KEY = "goray-graph-hide-isolated";
const HIDE_TEST_ONLY_KEY = "goray-graph-hide-test-only";

const EMPTY_GRAPH_ANALYSIS: GraphAnalysis = {
  edges: [],
  nodesInCycle: new Set<string>(),
  hiddenByNode: new Map<string, number>(),
  transitiveCount: 0,
};

const EMPTY_NODE_META: ReadonlyMap<string, GraphNodeMeta> = new Map();
const EMPTY_GRAPH_NODES: ReportGraphNode[] = [];
const EMPTY_IDS: ReadonlySet<string> = new Set();

function readStoredToggle(key: string): boolean | null {
  try {
    const stored = localStorage.getItem(key);
    if (stored === "1") {
      return true;
    }
    if (stored === "0") {
      return false;
    }
  } catch {
    // View-state persistence is best effort.
  }
  return null;
}

function writeStoredToggle(key: string, value: boolean | null): void {
  try {
    if (value === null) {
      localStorage.removeItem(key);
    } else {
      localStorage.setItem(key, value ? "1" : "0");
    }
  } catch {
    // View-state persistence is best effort.
  }
}

function isAbortError(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    "name" in error &&
    (error as { name?: unknown }).name === "AbortError"
  );
}

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
  const [simplifyPaths, setSimplifyPaths] = useState<boolean | null>(() =>
    readStoredToggle(SIMPLIFY_KEY),
  );
  const [hideHubs, setHideHubs] = useState<boolean>(
    () => readStoredToggle(HIDE_HUBS_KEY) ?? false,
  );
  const [hideUntracked, setHideUntracked] = useState<boolean | null>(() =>
    readStoredToggle(HIDE_UNTRACKED_KEY),
  );
  const [hideIsolated, setHideIsolated] = useState<boolean | null>(() =>
    readStoredToggle(HIDE_ISOLATED_KEY),
  );
  const [hideTestOnly, setHideTestOnly] = useState<boolean | null>(() =>
    readStoredToggle(HIDE_TEST_ONLY_KEY),
  );
  const [unhiddenNodes, setUnhiddenNodes] = useState<Set<string>>(() => new Set());

  const etagRef = useRef<string | null>(null);
  const pollAbortRef = useRef<AbortController | null>(null);
  const pollInFlightRef = useRef(false);

  const report = source?.report ?? null;
  const index = useMemo(() => (report ? buildEntityIndex(report) : null), [report]);
  const counts = useMemo(() => (report ? reportCounts(report) : EMPTY_COUNTS), [report]);
  const scopeGraph = useMemo<ReportGraph | null>(() => {
    if (!report) {
      return null;
    }
    return scope === "modules" ? report.graphs.modules : report.graphs.packages;
  }, [report, scope]);
  const nodeMeta = useMemo<ReadonlyMap<string, GraphNodeMeta>>(() => {
    if (!report || !scopeGraph) {
      return EMPTY_NODE_META;
    }
    return buildGraphNodeMeta(report, scope, scopeGraph);
  }, [report, scope, scopeGraph]);
  const graphHubs = useMemo(() => (scopeGraph ? findHubs(scopeGraph) : []), [scopeGraph]);
  const untrackedNodes = useMemo(
    () => scopeGraph?.nodes.filter((node) => node.untracked) ?? EMPTY_GRAPH_NODES,
    [scopeGraph],
  );
  const connectedNodeIds = useMemo<ReadonlySet<string>>(() => {
    if (!scopeGraph) {
      return EMPTY_IDS;
    }
    const ids = new Set<string>();
    for (const edge of scopeGraph.edges) {
      ids.add(edge.from);
      ids.add(edge.to);
    }
    return ids;
  }, [scopeGraph]);
  const isolatedNodes = useMemo(
    () =>
      scopeGraph?.nodes.filter((node) => !connectedNodeIds.has(node.id)) ?? EMPTY_GRAPH_NODES,
    [scopeGraph, connectedNodeIds],
  );
  const testOnlyNodes = useMemo(
    () =>
      scopeGraph?.nodes.filter((node) => nodeMeta.get(node.id)?.testOnly === true) ??
      EMPTY_GRAPH_NODES,
    [scopeGraph, nodeMeta],
  );
  const hasTrackedNodes = scopeGraph?.nodes.some((node) => !node.untracked) ?? false;
  const hasConnectedNodes = connectedNodeIds.size > 0;
  // Defaults follow the payload: hide untracked work only while some tracked
  // node remains, hide isolated nodes only when a connected graph exists, and
  // hide test-only nodes only when this scope actually contains one.
  const hideUntrackedActive =
    untrackedNodes.length > 0 && (hideUntracked ?? hasTrackedNodes);
  const hideIsolatedActive = isolatedNodes.length > 0 && (hideIsolated ?? hasConnectedNodes);
  const hideTestOnlyActive = testOnlyNodes.length > 0 && (hideTestOnly ?? true);

  const hidden = useMemo(() => {
    const reasonsByID = new Map<string, GraphFilterReason[]>();
    const mark = (
      nodes: readonly ReportGraphNode[],
      reason: GraphFilterReason,
      active: boolean,
    ) => {
      if (!active) {
        return;
      }
      for (const node of nodes) {
        if (unhiddenNodes.has(node.id)) {
          continue;
        }
        const reasons = reasonsByID.get(node.id);
        if (reasons) {
          reasons.push(reason);
        } else {
          reasonsByID.set(node.id, [reason]);
        }
      }
    };
    mark(
      graphHubs.map((hub) => hub.node),
      "hub",
      hideHubs,
    );
    mark(untrackedNodes, "untracked", hideUntrackedActive);
    mark(isolatedNodes, "isolated", hideIsolatedActive);
    mark(testOnlyNodes, "testOnly", hideTestOnlyActive);

    const nodes: HiddenGraphNode[] = [];
    const counts: Record<GraphFilterReason, number> = {
      hub: 0,
      untracked: 0,
      isolated: 0,
      testOnly: 0,
    };
    for (const node of scopeGraph?.nodes ?? EMPTY_GRAPH_NODES) {
      const reasons = reasonsByID.get(node.id);
      if (!reasons) {
        continue;
      }
      nodes.push({ node, reasons });
      for (const reason of reasons) {
        counts[reason] += 1;
      }
    }
    nodes.sort((left, right) => left.node.label.localeCompare(right.node.label));
    return {
      ids: new Set(reasonsByID.keys()),
      nodes,
      counts,
    };
  }, [
    graphHubs,
    hideHubs,
    untrackedNodes,
    hideUntrackedActive,
    isolatedNodes,
    hideIsolatedActive,
    testOnlyNodes,
    hideTestOnlyActive,
    scopeGraph,
    unhiddenNodes,
  ]);

  // Transitive reduction, cycle marking, and the Inspector's hidden-edge
  // count all describe the graph that is actually on screen. Recompute them
  // after filtered nodes are removed so an edge whose alternate path ran
  // through a hidden node is not reported (or removed) as redundant.
  const visibleGraph = useMemo<ReportGraph | null>(() => {
    if (!scopeGraph || hidden.ids.size === 0) {
      return scopeGraph;
    }
    return {
      nodes: scopeGraph.nodes.filter((node) => !hidden.ids.has(node.id)),
      edges: scopeGraph.edges.filter(
        (edge) => !hidden.ids.has(edge.from) && !hidden.ids.has(edge.to),
      ),
    };
  }, [scopeGraph, hidden.ids]);
  const graphAnalysis = useMemo<GraphAnalysis>(
    () => (visibleGraph ? analyzeGraph(visibleGraph) : EMPTY_GRAPH_ANALYSIS),
    [visibleGraph],
  );
  const graphEdgeCount = scopeGraph?.edges.length ?? 0;
  const simplifyEnabled = simplifyPaths ?? graphEdgeCount > 15;
  const filterCounts = useMemo(
    () => ({
      hubs: graphHubs.length,
      untracked: untrackedNodes.length,
      isolated: isolatedNodes.length,
      testOnly: testOnlyNodes.length,
    }),
    [graphHubs.length, untrackedNodes.length, isolatedNodes.length, testOnlyNodes.length],
  );
  const hiddenDirectForSelection =
    simplifyEnabled && selectedId !== null
      ? graphAnalysis.edges.reduce(
          (count, edge) =>
            count +
            (edge.transitive && (edge.from === selectedId || edge.to === selectedId) ? 1 : 0),
          0,
        )
      : 0;

  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
    try {
      localStorage.setItem("goray-theme", theme);
    } catch {
      // Theme persistence is best effort.
    }
  }, [theme]);

  useEffect(() => {
    writeStoredToggle(SIMPLIFY_KEY, simplifyPaths);
  }, [simplifyPaths]);

  useEffect(() => {
    writeStoredToggle(HIDE_HUBS_KEY, hideHubs);
  }, [hideHubs]);

  useEffect(() => {
    writeStoredToggle(HIDE_UNTRACKED_KEY, hideUntracked);
  }, [hideUntracked]);

  useEffect(() => {
    writeStoredToggle(HIDE_ISOLATED_KEY, hideIsolated);
  }, [hideIsolated]);

  useEffect(() => {
    writeStoredToggle(HIDE_TEST_ONLY_KEY, hideTestOnly);
  }, [hideTestOnly]);

  // Manual unhides are a temporary escape hatch from the active filters. Reset
  // them when the scope or the filter state itself changes.
  useEffect(() => {
    setUnhiddenNodes(new Set());
  }, [scope, hideHubs, hideUntrackedActive, hideIsolatedActive, hideTestOnlyActive]);

  useEffect(() => {
    if (source !== null) {
      return;
    }
    const controller = new AbortController();
    let cancelled = false;
    fetchLiveReport({ signal: controller.signal })
      .then((result) => {
        if (cancelled || result === null) {
          return;
        }
        etagRef.current = result.etag;
        setSource({ report: result.report, live: true });
        setLiveStatus("live");
      })
      .catch((error) => {
        if (!cancelled && !isAbortError(error)) {
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
    const poll = () => {
      if (document.hidden || pollInFlightRef.current) {
        return;
      }
      const controller = new AbortController();
      pollInFlightRef.current = true;
      pollAbortRef.current = controller;
      fetchLiveReport({ etag: etagRef.current, signal: controller.signal })
        .then((result) => {
          // 304 Not Modified: the report is unchanged, so skip every state
          // update and avoid recomputing derived graph views.
          if (cancelled || result === null) {
            return;
          }
          etagRef.current = result.etag;
          setSource((previous) =>
            previous ? { ...previous, report: result.report } : { report: result.report, live: true },
          );
          setLiveStatus("live");
        })
        .catch((error) => {
          if (!cancelled && !isAbortError(error)) {
            setLiveStatus("stale");
          }
        })
        .finally(() => {
          if (pollAbortRef.current === controller) {
            pollAbortRef.current = null;
            pollInFlightRef.current = false;
          }
        });
    };
    const timer = window.setInterval(poll, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
      pollAbortRef.current?.abort();
      pollAbortRef.current = null;
      pollInFlightRef.current = false;
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
              analysis={graphAnalysis}
              hubs={graphHubs}
              nodeMeta={nodeMeta}
              filterCounts={filterCounts}
              hiddenCounts={hidden.counts}
              simplifyPaths={simplifyEnabled}
              onSimplifyPaths={setSimplifyPaths}
              hideHubs={hideHubs}
              onHideHubs={setHideHubs}
              hideUntracked={hideUntrackedActive}
              onHideUntracked={setHideUntracked}
              hideIsolated={hideIsolatedActive}
              onHideIsolated={setHideIsolated}
              hideTestOnly={hideTestOnlyActive}
              onHideTestOnly={setHideTestOnly}
              hiddenNodeIds={hidden.ids}
              hiddenNodes={hidden.nodes}
              onUnhideNode={(id) => {
                setUnhiddenNodes((current) => {
                  const next = new Set(current);
                  next.add(id);
                  return next;
                });
              }}
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
            hiddenDirectEdges={hiddenDirectForSelection}
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
