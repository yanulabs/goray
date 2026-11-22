import {
  ArrowDown,
  ArrowRight,
  Maximize2,
  Minus,
  Plus,
  RotateCcw,
  X,
} from "lucide-react";
import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import type { EntityIndex, GraphNodeMeta } from "../lib/derive";
import { clamp, kindLabel } from "../lib/format";
import {
  graphSignature,
  layoutGraph,
  nodeVisual,
  polylineMidpoint,
  roundedEdgePath,
  type EdgePoint,
  type GraphAnalysis,
  type GraphFilterReason,
  type GraphHub,
  type GraphLayout,
  type HiddenGraphNode,
  type PositionedEdge,
} from "../lib/graphLayout";
import type {
  GraphScope,
  RankDirection,
  Report,
} from "../types/report";
import { EmptyState, IconButton, Segmented } from "./ui";

interface Transform {
  x: number;
  y: number;
  k: number;
}

type EdgeTone = "muted" | "unrelated" | "hover" | "outgoing" | "incoming" | "cycle";

const MIN_ZOOM = 0.15;
const MAX_ZOOM = 3;
const FIT_PADDING = 24;

const FILTER_REASON_LABEL: Record<GraphFilterReason, string> = {
  hub: "hub",
  untracked: "untracked",
  isolated: "isolated",
  testOnly: "test-only",
};

function fitTransform(layout: GraphLayout, width: number, height: number): Transform {
  const padding = FIT_PADDING;
  const scale = clamp(
    Math.min((width - padding * 2) / layout.width, (height - padding * 2) / layout.height),
    0.2,
    1.35,
  );
  return {
    k: scale,
    x: (width - layout.width * scale) / 2,
    y: (height - layout.height * scale) / 2,
  };
}

function edgeTone(
  edge: PositionedEdge,
  activeNodeId: string | null,
  activeEdges: Set<string> | undefined,
): EdgeTone {
  if (edge.cycle) {
    return "cycle";
  }
  if (activeNodeId === null) {
    return "muted";
  }
  if (activeEdges === undefined || !activeEdges.has(edge.id)) {
    return "unrelated";
  }
  return edge.from === activeNodeId ? "outgoing" : "incoming";
}

function ToolbarToggle({
  label,
  active,
  onClick,
  badge,
  title,
  disabled = false,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
  badge?: string | null;
  title: string;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      title={title}
      aria-label={title}
      aria-pressed={active}
      disabled={disabled}
      onClick={onClick}
      className={`inline-flex h-7 items-center gap-1 rounded border px-1.5 font-mono text-[10px] transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${
        active
          ? "border-indigo-400 bg-indigo-50 text-indigo-700 dark:border-indigo-700 dark:bg-indigo-950 dark:text-indigo-300"
          : "border-zinc-200 bg-white text-zinc-600 hover:border-zinc-300 hover:bg-zinc-100 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400 dark:hover:border-zinc-700 dark:hover:bg-zinc-900"
      }`}
    >
      <span>{label}</span>
      {badge ? (
        <span
          className={`rounded px-1 py-px text-[9px] tabular-nums ${
            active
              ? "bg-indigo-100 text-indigo-700 dark:bg-indigo-900 dark:text-indigo-200"
              : "bg-zinc-100 text-zinc-500 dark:bg-zinc-900 dark:text-zinc-400"
          }`}
        >
          {badge}
        </span>
      ) : null}
    </button>
  );
}

