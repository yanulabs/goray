// This file mirrors internal/render/html.go exactly. Every field name below is
// the JSON key emitted by the Go report payload.

export type SyncState =
  | "synced"
  | "soft_stale"
  | "hard_stale"
  | "missing"
  | "not_eligible";

export type NodeKind =
  | "module"
  | "package"
  | "file"
  | "type"
  | "struct"
  | "interface"
  | "func"
  | "method"
  | string;

export interface ReportSummary {
  intent?: string;
  side_effects?: string[];
  invariants?: string[];
  concurrency?: string;
  key_invariants?: string[];
  boundaries?: string[];
  sync_state: SyncState;
  model_id?: string;
  deps_changed?: boolean;
}

export interface ReportMetrics {
  modules: number;
  packages: number;
  files: number;
  types: number;
  funcs: number;
  methods: number;
  loc: number;
  dead: number;
}

export interface ReportCoverage {
  eligible: number;
  synced: number;
  soft_stale: number;
  hard_stale: number;
  missing: number;
  not_eligible: number;
  percent: number;
}

export interface ReportSymbol {
  id: string;
  name: string;
  kind: NodeKind;
  pkg_path?: string;
  module?: string;
  file?: string;
  start_line?: number;
  end_line?: number;
  loc: number;
  complexity: number;
  fan_in: number;
  fan_out: number;
  exported: boolean;
  dead: boolean;
  eligible: boolean;
  is_test: boolean;
  is_generated: boolean;
  untracked: boolean;
  test_covered?: boolean;
  sync_state: SyncState;
  signature?: string;
  doc?: string;
  summary?: ReportSummary;
  callers?: string[];
  callees?: string[];
}

export interface ReportCoupled {
  pkg: string;
  fan_in: number;
  fan_out: number;
}

export interface ReportModuleCard {
  id: string;
  name: string;
  packages: number;
  prod_loc: number;
  test_loc: number;
  root_exported: number;
  ports_calls: number;
  goroutines: number;
  out_modules: number;
  in_modules: number;
  untracked_files: number;
  all_untracked?: boolean;
  sync_state: SyncState;
  summary?: ReportSummary;
}

export interface ReportPackageCard {
  id: string;
  pkg_path: string;
  rel_path: string;
  module: string;
  prod_loc: number;
  test_loc: number;
  exported: number;
  fan_in: number;
  fan_out: number;
  generated_files?: number;
  untracked_files?: number;
  sync_state: SyncState;
  summary?: ReportSummary;
}

export interface ReportImpl {
  id: string;
  name: string;
  pkg_path: string;
  via_ptr: boolean;
}

export interface ReportInterface {
  id: string;
  name: string;
  pkg_path: string;
  module: string;
  methods: number;
  implementations: ReportImpl[];
}

export interface ReportGraphNode {
  id: string;
  label: string;
  kind: NodeKind;
  module?: string;
  loc: number;
  fan_in: number;
  fan_out: number;
  eligible: boolean;
  untracked: boolean;
  layer: number;
  order: number;
  sync_state: SyncState;
  summary?: ReportSummary;
}

export interface ReportGraphEdge {
  from: string;
  to: string;
  weight: number;
}

export interface ReportGraph {
  nodes: ReportGraphNode[];
  edges: ReportGraphEdge[];
}

export interface ReportGraphs {
  modules: ReportGraph;
  packages: ReportGraph;
}

export interface ReportTreeNode {
  id: string;
  kind: NodeKind;
  label: string;
  sync_state: SyncState;
  children?: ReportTreeNode[];
}

export interface Report {
  repo: string;
  module_path: string;
  generated_at: string;
  metrics: ReportMetrics;
  coverage: ReportCoverage;
  hotspots: ReportSymbol[];
  coupled_packages: ReportCoupled[];
  modules: ReportModuleCard[];
  packages: ReportPackageCard[];
  symbols: ReportSymbol[];
  interfaces: ReportInterface[];
  graphs: ReportGraphs;
  tree: ReportTreeNode[];
  module_cycles: string[][];
}

export type ViewMode = "graph" | "hotspots" | "interfaces" | "explorer";
export type GraphScope = "modules" | "packages";
export type RankDirection = "LR" | "TB";
