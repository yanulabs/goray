import type {
  GraphScope,
  Report,
  ReportGraph,
  ReportInterface,
  ReportModuleCard,
  ReportPackageCard,
  ReportSymbol,
  ReportSummary,
  SyncState,
} from "../types/report";

export interface EntityMetrics {
  loc: number;
  complexity: number;
  fanIn: number;
  fanOut: number;
  testCovered?: boolean;
  testLoc?: number;
  testOnly?: boolean;
}

export interface GraphNodeMeta {
  testLoc: number;
  testOnly: boolean;
}

// The Go report emits test_only directly. Older embedded payloads only carry
// prod_loc/test_loc, so apply the same classification the backend uses: no
// production LOC and at least one test LOC. Genuinely empty packages/modules
// (both zero) are not test-only.
export function isTestOnlyCard(card: {
  test_only?: boolean;
  prod_loc: number;
  test_loc: number;
}): boolean {
  return card.test_only === true || (card.prod_loc === 0 && card.test_loc > 0);
}

export function buildGraphNodeMeta(
  report: Report,
  scope: GraphScope,
  graph: ReportGraph,
): Map<string, GraphNodeMeta> {
  const byID = new Map<string, GraphNodeMeta>();
  const cards = scope === "modules" ? report.modules : report.packages;
  for (const card of cards) {
    byID.set(card.id, {
      testLoc: card.test_loc,
      testOnly: isTestOnlyCard(card),
    });
  }
  // Tolerate payloads that classify the graph node itself even when the card
  // is absent (for example a package filtered out of the explorer tree).
  for (const node of graph.nodes) {
    if (node.test_only !== true) {
      continue;
    }
    const existing = byID.get(node.id);
    byID.set(node.id, {
      testLoc: existing?.testLoc ?? 0,
      testOnly: true,
    });
  }
  return byID;
}

export interface Entity {
  id: string;
  name: string;
  kind: string;
  module: string;
  pkgPath: string;
  file: string;
  line: number;
  signature: string;
  doc: string;
  syncState: SyncState;
  summary?: ReportSummary;
  untracked: boolean;
  metrics: EntityMetrics;
  symbol?: ReportSymbol;
  interfaceRef?: ReportInterface;
  moduleCard?: ReportModuleCard;
  packageCard?: ReportPackageCard;
}

export interface EntityIndex {
  entitiesById: Map<string, Entity>;
  symbolsById: Map<string, ReportSymbol>;
  modulesById: Map<string, ReportModuleCard>;
  packagesById: Map<string, ReportPackageCard>;
  interfacesById: Map<string, ReportInterface>;
  graphNodeIds: Set<string>;
  moduleNodeIds: Set<string>;
  packageNodeIds: Set<string>;
  moduleNames: string[];
}

export interface ReportCounts {
  modules: number;
  packages: number;
  files: number;
  symbols: number;
  cycles: number;
  hotspots: number;
  untested: number;
  coveragePercent: number;
  hasUntracked: boolean;
}

function packageName(card: ReportPackageCard): string {
  return card.rel_path === "." || card.rel_path === "" ? card.pkg_path : card.rel_path;
}

