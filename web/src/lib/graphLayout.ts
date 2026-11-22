import dagre from "@dagrejs/dagre";

import type { RankDirection, ReportGraph, ReportGraphNode } from "../types/report";

interface DagreGraphLabel {
  rankdir: RankDirection;
  nodesep: number;
  ranksep: number;
  marginx: number;
  marginy: number;
}

interface DagreNodeLabel {
  width: number;
  height: number;
  x?: number;
  y?: number;
}

interface DagreEdgeLabel {
  weight?: number;
}

export interface PositionedNode {
  node: ReportGraphNode;
  x: number;
  y: number;
  width: number;
  height: number;
  inCycle: boolean;
}

export interface PositionedEdge {
  from: string;
  to: string;
  weight: number;
  cycle: boolean;
}

export interface EdgeAnchor {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface EdgeSlot {
  sourceIndex: number;
  sourceCount: number;
  targetIndex: number;
  targetCount: number;
}

export interface GraphLayout {
  nodes: PositionedNode[];
  edges: PositionedEdge[];
  width: number;
  height: number;
}

function nodeWidth(node: ReportGraphNode): number {
  const minimum = node.kind === "module" ? 150 : 180;
  return Math.min(264, Math.max(minimum, 38 + node.label.length * 6.6));
}

export function layoutGraph(
  graph: ReportGraph,
  direction: RankDirection,
): GraphLayout {
  if (graph.nodes.length === 0) {
    return { nodes: [], edges: [], width: 0, height: 0 };
  }

  const g = new dagre.graphlib.Graph<DagreGraphLabel, DagreNodeLabel, DagreEdgeLabel>({
    directed: true,
    multigraph: true,
  });
  g.setGraph({
    rankdir: direction,
    nodesep: 28,
    ranksep: 72,
    marginx: 28,
    marginy: 28,
  });
  g.setDefaultEdgeLabel(() => ({}));

  const nodeByID = new Map<string, ReportGraphNode>();
  for (const node of graph.nodes) {
    nodeByID.set(node.id, node);
    g.setNode(node.id, { width: nodeWidth(node), height: 50 });
  }

  graph.edges.forEach((edge, index) => {
    if (!nodeByID.has(edge.from) || !nodeByID.has(edge.to)) {
      return;
    }
    g.setEdge(edge.from, edge.to, { weight: Math.max(1, edge.weight) }, `edge-${index}`);
  });

  dagre.layout(g);

  const nodes: PositionedNode[] = [];
  for (const node of graph.nodes) {
    const label = g.node(node.id);
    const width = label.width;
    const height = label.height;
    nodes.push({
      node,
      x: (label.x ?? 0) - width / 2,
      y: (label.y ?? 0) - height / 2,
      width,
      height,
      inCycle: false,
    });
  }

  const positionedByID = new Map(nodes.map((node) => [node.node.id, node]));
  const edges: PositionedEdge[] = [];
  graph.edges.forEach((edge) => {
    const from = positionedByID.get(edge.from);
    const to = positionedByID.get(edge.to);
    if (!from || !to) {
      return;
    }
    const source = nodeByID.get(edge.from);
    const target = nodeByID.get(edge.to);
    const cycle =
      edge.from === edge.to ||
      (source !== undefined && target !== undefined && target.layer <= source.layer);
    edges.push({ from: edge.from, to: edge.to, weight: edge.weight, cycle });
    if (cycle) {
      from.inCycle = true;
      to.inCycle = true;
    }
  });

  let minX = Number.POSITIVE_INFINITY;
  let minY = Number.POSITIVE_INFINITY;
  let maxX = Number.NEGATIVE_INFINITY;
  let maxY = Number.NEGATIVE_INFINITY;
  for (const node of nodes) {
    minX = Math.min(minX, node.x);
    minY = Math.min(minY, node.y);
    maxX = Math.max(maxX, node.x + node.width);
    maxY = Math.max(maxY, node.y + node.height);
  }

  // Normalize to a zero-origin coordinate space so fit-to-view math is exact.
  for (const node of nodes) {
    node.x -= minX;
    node.y -= minY;
  }

  return {
    nodes,
    edges,
    width: Math.max(1, maxX - minX),
    height: Math.max(1, maxY - minY),
  };
}

// Direct cubic edges anchored on node borders. Dagre's dummy-node polylines
// loop around the whole canvas on long edges; direct curves read better while
// dagre still owns node placement.
export function edgeCurve(
  edge: PositionedEdge,
  nodes: Map<string, EdgeAnchor>,
  direction: RankDirection,
  slot?: EdgeSlot,
): string {
  const from = nodes.get(edge.from);
  const to = nodes.get(edge.to);
  if (!from || !to) {
    return "";
  }

  const sourceIndex = slot ? slot.sourceIndex : 0;
  const sourceCount = slot ? slot.sourceCount : 1;
  const targetIndex = slot ? slot.targetIndex : 0;
  const targetCount = slot ? slot.targetCount : 1;

  if (direction === "TB") {
    const backwards = to.y + to.height <= from.y;
    const sourceX = from.x + (from.width * (sourceIndex + 1)) / (sourceCount + 1);
    const sourceY = backwards ? from.y : from.y + from.height;
    const targetX = to.x + (to.width * (targetIndex + 1)) / (targetCount + 1);
    const targetY = backwards ? to.y + to.height : to.y;
    const bend = Math.max(28, Math.abs(targetY - sourceY) * 0.45);
    const control = backwards ? -bend : bend;
    return `M ${sourceX} ${sourceY} C ${sourceX} ${sourceY + control}, ${targetX} ${targetY - control}, ${targetX} ${targetY}`;
  }

  const backwards = to.x + to.width <= from.x;
  const sourceX = backwards ? from.x : from.x + from.width;
  const sourceY = from.y + (from.height * (sourceIndex + 1)) / (sourceCount + 1);
  const targetX = backwards ? to.x + to.width : to.x;
  const targetY = to.y + (to.height * (targetIndex + 1)) / (targetCount + 1);
  const bend = Math.max(32, Math.abs(targetX - sourceX) * 0.45);
  const control = backwards ? -bend : bend;
  return `M ${sourceX} ${sourceY} C ${sourceX + control} ${sourceY}, ${targetX - control} ${targetY}, ${targetX} ${targetY}`;
}
