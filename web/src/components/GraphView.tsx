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

import type { EntityIndex } from "../lib/derive";
import { clamp, formatCompact, kindLabel } from "../lib/format";
import {
  edgeCurve,
  layoutGraph,
  type EdgeAnchor,
  type EdgeSlot,
  type GraphLayout,
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

const MIN_ZOOM = 0.15;
const MAX_ZOOM = 3;

function fitTransform(layout: GraphLayout, width: number, height: number): Transform {
  const padding = 56;
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

function truncate(value: string, max: number): string {
  if (value.length <= max) {
    return value;
  }
  return `${value.slice(0, Math.max(1, max - 1))}…`;
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
}: {
  report: Report;
  index: EntityIndex;
  scope: GraphScope;
  onScope: (scope: GraphScope) => void;
  selectedId: string | null;
  focusId: string | null;
  onSelect: (id: string) => void;
  onFocus: (id: string | null) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [direction, setDirection] = useState<RankDirection>("LR");
  const [view, setView] = useState<Transform>({ x: 0, y: 0, k: 1 });
  const [panning, setPanning] = useState(false);
  const drag = useRef<{
    x: number;
    y: number;
    startX: number;
    startY: number;
    moved: boolean;
  } | null>(null);
  const fittedKey = useRef("");

  const graph = scope === "modules" ? report.graphs.modules : report.graphs.packages;
  const layout = useMemo(() => layoutGraph(graph, direction), [graph, direction]);
  const anchors = useMemo(() => {
    const map = new Map<string, EdgeAnchor>();
    for (const node of layout.nodes) {
      map.set(node.node.id, {
        x: node.x,
        y: node.y,
        width: node.width,
        height: node.height,
      });
    }
    return map;
  }, [layout]);

  const edgeSlots = useMemo(() => {
    const outgoing = new Map<string, number[]>();
    const incoming = new Map<string, number[]>();
    layout.edges.forEach((edge, index) => {
      const outList = outgoing.get(edge.from);
      if (outList) {
        outList.push(index);
      } else {
        outgoing.set(edge.from, [index]);
      }
      const inList = incoming.get(edge.to);
      if (inList) {
        inList.push(index);
      } else {
        incoming.set(edge.to, [index]);
      }
    });
    const slots = new Map<number, EdgeSlot>();
    outgoing.forEach((indexes) => {
      indexes.forEach((edgeIndex, position) => {
        const existing = slots.get(edgeIndex);
        slots.set(edgeIndex, {
          sourceIndex: position,
          sourceCount: indexes.length,
          targetIndex: existing?.targetIndex ?? 0,
          targetCount: existing?.targetCount ?? 1,
        });
      });
    });
    incoming.forEach((indexes) => {
      indexes.forEach((edgeIndex, position) => {
        const existing = slots.get(edgeIndex);
        slots.set(edgeIndex, {
          sourceIndex: existing?.sourceIndex ?? 0,
          sourceCount: existing?.sourceCount ?? 1,
          targetIndex: position,
          targetCount: indexes.length,
        });
      });
    });
    return slots;
  }, [layout]);

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
    const key = `${scope}:${direction}`;
    if (size.width <= 0 || size.height <= 0 || layout.nodes.length === 0) {
      return;
    }
    if (fittedKey.current === key) {
      return;
    }
    fittedKey.current = key;
    setView(fitTransform(layout, size.width, size.height));
  }, [scope, direction, size.width, size.height, layout]);

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

  const focusPresent = focusId !== null && layout.nodes.some((node) => node.node.id === focusId);
  const activeState = useMemo(() => {
    if (!focusPresent || focusId === null) {
      return null;
    }
    const nodes = new Set<string>([focusId]);
    const edges = new Set<number>();
    layout.edges.forEach((edge, edgeIndex) => {
      if (edge.from === focusId) {
        nodes.add(edge.to);
        edges.add(edgeIndex);
      }
      if (edge.to === focusId) {
        nodes.add(edge.from);
        edges.add(edgeIndex);
      }
    });
    return { nodes, edges };
  }, [focusId, focusPresent, layout]);

  useEffect(() => {
    if (selectedId === null || size.width <= 0 || size.height <= 0) {
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

  if (layout.nodes.length === 0) {
    return (
      <div className="h-full overflow-hidden">
        <EmptyState
          title="No graph nodes"
          detail="The index produced no nodes for this scope. Re-run the index or switch scope."
        />
      </div>
    );
  }

  const focusLabel =
    focusId !== null
      ? index.entitiesById.get(focusId)?.name ?? focusId.replace(/^(mod|pkg):/, "")
      : "";

  return (
    <div ref={containerRef} className="relative h-full min-w-0 overflow-hidden">
      <svg
        width={size.width}
        height={size.height}
        className={`block h-full w-full touch-none ${panning ? "cursor-grabbing" : "cursor-grab"}`}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={endPan}
        onPointerCancel={endPan}
        role="application"
        aria-label="Dependency graph. Drag to pan, scroll to zoom, select a node to inspect it."
      >
        <defs>
          <marker
            id="goray-arrow"
            viewBox="0 0 8 8"
            refX="7"
            refY="4"
            markerWidth="7"
            markerHeight="7"
            orient="auto-start-reverse"
          >
            <path d="M 0 0 L 8 4 L 0 8 z" fill="context-stroke" />
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
          <marker
            id="goray-arrow-active"
            viewBox="0 0 8 8"
            refX="7"
            refY="4"
            markerWidth="7"
            markerHeight="7"
            orient="auto-start-reverse"
          >
            <path d="M 0 0 L 8 4 L 0 8 z" className="fill-indigo-500" />
          </marker>
        </defs>

        <g transform={`translate(${view.x} ${view.y}) scale(${view.k})`}>
          <g fill="none">
            {layout.edges.map((edge, edgeIndex) => {
              const isActive = activeState === null || activeState.edges.has(edgeIndex);
              const strokeClass = edge.cycle
                ? "stroke-red-500"
                : activeState !== null && activeState.edges.has(edgeIndex)
                  ? "stroke-indigo-500"
                  : "stroke-zinc-400 dark:stroke-zinc-600";
              const marker = edge.cycle
                ? "url(#goray-arrow-cycle)"
                : activeState !== null && activeState.edges.has(edgeIndex)
                  ? "url(#goray-arrow-active)"
                  : "url(#goray-arrow)";
              return (
                <path
                  key={`${edge.from}->${edge.to}-${edgeIndex}`}
                  d={edgeCurve(edge, anchors, direction, edgeSlots.get(edgeIndex))}
                  className={strokeClass}
                  strokeWidth={edge.cycle || activeState?.edges.has(edgeIndex) ? 1.7 : 1}
                  strokeDasharray={edge.cycle ? "4 3" : undefined}
                  markerEnd={marker}
                  opacity={isActive ? 1 : 0.15}
                  vectorEffect="non-scaling-stroke"
                />
              );
            })}
          </g>

          {layout.nodes.map(({ node, x, y, width, height, inCycle }) => {
            const isActive = activeState === null || activeState.nodes.has(node.id);
            const selected = selectedId === node.id;
            const labelMax = Math.max(12, Math.floor((width - (node.untracked ? 72 : 22)) / 6.4));
            return (
              <g
                key={node.id}
                transform={`translate(${x} ${y})`}
                opacity={isActive ? 1 : 0.15}
                role="button"
                tabIndex={0}
                aria-label={`${kindLabel(node.kind)} ${node.label}`}
                aria-pressed={selected}
                className="cursor-pointer outline-none"
                onPointerDown={(event) => event.stopPropagation()}
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
                  strokeWidth={selected ? 2 : 1}
                  vectorEffect="non-scaling-stroke"
                  className={
                    selected
                      ? "fill-white stroke-indigo-500 dark:fill-zinc-900"
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
                  className="fill-zinc-800 font-mono text-[11px] font-medium dark:fill-zinc-100"
                  style={{ fontSize: 11 }}
                >
                  {truncate(node.label, labelMax)}
                </text>
                <text
                  x={10}
                  y={35}
                  className="fill-zinc-500 font-mono text-[9px] tabular-nums dark:fill-zinc-400"
                  style={{ fontSize: 9 }}
                >
                  {formatCompact(node.loc)} loc · in {node.fan_in} · out {node.fan_out}
                </text>
                {node.untracked ? (
                  <g>
                    <rect
                      x={width - 56}
                      y={6}
                      width={50}
                      height={12}
                      rx={2}
                      className="fill-amber-50 stroke-amber-400 dark:fill-amber-950 dark:stroke-amber-800"
                      strokeWidth={0.5}
                      vectorEffect="non-scaling-stroke"
                    />
                    <text
                      x={width - 31}
                      y={15}
                      textAnchor="middle"
                      className="fill-amber-700 font-mono dark:fill-amber-300"
                      style={{ fontSize: 7 }}
                    >
                      untracked
                    </text>
                    <title>Untracked file</title>
                  </g>
                ) : null}
                <title>
                  {node.label} · {kindLabel(node.kind)} · {node.loc} LOC · fan-in {node.fan_in} ·
                  fan-out {node.fan_out}
                </title>
              </g>
            );
          })}
        </g>
      </svg>

      <div className="absolute right-2 top-2 flex items-center gap-1 rounded border border-zinc-200 bg-white p-1 dark:border-zinc-800 dark:bg-zinc-950">
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
          onClick={() => setView(fitTransform(layout, size.width, size.height))}
        >
          <Maximize2 size={13} />
        </IconButton>
        <IconButton label="Reset zoom" onClick={() => setView({ x: 0, y: 0, k: 1 })}>
          <RotateCcw size={13} />
        </IconButton>
      </div>

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
            {layout.nodes.length} nodes · {layout.edges.length} edges
          </span>
        )}
        <span className="flex items-center gap-1 font-mono text-[9px] text-zinc-400 dark:text-zinc-500">
          <span className="h-px w-4 border-t border-dashed border-red-500" /> cycle
        </span>
        <span className="flex items-center gap-1 font-mono text-[9px] text-zinc-400 dark:text-zinc-500">
          <span className="h-1.5 w-1.5 rounded-full bg-amber-500" /> untracked
        </span>
        <span className="hidden font-mono text-[9px] text-zinc-400 lg:inline dark:text-zinc-500">
          drag · wheel · esc
        </span>
      </div>
    </div>
  );
}