export function GraphView({
  report,
  index,
  scope,
  onScope,
  selectedId,
  focusId,
  onSelect,
  onFocus,
  analysis,
  hubs,
  nodeMeta,
  filterCounts,
  hiddenCounts,
  simplifyPaths,
  onSimplifyPaths,
  hideHubs,
  onHideHubs,
  hideUntracked,
  onHideUntracked,
  hideIsolated,
  onHideIsolated,
  hideTestOnly,
  onHideTestOnly,
  hiddenNodeIds,
  hiddenNodes,
  onUnhideNode,
}: {
  report: Report;
  index: EntityIndex;
  scope: GraphScope;
  onScope: (scope: GraphScope) => void;
  selectedId: string | null;
  focusId: string | null;
  onSelect: (id: string) => void;
  onFocus: (id: string | null) => void;
  analysis: GraphAnalysis;
  hubs: GraphHub[];
  nodeMeta: ReadonlyMap<string, GraphNodeMeta>;
  filterCounts: {
    hubs: number;
    untracked: number;
    isolated: number;
    testOnly: number;
  };
  hiddenCounts: Record<GraphFilterReason, number>;
  simplifyPaths: boolean;
  onSimplifyPaths: (value: boolean | null) => void;
  hideHubs: boolean;
  onHideHubs: (value: boolean) => void;
  hideUntracked: boolean;
  onHideUntracked: (value: boolean) => void;
  hideIsolated: boolean;
  onHideIsolated: (value: boolean) => void;
  hideTestOnly: boolean;
  onHideTestOnly: (value: boolean) => void;
  hiddenNodeIds: ReadonlySet<string>;
  hiddenNodes: HiddenGraphNode[];
  onUnhideNode: (id: string) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [direction, setDirection] = useState<RankDirection>("LR");
  const [view, setView] = useState<Transform>({ x: 0, y: 0, k: 1 });
  const [panning, setPanning] = useState(false);
  const [hoveredNodeId, setHoveredNodeId] = useState<string | null>(null);
  const [hoveredEdgeId, setHoveredEdgeId] = useState<string | null>(null);
  const [layoutState, setLayoutState] = useState<{
    key: string;
    layout: GraphLayout;
  } | null>(null);
  const [layoutError, setLayoutError] = useState<string | null>(null);
  const drag = useRef<{
    x: number;
    y: number;
    startX: number;
    startY: number;
    moved: boolean;
  } | null>(null);
  const fittedKey = useRef("");
  const fittedSize = useRef({ width: 0, height: 0 });
  const userMoved = useRef(false);
  const requestedKey = useRef<string | null>(null);

  const graph = scope === "modules" ? report.graphs.modules : report.graphs.packages;
  const graphKey = useMemo(() => graphSignature(graph, nodeMeta), [graph, nodeMeta]);
  const hideHubsActive = hideHubs && hubs.length > 0;
  const hiddenIdKey = useMemo(() => [...hiddenNodeIds].sort().join(","), [hiddenNodeIds]);
  const layoutKey = useMemo(
    () =>
      `${graphKey}:${scope}:${direction}:${simplifyPaths ? 1 : 0}:${
        hideHubsActive ? 1 : 0
      }:${hiddenIdKey}`,
    [graphKey, scope, direction, simplifyPaths, hideHubsActive, hiddenIdKey],
  );

  const layout =
    layoutState !== null && layoutState.key === layoutKey ? layoutState.layout : null;
  const visuals = useMemo(() => {
    const byID = new Map<string, ReturnType<typeof nodeVisual>>();
    if (!layout) {
      return byID;
    }
    for (const positioned of layout.nodes) {
      byID.set(positioned.node.id, nodeVisual(positioned.node, nodeMeta.get(positioned.node.id)));
    }
    return byID;
  }, [layout, nodeMeta]);

  useLayoutEffect(() => {
    const element = containerRef.current;
    if (!element) {
      return;
    }
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (entry) {
        setSize({ width: entry.contentRect.width, height: entry.contentRect.height });
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (requestedKey.current === layoutKey) {
      return;
    }
    requestedKey.current = layoutKey;
    let cancelled = false;
    setLayoutState(null);
    setLayoutError(null);
    layoutGraph(graph, analysis, direction, {
      simplifyPaths,
      hiddenNodeIds,
      nodeMeta,
    })
      .then((next) => {
        if (!cancelled) {
          setLayoutState({ key: layoutKey, layout: next });
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setLayoutError(error instanceof Error ? error.message : "Layout failed");
        }
      });
    return () => {
      cancelled = true;
      if (requestedKey.current === layoutKey) {
        requestedKey.current = null;
      }
    };
    // layoutKey folds in the graph content signature, direction, declutter
    // toggles, and hidden hub IDs; identity-only report refreshes must not
    // trigger a relayout.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layoutKey]);

  useEffect(() => {
    if (!layout || size.width <= 0 || size.height <= 0) {
      return;
    }
    const sameLayout = fittedKey.current === layoutKey;
    const sameSize =
      fittedSize.current.width === size.width && fittedSize.current.height === size.height;
    if (sameLayout && (sameSize || userMoved.current)) {
      return;
    }
    fittedKey.current = layoutKey;
    fittedSize.current = { width: size.width, height: size.height };
    userMoved.current = false;
    setView(fitTransform(layout, size.width, size.height));
  }, [layout, layoutKey, size.width, size.height]);

  useEffect(() => {
    setHoveredNodeId(null);
    setHoveredEdgeId(null);
  }, [layoutKey]);

  useEffect(() => {
    const element = containerRef.current;
    if (!element) {
      return;
    }
    const handleWheel = (event: WheelEvent) => {
      event.preventDefault();
      const rect = element.getBoundingClientRect();
      const mouseX = event.clientX - rect.left;
      const mouseY = event.clientY - rect.top;
      userMoved.current = true;
      setView((previous) => {
        const nextScale = clamp(
          previous.k * Math.exp(-event.deltaY * 0.0015),
          MIN_ZOOM,
          MAX_ZOOM,
        );
        const ratio = nextScale / previous.k;
        return {
          k: nextScale,
          x: mouseX - (mouseX - previous.x) * ratio,
          y: mouseY - (mouseY - previous.y) * ratio,
        };
      });
    };
    element.addEventListener("wheel", handleWheel, { passive: false });
    return () => element.removeEventListener("wheel", handleWheel);
  }, []);

  const activeNodeId = useMemo(() => {
    const candidate = hoveredNodeId ?? selectedId ?? focusId;
    if (candidate === null || !layout) {
      return null;
    }
    return layout.nodes.some((node) => node.node.id === candidate) ? candidate : null;
  }, [hoveredNodeId, selectedId, focusId, layout]);

  const activeState = useMemo(() => {
    if (activeNodeId === null || !layout) {
      return null;
    }
    const nodes = new Set<string>([activeNodeId]);
    const edges = new Set<string>();
    for (const edge of layout.edges) {
      if (edge.from === activeNodeId || edge.to === activeNodeId) {
        edges.add(edge.id);
        nodes.add(edge.from === activeNodeId ? edge.to : edge.from);
      }
    }
    return { nodes, edges };
  }, [activeNodeId, layout]);

  useEffect(() => {
    if (selectedId === null || !layout || size.width <= 0 || size.height <= 0) {
      return;
    }
    const selected = layout.nodes.find((node) => node.node.id === selectedId);
    if (!selected) {
      return;
    }
    const margin = 48;
    setView((previous) => {
      const left = selected.x * previous.k + previous.x;
      const right = (selected.x + selected.width) * previous.k + previous.x;
      const top = selected.y * previous.k + previous.y;
      const bottom = (selected.y + selected.height) * previous.k + previous.y;
      let deltaX = 0;
      let deltaY = 0;
      if (left < margin) {
        deltaX = margin - left;
      } else if (right > size.width - margin) {
        deltaX = size.width - margin - right;
      }
      if (top < margin) {
        deltaY = margin - top;
      } else if (bottom > size.height - margin) {
        deltaY = size.height - margin - bottom;
      }
      if (deltaX === 0 && deltaY === 0) {
        return previous;
      }
      return { ...previous, x: previous.x + deltaX, y: previous.y + deltaY };
    });
  }, [selectedId, size.width, size.height, layout]);

  const zoomBy = (factor: number) => {
    userMoved.current = true;
    setView((previous) => {
      const nextScale = clamp(previous.k * factor, MIN_ZOOM, MAX_ZOOM);
      const ratio = nextScale / previous.k;
      const centerX = size.width / 2;
      const centerY = size.height / 2;
      return {
        k: nextScale,
        x: centerX - (centerX - previous.x) * ratio,
        y: centerY - (centerY - previous.y) * ratio,
      };
    });
  };

  const handlePointerDown = (event: React.PointerEvent<SVGSVGElement>) => {
    if (event.button !== 0) {
      return;
    }
    drag.current = {
      x: event.clientX,
      y: event.clientY,
      startX: event.clientX,
      startY: event.clientY,
      moved: false,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
    setPanning(true);
  };

  const handlePointerMove = (event: React.PointerEvent<SVGSVGElement>) => {
    const current = drag.current;
    if (!current) {
      return;
    }
    const deltaX = event.clientX - current.x;
    const deltaY = event.clientY - current.y;
    if (Math.abs(event.clientX - current.startX) + Math.abs(event.clientY - current.startY) > 3) {
      current.moved = true;
    }
    current.x = event.clientX;
    current.y = event.clientY;
    if (current.moved) {
      userMoved.current = true;
      setView((previous) => ({ ...previous, x: previous.x + deltaX, y: previous.y + deltaY }));
    }
  };

  const endPan = (event: React.PointerEvent<SVGSVGElement>) => {
    const current = drag.current;
    drag.current = null;
    setPanning(false);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    if (current && !current.moved) {
      onFocus(null);
    }
  };

  const labels = useMemo(
    () => new Map((layout?.nodes ?? []).map((node) => [node.node.id, node.node.label])),
    [layout],
  );

  const orderedEdges = useMemo(() => {
    if (!layout) {
      return [];
    }
    const rank = (edge: PositionedEdge): number => {
      if (hoveredEdgeId === edge.id) {
        return 3;
      }
      if (edge.cycle) {
        return 1;
      }
      if (activeState?.edges.has(edge.id)) {
        return 2;
      }
      return 0;
    };
    return [...layout.edges].sort(
      (left, right) => rank(left) - rank(right) || left.id.localeCompare(right.id),
    );
  }, [layout, activeState, hoveredEdgeId]);

  if (graph.nodes.length === 0) {
    return (
      <div className="h-full overflow-hidden">
        <EmptyState
          title="No graph nodes"
          detail="The index produced no nodes for this scope. Re-run the index or switch scope."
        />
      </div>
    );
  }

  const focusPresent =
    focusId !== null && (layout?.nodes.some((node) => node.node.id === focusId) ?? false);
  const focusLabel =
    focusId !== null
      ? index.entitiesById.get(focusId)?.name ?? focusId.replace(/^(mod|pkg):/, "")
      : "";
  const hiddenDirectForSelection =
    simplifyPaths && selectedId !== null
      ? analysis.edges.reduce(
          (count, edge) =>
            count +
            (edge.transitive && (edge.from === selectedId || edge.to === selectedId) ? 1 : 0),
          0,
        )
      : 0;
  const simplifyBadge = simplifyPaths
    ? `${analysis.transitiveCount} transitive ${
        analysis.transitiveCount === 1 ? "edge" : "edges"
      } hidden`
    : analysis.transitiveCount > 0
      ? `${analysis.transitiveCount} reducible`
      : null;
  const hubBadge =
    filterCounts.hubs === 0
      ? null
      : hideHubsActive
        ? `${hiddenCounts.hub} hidden`
        : `${filterCounts.hubs} ${filterCounts.hubs === 1 ? "hub" : "hubs"}`;
  const untrackedBadge =
    filterCounts.untracked === 0
      ? null
      : hideUntracked
        ? `${hiddenCounts.untracked} hidden`
        : `${filterCounts.untracked} untracked`;
  const isolatedBadge =
    filterCounts.isolated === 0
      ? null
      : hideIsolated
        ? `${hiddenCounts.isolated} hidden`
        : `${filterCounts.isolated} isolated`;
  const testOnlyBadge =
    filterCounts.testOnly === 0
      ? null
      : hideTestOnly
        ? `${hiddenCounts.testOnly} hidden`
        : `${filterCounts.testOnly} test-only`;
  const weightUnit = scope === "modules" ? "symbol refs" : "imports";

  return (
    <div ref={containerRef} className="relative h-full min-w-0 overflow-hidden">
      {layout && layout.nodes.length > 0 ? (
        <svg
          width={size.width}
          height={size.height}
          className={`block h-full w-full touch-none ${panning ? "cursor-grabbing" : "cursor-grab"}`}
          onPointerDown={handlePointerDown}
          onPointerMove={handlePointerMove}
          onPointerUp={endPan}
          onPointerCancel={endPan}
          onPointerLeave={() => {
            setHoveredNodeId(null);
            setHoveredEdgeId(null);
          }}
          role="application"
          aria-label="Dependency graph. Drag to pan, scroll to zoom, select a node to inspect it."
        >
          <defs>
            <marker
              id="goray-arrow-muted"
              viewBox="0 0 8 8"
              refX="7"
              refY="4"
              markerWidth="7"
              markerHeight="7"
              orient="auto-start-reverse"
            >
              <path d="M 0 0 L 8 4 L 0 8 z" className="fill-zinc-400 dark:fill-zinc-500" />
            </marker>
            <marker
              id="goray-arrow-out"
              viewBox="0 0 8 8"
              refX="7"
              refY="4"
              markerWidth="7"
              markerHeight="7"
              orient="auto-start-reverse"
            >
              <path d="M 0 0 L 8 4 L 0 8 z" className="fill-indigo-500" />
            </marker>
            <marker
              id="goray-arrow-in"
              viewBox="0 0 8 8"
              refX="7"
              refY="4"
              markerWidth="7"
              markerHeight="7"
              orient="auto-start-reverse"
            >
              <path d="M 0 0 L 8 4 L 0 8 z" className="fill-emerald-500" />
            </marker>
            <marker
              id="goray-arrow-cycle"
              viewBox="0 0 8 8"
              refX="7"
              refY="4"
              markerWidth="7"
              markerHeight="7"
              orient="auto-start-reverse"
            >
              <path d="M 0 0 L 8 4 L 0 8 z" className="fill-red-500" />
            </marker>
          </defs>

          <g transform={`translate(${view.x} ${view.y}) scale(${view.k})`}>
            <g fill="none">
              {orderedEdges.map((edge) => {
                const baseTone = edgeTone(edge, activeNodeId, activeState?.edges);
                const hovered = hoveredEdgeId === edge.id;
                const tone: EdgeTone =
                  hovered && (baseTone === "muted" || baseTone === "unrelated")
                    ? "hover"
                    : baseTone;
                const active = activeState?.edges.has(edge.id) ?? false;
                const path = roundedEdgePath(edge.points);
                const labelPoint: EdgePoint | null = hovered
                  ? polylineMidpoint(edge.points)
                  : null;
                const strokeClass =
                  tone === "outgoing"
                    ? "stroke-indigo-500"
                    : tone === "incoming"
                      ? "stroke-emerald-500"
                      : tone === "cycle"
                        ? "stroke-red-500"
                        : tone === "hover"
                          ? "stroke-zinc-700 dark:stroke-zinc-200"
                          : "stroke-zinc-400 dark:stroke-zinc-500";
                const opacityClass =
                  tone === "unrelated"
                    ? "opacity-[0.12]"
                    : tone === "cycle"
                      ? "opacity-90"
                      : tone === "muted"
                        ? "opacity-[0.55] dark:opacity-[0.42]"
                        : "opacity-100";
                const strokeWidth =
                  tone === "outgoing" || tone === "incoming" || tone === "hover"
                    ? 2
                    : tone === "cycle"
                      ? active
                        ? 2
                        : 1.75
                      : 1.35;
                const marker =
                  tone === "outgoing"
                    ? "url(#goray-arrow-out)"
                    : tone === "incoming"
                      ? "url(#goray-arrow-in)"
                      : tone === "cycle"
                        ? "url(#goray-arrow-cycle)"
                        : "url(#goray-arrow-muted)";
                const fromLabel = labels.get(edge.from) ?? edge.from;
                const toLabel = labels.get(edge.to) ?? edge.to;
                const labelText = `${edge.weight} ${weightUnit}`;
                const labelWidth = Math.max(42, labelText.length * 5.2 + 10);
                return (
                  <g key={edge.id}>
                    <path
                      d={path}
                      strokeWidth={4.5}
                      vectorEffect="non-scaling-stroke"
                      pointerEvents="none"
                      className="stroke-zinc-50 dark:stroke-zinc-950"
                    />
                    <path
                      d={path}
                      className={`${strokeClass} ${opacityClass}`}
                      strokeWidth={strokeWidth}
                      strokeDasharray={edge.cycle ? "4 3" : undefined}
                      markerEnd={marker}
                      vectorEffect="non-scaling-stroke"
                      pointerEvents="none"
                    />
                    <path
                      d={path}
                      stroke="transparent"
                      strokeWidth={14}
                      vectorEffect="non-scaling-stroke"
                      pointerEvents="stroke"
                      onPointerEnter={() => setHoveredEdgeId(edge.id)}
                      onPointerLeave={() =>
                        setHoveredEdgeId((current) => (current === edge.id ? null : current))
                      }
                    >
                      <title>
                        {`${fromLabel} → ${toLabel}\n${edge.weight} ${weightUnit}${
                          edge.cycle ? " · cycle" : ""
                        }${edge.transitive ? " · redundant path" : ""}`}
                      </title>
                    </path>
                    {labelPoint !== null ? (
                      <g
                        transform={`translate(${labelPoint.x} ${labelPoint.y})`}
                        pointerEvents="none"
                      >
                        <rect
                          x={-labelWidth / 2}
                          y={-8}
                          width={labelWidth}
                          height={16}
                          rx={3}
                          strokeWidth={0.75}
                          vectorEffect="non-scaling-stroke"
                          className="fill-zinc-50 stroke-zinc-300 dark:fill-zinc-950 dark:stroke-zinc-700"
                        />
                        <text
                          y={3}
                          textAnchor="middle"
                          className="fill-zinc-700 font-mono dark:fill-zinc-300"
                          style={{ fontSize: 9 }}
                        >
                          {labelText}
                        </text>
                      </g>
                    ) : null}
                  </g>
                );
              })}
            </g>

            {layout.nodes.map(({ node, x, y, width, height, inCycle }) => {
              const visible =
                activeState === null || activeState.nodes.has(node.id);
              const selected = selectedId === node.id;
              const hovered = hoveredNodeId === node.id;
              const hiddenDirect = simplifyPaths
                ? analysis.hiddenByNode.get(node.id) ?? 0
                : 0;
              const visual = visuals.get(node.id) ?? nodeVisual(node, nodeMeta.get(node.id));
              return (
                <g
                  key={node.id}
                  transform={`translate(${x} ${y})`}
                  opacity={visible ? 1 : 0.12}
                  role="button"
                  tabIndex={0}
                  aria-label={`${kindLabel(node.kind)} ${node.label}`}
                  aria-pressed={selected}
                  className="cursor-pointer outline-none"
                  onPointerDown={(event) => event.stopPropagation()}
                  onPointerEnter={() => setHoveredNodeId(node.id)}
                  onPointerLeave={() =>
                    setHoveredNodeId((current) => (current === node.id ? null : current))
                  }
                  onClick={(event) => {
                    event.stopPropagation();
                    onFocus(node.id);
                    onSelect(node.id);
                  }}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") {
                      event.preventDefault();
                      onFocus(node.id);
                      onSelect(node.id);
                    }
                  }}
                >
                  <rect
                    width={width}
                    height={height}
                    rx={4}
                    strokeWidth={selected ? 2 : hovered ? 1.5 : 1}
                    vectorEffect="non-scaling-stroke"
                    className={
                      selected
                        ? "fill-white stroke-indigo-500 dark:fill-zinc-900"
                        : hovered
                          ? "fill-white stroke-indigo-400 dark:fill-zinc-900 dark:stroke-indigo-500"
                          : inCycle
                            ? "fill-white stroke-red-400 dark:fill-zinc-900 dark:stroke-red-700"
                            : "fill-white stroke-zinc-300 dark:fill-zinc-900 dark:stroke-zinc-700"
                    }
                  />
                  {selected ? (
                    <rect
                      x={1}
                      y={1}
                      width={width - 2}
                      height={height - 2}
                      rx={3}
                      fill="none"
                      className="stroke-indigo-200 dark:stroke-indigo-900"
                      strokeWidth={1}
                      vectorEffect="non-scaling-stroke"
                    />
                  ) : null}
                  <text
                    x={10}
                    y={19}
                    className="fill-zinc-800 font-mono font-medium dark:fill-zinc-100"
                    style={{ fontSize: 11 }}
                  >
                    {visual.label}
                  </text>
                  <text
                    x={10}
                    y={35}
                    className="fill-zinc-500 font-mono tabular-nums dark:fill-zinc-400"
                    style={{ fontSize: 9 }}
                  >
                    {visual.metrics}
                  </text>
                  {visual.badges.map((badge) => {
                    const testOnly = badge.label === "test-only";
                    return (
                      <g key={badge.label}>
                        <rect
                          x={badge.x}
                          y={6}
                          width={badge.width}
                          height={12}
                          rx={2}
                          className={
                            testOnly
                              ? "fill-indigo-50 stroke-indigo-400 dark:fill-indigo-950 dark:stroke-indigo-700"
                              : "fill-amber-50 stroke-amber-400 dark:fill-amber-950 dark:stroke-amber-800"
                          }
                          strokeWidth={0.5}
                          vectorEffect="non-scaling-stroke"
                        />
                        <text
                          x={badge.x + badge.width / 2}
                          y={15}
                          textAnchor="middle"
                          className={
                            testOnly
                              ? "fill-indigo-700 font-mono dark:fill-indigo-300"
                              : "fill-amber-700 font-mono dark:fill-amber-300"
                          }
                          style={{ fontSize: 7 }}
                        >
                          {badge.label}
                        </text>
                      </g>
                    );
                  })}
                  <title>
                    {`${node.label} · ${kindLabel(node.kind)} · ${
                      visual.testOnly ? `${visual.testLoc} test LOC` : `${node.loc} LOC`
                    } · fan-in ${node.fan_in} · fan-out ${node.fan_out}${
                      visual.testOnly && node.loc > 0 ? ` · ${node.loc} prod LOC` : ""
                    }${
                      hiddenDirect > 0
                        ? ` · ${hiddenDirect} redundant direct ${
                            hiddenDirect === 1 ? "dependency" : "dependencies"
                          } hidden`
                        : ""
                    }`}
                  </title>
                </g>
              );
            })}
          </g>
        </svg>
      ) : (
        <div className="flex h-full items-center justify-center px-6">
          {layoutError ? (
            <div className="max-w-md text-center">
              <div className="font-mono text-[11px] uppercase tracking-widest text-red-500">
                Layout failed
              </div>
              <p className="mt-1 font-mono text-[10px] leading-5 text-zinc-500 dark:text-zinc-400">
                {layoutError}
              </p>
            </div>
          ) : layout && layout.nodes.length === 0 ? (
            <EmptyState
              title="All nodes hidden"
              detail="Every node in this scope is hidden by the active graph filters. Turn a filter off or unhide a node below to bring it back."
            />
          ) : (
            <span className="font-mono text-[10px] uppercase tracking-widest text-zinc-400 dark:text-zinc-500">
              laying out…
            </span>
          )}
        </div>
      )}

      <div className="absolute right-2 top-2 flex max-w-[calc(100%-1rem)] flex-wrap items-center justify-end gap-1 rounded border border-zinc-200 bg-white p-1 dark:border-zinc-800 dark:bg-zinc-950">
        <Segmented<GraphScope>
          compact
          value={scope}
          onChange={onScope}
          options={[
            { value: "modules", label: "Modules" },
            { value: "packages", label: "Packages" },
          ]}
        />
        <span className="mx-0.5 h-4 w-px bg-zinc-200 dark:bg-zinc-800" />
        <ToolbarToggle
          label="Simplify paths"
          active={simplifyPaths}
          onClick={() => onSimplifyPaths(!simplifyPaths)}
          badge={simplifyBadge}
          title={
            simplifyPaths
              ? `Simplify paths is on: ${analysis.transitiveCount} transitive edges hidden`
              : `Simplify paths is off: ${analysis.transitiveCount} transitive edges can be hidden`
          }
        />
        <ToolbarToggle
          label="Hide hubs"
          active={hideHubsActive}
          onClick={() => onHideHubs(!hideHubsActive)}
          badge={hubBadge}
          disabled={filterCounts.hubs === 0}
          title={
            filterCounts.hubs === 0
              ? "Hide hubs is unavailable: no high fan-in utility nodes in this scope"
              : hideHubsActive
                ? `Hide hubs is on: ${hiddenCounts.hub} of ${filterCounts.hubs} hubs hidden`
                : `Hide hubs is off: ${filterCounts.hubs} high fan-in utility nodes`
          }
        />
        <ToolbarToggle
          label="Hide untracked"
          active={hideUntracked}
          onClick={() => onHideUntracked(!hideUntracked)}
          badge={untrackedBadge}
          disabled={filterCounts.untracked === 0}
          title={
            filterCounts.untracked === 0
              ? "Hide untracked is unavailable: every node in this scope is tracked"
              : hideUntracked
                ? `Hide untracked is on: ${hiddenCounts.untracked} untracked ${
                    hiddenCounts.untracked === 1 ? "node" : "nodes"
                  } removed from layout`
                : `Hide untracked is off: ${filterCounts.untracked} untracked ${
                    filterCounts.untracked === 1 ? "node" : "nodes"
                  } in layout`
          }
        />
        <ToolbarToggle
          label="Hide isolated"
          active={hideIsolated}
          onClick={() => onHideIsolated(!hideIsolated)}
          badge={isolatedBadge}
          disabled={filterCounts.isolated === 0}
          title={
            filterCounts.isolated === 0
              ? "Hide isolated is unavailable: every node in this scope has a dependency"
              : hideIsolated
                ? `Hide isolated is on: ${hiddenCounts.isolated} disconnected ${
                    hiddenCounts.isolated === 1 ? "node" : "nodes"
                  } removed from layout`
                : `Hide isolated is off: ${filterCounts.isolated} disconnected ${
                    filterCounts.isolated === 1 ? "node" : "nodes"
                  } in layout`
          }
        />
        <ToolbarToggle
          label="Hide test-only"
          active={hideTestOnly}
          onClick={() => onHideTestOnly(!hideTestOnly)}
          badge={testOnlyBadge}
          disabled={filterCounts.testOnly === 0}
          title={
            filterCounts.testOnly === 0
              ? "Hide test-only is unavailable: no test-only modules or packages in this scope"
              : hideTestOnly
                ? `Hide test-only is on: ${hiddenCounts.testOnly} test-only ${
                    hiddenCounts.testOnly === 1 ? "node" : "nodes"
                  } removed from layout`
                : `Hide test-only is off: ${filterCounts.testOnly} test-only ${
                    filterCounts.testOnly === 1 ? "node" : "nodes"
                  } in layout`
          }
        />
        <span className="mx-0.5 h-4 w-px bg-zinc-200 dark:bg-zinc-800" />
        <IconButton
          label={`Switch to ${direction === "LR" ? "top-to-bottom" : "left-to-right"} layout`}
          onClick={() => setDirection((current) => (current === "LR" ? "TB" : "LR"))}
        >
          {direction === "LR" ? <ArrowRight size={13} /> : <ArrowDown size={13} />}
        </IconButton>
        <IconButton label="Zoom out" onClick={() => zoomBy(1 / 1.25)}>
          <Minus size={13} />
        </IconButton>
        <IconButton label="Zoom in" onClick={() => zoomBy(1.25)}>
          <Plus size={13} />
        </IconButton>
        <IconButton
          label="Fit to view"
          onClick={() => {
            if (layout) {
              userMoved.current = false;
              fittedSize.current = { width: size.width, height: size.height };
              setView(fitTransform(layout, size.width, size.height));
            }
          }}
        >
          <Maximize2 size={13} />
        </IconButton>
        <IconButton
          label="Reset zoom"
          onClick={() => {
            userMoved.current = true;
            setView({ x: 0, y: 0, k: 1 });
          }}
        >
          <RotateCcw size={13} />
        </IconButton>
      </div>

      {hiddenNodes.length > 0 ? (
        <div className="absolute bottom-11 left-2 flex max-h-24 max-w-[calc(100%-1rem)] flex-wrap items-center gap-1 overflow-y-auto rounded border border-zinc-200 bg-white px-2 py-1 dark:border-zinc-800 dark:bg-zinc-950">
          <span className="font-mono text-[9px] uppercase tracking-wider text-zinc-400 dark:text-zinc-500">
            Hidden ({hiddenNodes.length}):
          </span>
          {hiddenNodes.map(({ node, reasons }) => (
            <span
              key={node.id}
              className="inline-flex h-5 items-center rounded border border-zinc-200 bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-900"
            >
              <button
                type="button"
                onClick={() => onSelect(node.id)}
                title={`Inspect ${node.label} (${reasons
                  .map((reason) => FILTER_REASON_LABEL[reason])
                  .join(", ")})`}
                className="block max-w-40 truncate px-1.5 font-mono text-[9px] text-zinc-600 hover:text-indigo-600 dark:text-zinc-300 dark:hover:text-indigo-300"
              >
                {node.label}{" "}
                <span className="text-zinc-400 dark:text-zinc-500">
                  ({reasons.map((reason) => FILTER_REASON_LABEL[reason]).join("+")})
                </span>
              </button>
              <button
                type="button"
                onClick={() => onUnhideNode(node.id)}
                title={`Unhide ${node.label}`}
                aria-label={`Unhide ${node.label}`}
                className="border-l border-zinc-200 px-1 text-zinc-400 hover:bg-zinc-100 hover:text-zinc-700 dark:border-zinc-800 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
              >
                <X size={9} />
              </button>
            </span>
          ))}
        </div>
      ) : null}

      <div className="absolute bottom-2 left-2 flex max-w-[calc(100%-1rem)] items-center gap-3 rounded border border-zinc-200 bg-white px-2 py-1 dark:border-zinc-800 dark:bg-zinc-950">
        {focusPresent && focusId !== null ? (
          <span className="flex min-w-0 items-center gap-1.5 font-mono text-[10px]">
            <span className="text-zinc-400 dark:text-zinc-500">focus</span>
            <span className="max-w-56 truncate text-zinc-700 dark:text-zinc-300">{focusLabel}</span>
            <button
              type="button"
              onClick={() => onFocus(null)}
              className="rounded p-0.5 text-zinc-400 hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-900 dark:hover:text-zinc-200"
              title="Clear focus (Esc)"
              aria-label="Clear focus"
            >
              <X size={11} />
            </button>
          </span>
        ) : (
          <span className="font-mono text-[10px] text-zinc-400 dark:text-zinc-500">
            {layout ? `${layout.nodes.length} nodes · ${layout.edges.length} edges` : "laying out…"}
          </span>
        )}
        {hiddenDirectForSelection > 0 ? (
          <span
            className="font-mono text-[9px] text-indigo-600 dark:text-indigo-300"
            title="Simplify paths hides redundant direct dependencies of the selected node"
          >
            {hiddenDirectForSelection} direct hidden
          </span>
        ) : null}
        <span className="flex items-center gap-1 font-mono text-[9px] text-zinc-400 dark:text-zinc-500">
          <span className="h-px w-4 border-t border-dashed border-red-500" /> cycle
        </span>
        <span className="flex items-center gap-1 font-mono text-[9px] text-zinc-400 dark:text-zinc-500">
          <span className="h-1.5 w-1.5 rounded-full bg-amber-500" /> untracked
        </span>
        <span className="flex items-center gap-1 font-mono text-[9px] text-zinc-400 dark:text-zinc-500">
          <span className="h-1.5 w-1.5 rounded-full bg-indigo-500" /> test-only
        </span>
        <span className="hidden font-mono text-[9px] text-zinc-400 lg:inline dark:text-zinc-500">
          drag · wheel · esc
        </span>
      </div>
    </div>
  );
}