export function buildEntityIndex(report: Report): EntityIndex {
  const entitiesById = new Map<string, Entity>();
  const modulesById = new Map<string, ReportModuleCard>();
  const packagesById = new Map<string, ReportPackageCard>();
  const interfacesById = new Map<string, ReportInterface>();
  const symbolsById = new Map<string, ReportSymbol>();
  const moduleNames = new Set<string>();

  for (const card of report.packages) {
    packagesById.set(card.id, card);
    moduleNames.add(card.module);
    entitiesById.set(card.id, {
      id: card.id,
      name: packageName(card),
      kind: "package",
      module: card.module,
      pkgPath: card.pkg_path,
      file: "",
      line: 0,
      signature: "",
      doc: "",
      syncState: card.sync_state,
      summary: card.summary,
      untracked: (card.untracked_files ?? 0) > 0,
      metrics: {
        loc: card.prod_loc,
        complexity: 0,
        fanIn: card.fan_in,
        fanOut: card.fan_out,
        testLoc: card.test_loc,
        testOnly: isTestOnlyCard(card),
      },
      packageCard: card,
    });
  }

  for (const card of report.modules) {
    modulesById.set(card.id, card);
    moduleNames.add(card.name);
    entitiesById.set(card.id, {
      id: card.id,
      name: card.name,
      kind: "module",
      module: card.name,
      pkgPath: "",
      file: "",
      line: 0,
      signature: "",
      doc: "",
      syncState: card.sync_state,
      summary: card.summary,
      untracked: card.all_untracked === true || card.untracked_files > 0,
      metrics: {
        loc: card.prod_loc,
        complexity: 0,
        fanIn: card.in_modules,
        fanOut: card.out_modules,
        testLoc: card.test_loc,
        testOnly: isTestOnlyCard(card),
      },
      moduleCard: card,
    });
  }

  for (const symbol of report.symbols) {
    symbolsById.set(symbol.id, symbol);
    if (symbol.kind === "module" || symbol.kind === "package") {
      continue;
    }
    entitiesById.set(symbol.id, {
      id: symbol.id,
      name: symbol.name,
      kind: symbol.kind,
      module: symbol.module ?? "",
      pkgPath: symbol.pkg_path ?? "",
      file: symbol.file ?? "",
      line: symbol.start_line ?? 0,
      signature: symbol.signature ?? "",
      doc: symbol.doc ?? "",
      syncState: symbol.sync_state,
      summary: symbol.summary,
      untracked: symbol.untracked,
      metrics: {
        loc: symbol.loc,
        complexity: symbol.complexity,
        fanIn: symbol.fan_in,
        fanOut: symbol.fan_out,
        testCovered: symbol.test_covered,
      },
      symbol,
    });
  }

  for (const iface of report.interfaces) {
    interfacesById.set(iface.id, iface);
    moduleNames.add(iface.module);
    const existing = entitiesById.get(iface.id);
    if (existing) {
      existing.interfaceRef = iface;
      existing.kind = "interface";
    } else {
      entitiesById.set(iface.id, {
        id: iface.id,
        name: iface.name,
        kind: "interface",
        module: iface.module,
        pkgPath: iface.pkg_path,
        file: "",
        line: 0,
        signature: "",
        doc: "",
        syncState: "not_eligible",
        untracked: false,
        metrics: {
          loc: 0,
          complexity: 0,
          fanIn: 0,
          fanOut: iface.implementations.length,
        },
        interfaceRef: iface,
      });
    }
  }

  const graphNodeIds = new Set<string>();
  const moduleNodeIds = new Set<string>();
  const packageNodeIds = new Set<string>();
  for (const node of report.graphs.modules.nodes) {
    graphNodeIds.add(node.id);
    moduleNodeIds.add(node.id);
  }
  for (const node of report.graphs.packages.nodes) {
    graphNodeIds.add(node.id);
    packageNodeIds.add(node.id);
  }

  return {
    entitiesById,
    symbolsById,
    modulesById,
    packagesById,
    interfacesById,
    graphNodeIds,
    moduleNodeIds,
    packageNodeIds,
    moduleNames: [...moduleNames].filter((name) => name !== "").sort((a, b) => a.localeCompare(b)),
  };
}

export function reportCounts(report: Report): ReportCounts {
  let symbols = 0;
  let untested = 0;
  let hasUntracked = false;
  for (const symbol of report.symbols) {
    if (symbol.untracked) {
      hasUntracked = true;
    }
    if (symbol.kind === "func" || symbol.kind === "method" || symbol.kind === "type" || symbol.kind === "struct" || symbol.kind === "interface") {
      symbols += 1;
    }
    if (
      (symbol.kind === "func" || symbol.kind === "method") &&
      !symbol.is_test &&
      !symbol.is_generated &&
      symbol.test_covered === false
    ) {
      untested += 1;
    }
  }
  return {
    modules: report.metrics.modules,
    packages: report.metrics.packages,
    files: report.metrics.files,
    symbols,
    cycles: report.module_cycles.length,
    hotspots: report.hotspots.length,
    untested,
    coveragePercent: report.coverage.percent,
    hasUntracked,
  };
}

export function symbolName(index: EntityIndex, id: string): string {
  const entity = index.entitiesById.get(id);
  if (entity) {
    return entity.name;
  }
  const value = id.startsWith("sym:") ? id.slice(4) : id;
  const dot = value.lastIndexOf(".");
  return dot >= 0 ? value.slice(dot + 1) : value;
}

export function symbolLocation(index: EntityIndex, id: string): string {
  const symbol = index.symbolsById.get(id);
  if (!symbol) {
    return "";
  }
  return symbol.file ? `${symbol.file}:${symbol.start_line ?? 0}` : "";
}

export function isHotspotCandidate(symbol: ReportSymbol): boolean {
  if (symbol.is_test) {
    return false;
  }
  return symbol.kind === "func" || symbol.kind === "method" || symbol.kind === "type";
}
