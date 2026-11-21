(() => {
"use strict";

const DATA = JSON.parse(document.getElementById("report-data").textContent);
const SVG_NS = ["http", "://www.w3.org/2000/svg"].join("");
const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));
const fmt = (value) => Number(value || 0).toLocaleString();

const symbols = Array.isArray(DATA.symbols) ? DATA.symbols : [];
const symbolById = new Map(symbols.map((s) => [s.id, s]));
const packageByPath = new Map((DATA.packages || []).map((p) => [p.rel_path, p]));
const moduleNames = (DATA.modules || []).map((m) => m.name);
const oneToOneModules = (() => {
  const modules = DATA.modules || [];
  return modules.length > 0 && modules.every((m) => (m.packages || 0) <= 1);
})();

// Cross-reference maps built once on boot for O(1) inspector lookups.
const implsByTypeId = new Map();
const ifaceById = new Map();
for (const iface of DATA.interfaces || []) {
  ifaceById.set(iface.id, iface);
  for (const impl of iface.implementations || []) {
    if (!implsByTypeId.has(impl.id)) implsByTypeId.set(impl.id, []);
    implsByTypeId.get(impl.id).push({ iface, via_ptr: Boolean(impl.via_ptr) });
  }
}

const methodsByTypeKey = new Map();
for (const s of symbols) {
  if (s.kind !== "method" || !s.pkg_path) continue;
  const m = s.id.match(/^sym:(.+)\.\(\*?([^)]+)\)\.[^.]+$/);
  if (!m) continue;
  const typeId = "sym:" + m[1] + "." + m[2];
  if (!methodsByTypeKey.has(typeId)) methodsByTypeKey.set(typeId, []);
  methodsByTypeKey.get(typeId).push(s);
}

// Deterministic module color palette (HSL).
const moduleColorCache = new Map();
function moduleColor(name) {
  if (!name) return "var(--line-strong)";
  if (moduleColorCache.has(name)) return moduleColorCache.get(name);
  let hash = 2166136261;
  for (let i = 0; i < name.length; i++) {
    hash ^= name.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  const hue = Math.abs(hash) % 360;
  const color = "hsl(" + hue + " 62% 48%)";
  moduleColorCache.set(name, color);
  return color;
}

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = String(text);
  return node;
}

function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
}

function button(text, className, onClick) {
  const node = el("button", className, text);
  node.type = "button";
  if (onClick) node.addEventListener("click", onClick);
  return node;
}

function svgElement(tag, attrs) {
  const node = document.createElementNS(SVG_NS, tag);
  for (const key of Object.keys(attrs || {})) node.setAttribute(key, String(attrs[key]));
  return node;
}

let toastTimer = null;
function showToast(message) {
  const toast = $("#toast");
  toast.textContent = message;
  toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { toast.hidden = true; }, 1800);
}

function copyText(text, label) {
  const fallback = () => {
    const area = el("textarea");
    area.value = text;
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.select();
    try { document.execCommand("copy"); showToast("Copied " + label); } catch (_) { showToast("Copy failed"); }
    document.body.removeChild(area);
  };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(() => showToast("Copied " + label), fallback);
  } else {
    fallback();
  }
}

function realSideEffects(summary) {
  if (!summary || !Array.isArray(summary.side_effects)) return [];
  return summary.side_effects.filter((e) => e && e !== "none");
}

function riskScore(symbol) {
  const c = Number(symbol.complexity || 0);
  const fi = Number(symbol.fan_in || 0);
  const loc = Number(symbol.loc || 0);
  if (c === 0 && fi === 0 && loc === 0) return 0;
  return Math.round((c * (1 + Math.log2(1 + fi)) + loc / 40) * 10) / 10;
}

function stateClass(state) {
  switch (state) {
    case "synced": return "good";
    case "soft_stale": return "warn";
    case "hard_stale": return "bad";
    case "not_eligible": return "";
    default: return "accent";
  }
}

function stateColor(state) {
  switch (state) {
    case "synced": return "var(--good)";
    case "soft_stale": return "var(--warn)";
    case "hard_stale": return "var(--bad)";
    case "not_eligible": return "var(--muted)";
    default: return "var(--accent)";
  }
}

function stateBadge(state) {
  return el("span", "pill " + stateClass(state), state || "missing");
}

function addPills(parent, values, className) {
  if (!values || !values.length) return;
  const pills = el("div", "pills");
  for (const value of values) pills.appendChild(el("span", "pill " + (className || ""), value));
  parent.appendChild(pills);
}

function addList(parent, title, values) {
  if (!values || !values.length) return;
  parent.appendChild(el("h3", null, title));
  const list = el("ul", "clean");
  for (const value of values) list.appendChild(el("li", null, value));
  parent.appendChild(list);
}

function makeMeter(value, maxVal, warnAt, badAt) {
  const wrap = el("div", "meter-cell");
  wrap.appendChild(el("span", null, fmt(value)));
  const bar = el("span", "meter");
  const fill = el("span");
  const pct = maxVal > 0 ? Math.min(100, Math.round((value / maxVal) * 100)) : 0;
  fill.style.width = pct + "%";
  fill.className = value >= badAt ? "meter-high" : value >= warnAt ? "meter-med" : "meter-low";
  bar.appendChild(fill);
  wrap.appendChild(bar);
  return wrap;
}

/* ==================== Inspector with History & Blast Radius ==================== */

const inspectorHistory = { stack: [], index: -1 };

function computeBlastRadius(startId, maxDepth = 6) {
  const visited = new Set([startId]);
  const direct = [];
  const transitive = [];
  const affectedPkgs = new Set();
  const queue = [{ id: startId, depth: 0 }];

  while (queue.length) {
    const { id, depth } = queue.shift();
    if (depth >= maxDepth) continue;
    const sym = symbolById.get(id);
    if (!sym || !Array.isArray(sym.callers)) continue;
    for (const callerId of sym.callers) {
      if (visited.has(callerId)) continue;
      visited.add(callerId);
      const caller = symbolById.get(callerId);
      if (depth === 0) direct.push(callerId);
      else transitive.push(callerId);
      if (caller && caller.pkg_path) affectedPkgs.add(caller.pkg_path);
      queue.push({ id: callerId, depth: depth + 1 });
    }
  }
  return { direct, transitive, affectedPkgs: Array.from(affectedPkgs).sort() };
}

function summaryNode(summary, fallbackState) {
  const section = el("div", "section");
  section.appendChild(el("h3", null, "Structured summary"));
  if (!summary) {
    section.appendChild(el("div", "summary-empty", "No cached LLM summary. Static semantic index data is shown above."));
    if (fallbackState) {
      const row = el("div", "pills");
      row.style.marginTop = "6px";
      row.appendChild(stateBadge(fallbackState));
      section.appendChild(row);
    }
    return section;
  }
  if (summary.intent) section.appendChild(el("div", "summary-intent", summary.intent));
  const effects = realSideEffects(summary);
  if (effects.length) {
    addPills(section, effects, "warn");
  } else if (Array.isArray(summary.side_effects) && summary.side_effects.includes("none")) {
    addPills(section, ["no side effects reported"]);
  }
  addList(section, "Invariants", summary.invariants || summary.key_invariants);
  if (summary.concurrency) {
    section.appendChild(el("h3", null, "Concurrency"));
    section.appendChild(el("div", null, summary.concurrency));
  }
  addList(section, "Boundaries", summary.boundaries);
  if (summary.model_id) {
    const meta = el("div", "pills");
    meta.style.marginTop = "8px";
    meta.appendChild(el("span", "pill mono", summary.model_id));
    section.appendChild(meta);
  }
  if (summary.deps_changed) {
    section.appendChild(el("div", "notice", "Direct dependencies or children changed; this cached summary is soft-stale."));
  }
  return section;
}

function updateInspectorNavButtons() {
  $("#inspector-back").disabled = inspectorHistory.index <= 0;
  $("#inspector-forward").disabled = inspectorHistory.index >= inspectorHistory.stack.length - 1;
}

function openInspector(id, pushHistory = true) {
  let symbol = symbolById.get(id);
  if (!symbol && packageByPath.has(id)) {
    symbol = symbolById.get(packageByPath.get(id).id);
  }
  if (!symbol) return;

  if (pushHistory && inspectorHistory.stack[inspectorHistory.index] !== symbol.id) {
    inspectorHistory.stack = inspectorHistory.stack.slice(0, inspectorHistory.index + 1);
    inspectorHistory.stack.push(symbol.id);
    inspectorHistory.index = inspectorHistory.stack.length - 1;
  }
  updateInspectorNavButtons();
  syncUrlHash(activeTab(), symbol.id);

  const inspector = $("#inspector");
  const body = $("#inspector-body");
  $("#inspector-title").textContent = symbol.name || symbol.id;
  $("#inspector-subtitle").textContent = [symbol.kind, symbol.pkg_path || symbol.module].filter(Boolean).join(" · ");
  clear(body);

  // Copyable ID bar
  const idBar = el("div", "id-bar");
  idBar.appendChild(el("span", "id", symbol.id));
  idBar.appendChild(button("Copy ID", "ghost btn-xs", () => copyText(symbol.id, "symbol ID")));
  body.appendChild(idBar);

  // Stat strip
  const strip = el("div", "stat-strip");
  const addStat = (k, v) => {
    const box = el("div", "stat-box");
    box.appendChild(el("div", "k", k));
    box.appendChild(el("div", "v", v));
    strip.appendChild(box);
  };
  addStat("LOC", fmt(symbol.loc));
  addStat("Complexity", fmt(symbol.complexity));
  addStat("Fan-in / out", fmt(symbol.fan_in) + " / " + fmt(symbol.fan_out));
  addStat("Risk", riskScore(symbol).toFixed(1));
  body.appendChild(strip);

  // Metadata KV
  const facts = el("div", "kv");
  const addFact = (key, valueNodeOrText) => {
    facts.appendChild(el("div", "k", key));
    const v = el("div", "v");
    if (typeof valueNodeOrText === "string") v.textContent = valueNodeOrText;
    else v.appendChild(valueNodeOrText);
    facts.appendChild(v);
  };
  if (symbol.file) {
    const locStr = symbol.file + (symbol.start_line ? ":" + symbol.start_line + (symbol.end_line ? "-" + symbol.end_line : "") : "");
    const locWrap = el("span", "pills");
    locWrap.appendChild(el("span", "mono", locStr));
    locWrap.appendChild(button("Copy", "ghost btn-xs", () => copyText(symbol.file + ":" + (symbol.start_line || 1), "source location")));
    addFact("Source", locWrap);
  }
  addFact("Package", symbol.pkg_path || "—");
  if (symbol.module) {
    const modPill = el("span", "pill");
    const dot = el("span", "dot");
    dot.style.background = moduleColor(symbol.module);
    modPill.appendChild(dot);
    modPill.appendChild(document.createTextNode(symbol.module));
    addFact("Module", modPill);
  }
  const flagRow = el("div", "pills");
  if (symbol.exported) flagRow.appendChild(el("span", "pill accent", "exported"));
  if (symbol.dead) flagRow.appendChild(el("span", "pill bad", "dead code"));
  flagRow.appendChild(el("span", "pill", symbol.eligible ? "eligible" : "not eligible"));
  flagRow.appendChild(stateBadge(symbol.sync_state));
  addFact("Status", flagRow);
  body.appendChild(facts);

  // Structured Summary
  body.appendChild(summaryNode(symbol.summary, symbol.sync_state));

  // Blast Radius (when callers exist)
  if (symbol.callers && symbol.callers.length) {
    const blast = computeBlastRadius(symbol.id);
    const section = el("div", "section");
    section.appendChild(el("h3", null, "Upstream Blast Radius"));
    const box = el("div", "blast-box");
    box.textContent = fmt(blast.direct.length) + " direct callers · " +
      fmt(blast.transitive.length) + " transitive callers · " +
      fmt(blast.affectedPkgs.length) + " impacted package(s)";
    section.appendChild(box);
    if (blast.affectedPkgs.length) addPills(section, blast.affectedPkgs.slice(0, 12), "mono");
    body.appendChild(section);
  }

  // Call Relations
  const addRelationSection = (title, ids) => {
    if (!ids || !ids.length) return;
    const section = el("div", "section");
    section.appendChild(el("h3", null, title + " (" + ids.length + ")"));
    const row = el("div", "relation");
    for (const target of ids.slice(0, 80)) {
      const targetSym = symbolById.get(target);
      row.appendChild(button(targetSym ? targetSym.name : target, "linkish mono", () => openInspector(target)));
    }
    section.appendChild(row);
    if (ids.length > 80) section.appendChild(el("div", "muted", "Showing 80 of " + ids.length));
    body.appendChild(section);
  };
  addRelationSection("Direct Callers", symbol.callers);
  addRelationSection("Direct Callees", symbol.callees);

  // Type Methods & Interface Cross-References
  if (symbol.kind === "type") {
    const methods = methodsByTypeKey.get(symbol.id) || [];
    if (methods.length) {
      const section = el("div", "section");
      section.appendChild(el("h3", null, "Methods (" + methods.length + ")"));
      const row = el("div", "relation");
      for (const m of methods) {
        row.appendChild(button(m.name, "linkish mono", () => openInspector(m.id)));
      }
      section.appendChild(row);
      body.appendChild(section);
    }
    const ifaceInfo = ifaceById.get(symbol.id);
    if (ifaceInfo) {
      const section = el("div", "section");
      section.appendChild(el("h3", null, "Concrete Implementations (" + (ifaceInfo.implementations || []).length + ")"));
      const row = el("div", "relation");
      if (ifaceInfo.implementations && ifaceInfo.implementations.length) {
        for (const impl of ifaceInfo.implementations) {
          row.appendChild(button(impl.name + (impl.via_ptr ? " (*ptr)" : ""), "linkish mono", () => openInspector(impl.id)));
        }
      } else {
        row.appendChild(el("span", "summary-empty", "No concrete types implement this interface."));
      }
      section.appendChild(row);
      body.appendChild(section);
    }
    const implemented = implsByTypeId.get(symbol.id);
    if (implemented && implemented.length) {
      const section = el("div", "section");
      section.appendChild(el("h3", null, "Implements Interfaces (" + implemented.length + ")"));
      const row = el("div", "relation");
      for (const entry of implemented) {
        row.appendChild(button(entry.iface.name + (entry.via_ptr ? " (*ptr)" : ""), "linkish mono", () => openInspector(entry.iface.id)));
      }
      section.appendChild(row);
      body.appendChild(section);
    }
  }

  // Exported API for containers
  const api = apiSymbols(symbol);
  if (api.length) {
    const section = el("div", "section");
    section.appendChild(el("h3", null, "Exported API (" + api.length + ")"));
    const row = el("div", "relation");
    for (const item of api.slice(0, 100)) {
      const label = item.kind === "method" ? "(" + (item.pkg_path || "").split("/").pop() + ")." + item.name : item.name;
      row.appendChild(button(label, "linkish mono", () => openInspector(item.id)));
    }
    section.appendChild(row);
    if (api.length > 100) section.appendChild(el("div", "muted", "Showing 100 of " + api.length));
    body.appendChild(section);
  }

  inspector.hidden = false;
  $("#inspector-backdrop").hidden = false;
  body.scrollTop = 0;
}

function apiSymbols(symbol) {
  const wanted = new Set(["func", "method", "type"]);
  return symbols.filter((item) => {
    if (item.id === symbol.id || !item.exported || !wanted.has(item.kind)) return false;
    if (symbol.kind === "package") return item.pkg_path === symbol.pkg_path;
    if (symbol.kind === "module") return item.module === symbol.module;
    if (symbol.kind === "file") return item.file === symbol.file;
    return false;
  });
}

function closeInspector() {
  $("#inspector").hidden = true;
  $("#inspector-backdrop").hidden = true;
  syncUrlHash(activeTab(), null);
}

/* ==================== Overview Tab ==================== */

const overviewState = { query: "", sort: "loc" };

function renderOverview() {
  const root = $("#overview-content");
  clear(root);
  const metrics = DATA.metrics || {};
  const coverage = DATA.coverage || {};

  // Top KPI Grid (actionable)
  const grid = el("div", "grid");
  const addMetric = (label, value, sub, tone, onClick) => {
    const card = el("div", "metric" + (tone ? " tone-" + tone : "") + (onClick ? " actionable" : ""));
    if (onClick) card.addEventListener("click", onClick);
    card.appendChild(el("div", "k", label));
    card.appendChild(el("div", "v", value));
    if (sub) card.appendChild(el("div", "s", sub));
    grid.appendChild(card);
  };
  addMetric("Modules", fmt(metrics.modules), "View dependency graph →", null, () => {
    graphState.mode = oneToOneModules ? "packages" : "modules";
    switchTab("graph");
  });
  addMetric("Packages", fmt(metrics.packages), "View package graph →", null, () => {
    graphState.mode = "packages";
    switchTab("graph");
  });
  addMetric("Files", fmt(metrics.files), "Browse hierarchy →", null, () => switchTab("tree"));
  addMetric("Types", fmt(metrics.types), fmt((DATA.interfaces || []).length) + " interfaces →", null, () => switchTab("interfaces"));
  addMetric("Functions", fmt((metrics.funcs || 0) + (metrics.methods || 0)), fmt(metrics.funcs) + " funcs · " + fmt(metrics.methods) + " methods", null, () => {
    resetHotspotFilters();
    switchTab("hotspots");
  });
  addMetric("Production LOC", fmt(metrics.loc), "Indexed production lines");
  addMetric("Dead code", fmt(metrics.dead), "Inspect unreferenced →", metrics.dead > 0 ? "bad" : "good", () => {
    resetHotspotFilters({ dead: true });
    switchTab("hotspots");
  });
  addMetric("Summary coverage", (coverage.percent || 0) + "%", fmt(coverage.synced) + " / " + fmt(coverage.eligible) + " eligible", (coverage.percent || 0) >= 80 ? "good" : null, () => {
    resetHotspotFilters({ eligible: true });
    switchTab("hotspots");
  });
  root.appendChild(grid);

  // Multi-Segment Summary Coverage Panel
  const covPanel = el("div", "panel");
  covPanel.style.marginTop = "14px";
  const covHead = el("div", "card-head");
  covHead.appendChild(el("h2", null, "Summary Cache Health"));
  covHead.appendChild(el("span", "muted", fmt(coverage.eligible) + " eligible of " + fmt((coverage.eligible || 0) + (coverage.not_eligible || 0)) + " total symbols"));
  covPanel.appendChild(covHead);

  const denom = Math.max(1, Number(coverage.eligible || 0));
  const bar = el("div", "coverage-bar");
  const addSeg = (cls, count, label) => {
    if (!count) return;
    const seg = el("span", cls);
    seg.style.width = ((count / denom) * 100).toFixed(2) + "%";
    seg.title = label + ": " + fmt(count);
    bar.appendChild(seg);
  };
  addSeg("seg-good", coverage.synced, "Synced");
  addSeg("seg-warn", coverage.soft_stale, "Soft stale");
  addSeg("seg-bad", coverage.hard_stale, "Hard stale");
  addSeg("seg-missing", coverage.missing, "Missing");
  covPanel.appendChild(bar);

  const covPills = el("div", "pills");
  covPills.appendChild(el("span", "pill good", "synced " + fmt(coverage.synced)));
  covPills.appendChild(el("span", "pill warn", "soft stale " + fmt(coverage.soft_stale)));
  covPills.appendChild(el("span", "pill bad", "hard stale " + fmt(coverage.hard_stale)));
  covPills.appendChild(el("span", "pill accent", "missing " + fmt(coverage.missing)));
  covPills.appendChild(el("span", "pill", "not eligible " + fmt(coverage.not_eligible)));
  covPanel.appendChild(covPills);
  root.appendChild(covPanel);

  // Two-column Risk Hotspots + Most Coupled Packages
  const columns = el("div", "two-col");

  const riskPanel = el("div", "panel");
  const riskHeader = el("div", "card-head");
  riskHeader.appendChild(el("h2", null, "Top Risk Hotspots"));
  riskHeader.appendChild(button("All hotspots →", "ghost btn-xs", () => {
    resetHotspotFilters({ high: true });
    switchTab("hotspots");
  }));
  riskPanel.appendChild(riskHeader);
  const hotspots = DATA.hotspots || [];
  if (hotspots.length) {
    const maxC = Math.max(10, ...hotspots.map((s) => s.complexity || 0));
    const table = el("table");
    const head = el("tr");
    for (const label of ["Symbol", "Kind", "Complexity", "Fan-in"]) head.appendChild(el("th", label === "Complexity" || label === "Fan-in" ? "num" : null, label));
    table.appendChild(el("thead")).appendChild(head);
    const tbody = el("tbody");
    for (const symbol of hotspots) {
      const row = el("tr", "clickable");
      row.addEventListener("click", () => openInspector(symbol.id));
      row.appendChild(el("td", "mono", symbol.name));
      row.appendChild(el("td", null, symbol.kind));
      const cCell = el("td", "num");
      cCell.appendChild(makeMeter(symbol.complexity || 0, maxC, 10, 20));
      row.appendChild(cCell);
      row.appendChild(el("td", "num", fmt(symbol.fan_in)));
      tbody.appendChild(row);
    }
    table.appendChild(tbody);
    riskPanel.appendChild(table);
  } else {
    riskPanel.appendChild(el("div", "empty", "No complexity hotspots detected."));
  }
  columns.appendChild(riskPanel);

  const coupledPanel = el("div", "panel");
  const coupledHeader = el("div", "card-head");
  coupledHeader.appendChild(el("h2", null, "Most Coupled Packages"));
  coupledHeader.appendChild(button("Graph →", "ghost btn-xs", () => {
    graphState.mode = "packages";
    switchTab("graph");
  }));
  coupledPanel.appendChild(coupledHeader);
  const coupled = DATA.coupled_packages || [];
  if (coupled.length) {
    const table = el("table");
    const head = el("tr");
    for (const label of ["Package", "Fan-in", "Fan-out", "Instability"]) {
      head.appendChild(el("th", label === "Package" ? null : "num", label));
    }
    table.appendChild(el("thead")).appendChild(head);
    const tbody = el("tbody");
    for (const pkg of coupled) {
      const row = el("tr", "clickable");
      const targetId = symbolById.has("pkg:" + pkg.pkg) ? "pkg:" + pkg.pkg : (packageByPath.get(pkg.pkg) || {}).id || ("pkg:" + pkg.pkg);
      row.addEventListener("click", () => openInspector(targetId));
      row.appendChild(el("td", "mono", pkg.pkg));
      row.appendChild(el("td", "num", fmt(pkg.fan_in)));
      row.appendChild(el("td", "num", fmt(pkg.fan_out)));
      const total = (pkg.fan_in || 0) + (pkg.fan_out || 0);
      const instability = total > 0 ? ((pkg.fan_out || 0) / total).toFixed(2) : "0.00";
      row.appendChild(el("td", "num muted", instability));
      tbody.appendChild(row);
    }
    table.appendChild(tbody);
    coupledPanel.appendChild(table);
  } else {
    coupledPanel.appendChild(el("div", "empty", "No package coupling data."));
  }
  columns.appendChild(coupledPanel);
  root.appendChild(columns);

  // Architecture Cards Filter Bar
  const archHeader = el("div", "section-header");
  archHeader.appendChild(el("h2", null, "Architecture Breakdown"));
  const archTools = el("div", "section-tools");
  const archSearch = el("input");
  archSearch.type = "search";
  archSearch.placeholder = "Filter modules & packages…";
  archSearch.value = overviewState.query;
  archSearch.addEventListener("input", () => {
    overviewState.query = archSearch.value.toLowerCase();
    renderArchitectureCards(archContainer);
  });
  const archSort = el("select");
  archSort.appendChild(new Option("Sort: Prod LOC", "loc"));
  archSort.appendChild(new Option("Sort: Coupling", "coupling"));
  archSort.appendChild(new Option("Sort: Name", "name"));
  archSort.value = overviewState.sort;
  archSort.addEventListener("change", () => {
    overviewState.sort = archSort.value;
    renderArchitectureCards(archContainer);
  });
  archTools.appendChild(archSearch);
  archTools.appendChild(archSort);
  archHeader.appendChild(archTools);
  root.appendChild(archHeader);

  const archContainer = el("div");
  root.appendChild(archContainer);
  renderArchitectureCards(archContainer);
}

function renderArchitectureCards(container) {
  clear(container);
  const q = overviewState.query;
  const sortFn = (a, b, locKey, coupA, coupB, nameA, nameB) => {
    if (overviewState.sort === "name") return nameA.localeCompare(nameB);
    if (overviewState.sort === "coupling") return (coupB - coupA) || nameA.localeCompare(nameB);
    return ((b[locKey] || 0) - (a[locKey] || 0)) || nameA.localeCompare(nameB);
  };

  const modules = (oneToOneModules ? [] : (DATA.modules || []))
    .filter((m) => !q || (m.name + " " + (m.summary && m.summary.intent || "")).toLowerCase().includes(q))
    .slice()
    .sort((a, b) => sortFn(a, b, "prod_loc", (a.in_modules || 0) + (a.out_modules || 0), (b.in_modules || 0) + (b.out_modules || 0), a.name, b.name));

  if (modules.length) {
    const mh = el("h3", "muted", "Modules (" + modules.length + ")");
    mh.style.marginTop = "8px";
    container.appendChild(mh);
    const modGrid = el("div", "cards");
    for (const mod of modules) {
      const card = el("div", "card clickable");
      card.style.setProperty("--card-accent", moduleColor(mod.name));
      card.addEventListener("click", () => openInspector(mod.id));
      const head = el("div", "card-head");
      head.appendChild(el("span", "card-title mono", mod.name));
      head.appendChild(stateBadge(mod.sync_state));
      card.appendChild(head);
      card.appendChild(el("div", "card-stats", fmt(mod.packages) + " pkgs · " + fmt(mod.prod_loc) + " LOC · " + fmt(mod.in_modules) + " in / " + fmt(mod.out_modules) + " out"));
      if (mod.summary && mod.summary.intent) {
        card.appendChild(el("div", "intent", mod.summary.intent));
        addPills(card, mod.summary.boundaries);
      } else {
        card.appendChild(el("div", "summary-empty", "No cached module summary."));
      }
      modGrid.appendChild(card);
    }
    container.appendChild(modGrid);
  }

  const packages = (DATA.packages || [])
    .filter((p) => !q || (p.rel_path + " " + (p.summary && p.summary.intent || "")).toLowerCase().includes(q))
    .slice()
    .sort((a, b) => sortFn(a, b, "prod_loc", (a.fan_in || 0) + (a.fan_out || 0), (b.fan_in || 0) + (b.fan_out || 0), a.rel_path, b.rel_path));

  if (packages.length) {
    const ph = el("h3", "muted", "Packages (" + packages.length + ")");
    ph.style.marginTop = "16px";
    container.appendChild(ph);
    const pkgGrid = el("div", "cards");
    for (const pkg of packages) {
      const sym = symbolById.get(pkg.id);
      const card = el("div", "card clickable");
      card.style.setProperty("--card-accent", moduleColor(sym ? sym.module : ""));
      card.addEventListener("click", () => openInspector(pkg.id));
      const head = el("div", "card-head");
      head.appendChild(el("span", "card-title mono", pkg.rel_path));
      head.appendChild(stateBadge(pkg.sync_state));
      card.appendChild(head);
      card.appendChild(el("div", "card-stats", fmt(pkg.prod_loc) + " LOC · " + fmt(pkg.exported) + " exported · " + fmt(pkg.fan_in) + " in / " + fmt(pkg.fan_out) + " out"));
      if (pkg.summary && pkg.summary.intent) {
        card.appendChild(el("div", "intent", pkg.summary.intent));
        addPills(card, pkg.summary.boundaries);
      } else {
        card.appendChild(el("div", "summary-empty", "No cached package summary."));
      }
      pkgGrid.appendChild(card);
    }
    container.appendChild(pkgGrid);
  }

  if (!modules.length && !packages.length) {
    container.appendChild(el("div", "empty", "No modules or packages match \"" + q + "\"."));
  }
}

/* ==================== Hotspots & Tech Debt Table ==================== */

const PAGE_SIZE = 200;
const hotspotState = {
  query: "",
  kind: "all",
  module: "",
  state: "all",
  eligible: false,
  dead: false,
  high: false,
  exported: false,
  sideEffects: false,
  includeTests: false,
  sort: "risk",
  direction: -1,
  limit: PAGE_SIZE,
};
let lastFilteredHotspots = [];

function resetHotspotFilters(overrides = {}) {
  hotspotState.query = "";
  hotspotState.kind = "all";
  hotspotState.module = "";
  hotspotState.state = "all";
  hotspotState.eligible = false;
  hotspotState.dead = false;
  hotspotState.high = false;
  hotspotState.exported = false;
  hotspotState.sideEffects = false;
  hotspotState.includeTests = false;
  hotspotState.limit = PAGE_SIZE;
  Object.assign(hotspotState, overrides);
  setupHotspots();
}

function setupHotspots() {
  const controls = $("#hotspot-controls");
  clear(controls);

  const search = el("input");
  search.type = "search";
  search.placeholder = "Filter symbol, package, file…";
  search.value = hotspotState.query;
  search.addEventListener("input", () => {
    hotspotState.query = search.value.toLowerCase();
    hotspotState.limit = PAGE_SIZE;
    renderHotspots();
  });
  controls.appendChild(search);

  const kindSel = el("select");
  kindSel.appendChild(new Option("All kinds", "all"));
  for (const k of ["func", "method", "type", "file"]) kindSel.appendChild(new Option(k, k));
  kindSel.value = hotspotState.kind;
  kindSel.addEventListener("change", () => {
    hotspotState.kind = kindSel.value;
    hotspotState.limit = PAGE_SIZE;
    renderHotspots();
  });
  controls.appendChild(kindSel);

  if (moduleNames.length > 1) {
    const modSel = el("select");
    modSel.appendChild(new Option("All modules", ""));
    for (const m of moduleNames) modSel.appendChild(new Option(m, m));
    modSel.value = hotspotState.module;
    modSel.addEventListener("change", () => {
      hotspotState.module = modSel.value;
      hotspotState.limit = PAGE_SIZE;
      renderHotspots();
    });
    controls.appendChild(modSel);
  }

  const stateSel = el("select");
  stateSel.appendChild(new Option("All summary states", "all"));
  for (const st of ["synced", "soft_stale", "hard_stale", "missing", "not_eligible"]) {
    stateSel.appendChild(new Option(st, st));
  }
  stateSel.value = hotspotState.state;
  stateSel.addEventListener("change", () => {
    hotspotState.state = stateSel.value;
    hotspotState.limit = PAGE_SIZE;
    renderHotspots();
  });
  controls.appendChild(stateSel);

  const toggles = [
    ["eligible", "Eligible"],
    ["dead", "Dead code"],
    ["high", "Complexity ≥10"],
    ["exported", "Exported"],
    ["sideEffects", "Side effects"],
    ["includeTests", "Include tests"],
  ];
  for (const [key, label] of toggles) {
    const wrapper = el("label", "toggle-chip");
    if (key === "includeTests") wrapper.title = "Include test and generated symbols";
    const input = el("input");
    input.type = "checkbox";
    input.checked = Boolean(hotspotState[key]);
    input.addEventListener("change", () => {
      hotspotState[key] = input.checked;
      hotspotState.limit = PAGE_SIZE;
      renderHotspots();
    });
    wrapper.appendChild(input);
    wrapper.appendChild(document.createTextNode(label));
    controls.appendChild(wrapper);
  }

  controls.appendChild(el("div", "spacer"));
  controls.appendChild(button("Export CSV", "ghost", exportHotspotsCSV));
  renderHotspots();
}

function exportHotspotsCSV() {
  const esc = (v) => {
    const s = String(v == null ? "" : v);
    return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
  };
  const header = ["id", "name", "kind", "package", "file", "loc", "complexity", "fan_in", "risk_score", "dead", "sync_state", "side_effects"];
  const lines = [header.join(",")];
  for (const s of lastFilteredHotspots) {
    lines.push([
      s.id, s.name, s.kind, s.pkg_path, s.file, s.loc || 0, s.complexity || 0,
      s.fan_in || 0, riskScore(s), s.dead ? "yes" : "no", s.sync_state || "",
      realSideEffects(s.summary).join(";")
    ].map(esc).join(","));
  }
  const blob = new Blob([lines.join("\n")], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = el("a");
  a.href = url;
  a.download = (DATA.repo || "codebase") + "-hotspots.csv";
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
  showToast("Exported " + fmt(lastFilteredHotspots.length) + " rows to CSV");
}

function renderHotspots() {
  const root = $("#hotspot-table");
  clear(root);
  const allowedKinds = new Set(["func", "method", "type", "file"]);

  const rows = symbols.filter((s) => {
    if (!allowedKinds.has(s.kind)) return false;
    if (!hotspotState.includeTests && (s.is_test || s.is_generated)) return false;
    if (hotspotState.kind !== "all" && s.kind !== hotspotState.kind) return false;
    if (hotspotState.module && s.module !== hotspotState.module) return false;
    if (hotspotState.state !== "all" && (s.sync_state || "missing") !== hotspotState.state) return false;
    if (hotspotState.eligible && !s.eligible) return false;
    if (hotspotState.dead && !s.dead) return false;
    if (hotspotState.high && (s.complexity || 0) < 10) return false;
    if (hotspotState.exported && !s.exported) return false;
    if (hotspotState.sideEffects && !realSideEffects(s.summary).length) return false;
    if (!hotspotState.query) return true;
    return [s.name, s.id, s.pkg_path, s.file, s.module].join(" ").toLowerCase().includes(hotspotState.query);
  });

  const val = (s, key) => {
    switch (key) {
      case "symbol": return s.name || "";
      case "kind": return s.kind || "";
      case "package": return s.pkg_path || "";
      case "loc": return s.loc || 0;
      case "complexity": return s.complexity || 0;
      case "fanIn": return s.fan_in || 0;
      case "risk": return riskScore(s);
      case "dead": return s.dead ? 1 : 0;
      case "state": return s.sync_state || "";
      case "sideEffects": return realSideEffects(s.summary).join(",");
      default: return "";
    }
  };
  rows.sort((a, b) => {
    const av = val(a, hotspotState.sort), bv = val(b, hotspotState.sort);
    if (av === bv) return (a.name || a.id).localeCompare(b.name || b.id);
    if (typeof av === "number" && typeof bv === "number") return (av - bv) * hotspotState.direction;
    return String(av).localeCompare(String(bv)) * hotspotState.direction;
  });
  lastFilteredHotspots = rows;

  if (!rows.length) {
    root.appendChild(el("div", "empty", "No symbols match the current filters."));
    return;
  }

  const maxRisk = Math.max(10, ...rows.slice(0, 50).map(riskScore));
  const maxComplexity = Math.max(10, ...rows.slice(0, 50).map((s) => s.complexity || 0));

  const scroller = el("div", "table-scroll");
  const table = el("table");
  const thead = el("thead");
  const headRow = el("tr");
  const cols = [
    ["symbol", "Symbol", false],
    ["kind", "Kind", false],
    ["package", "Package", false],
    ["risk", "Risk", true],
    ["loc", "LOC", true],
    ["complexity", "Complexity", true],
    ["fanIn", "Fan-In", true],
    ["dead", "Dead", false],
    ["state", "State", false],
    ["sideEffects", "Side Effects", false],
  ];
  for (const [key, label, numeric] of cols) {
    const isSorted = hotspotState.sort === key;
    const arrow = isSorted ? (hotspotState.direction > 0 ? " ▲" : " ▼") : "";
    const th = el("th", "sortable" + (numeric ? " num" : "") + (isSorted ? " sort-active" : ""), label + arrow);
    th.addEventListener("click", () => {
      if (hotspotState.sort === key) hotspotState.direction *= -1;
      else {
        hotspotState.sort = key;
        hotspotState.direction = numeric || key === "dead" ? -1 : 1;
      }
      renderHotspots();
    });
    headRow.appendChild(th);
  }
  thead.appendChild(headRow);
  table.appendChild(thead);

  const tbody = el("tbody");
  const visible = rows.slice(0, hotspotState.limit);
  for (const s of visible) {
    const row = el("tr", "clickable");
    row.addEventListener("click", () => openInspector(s.id));
    row.appendChild(el("td", "mono", s.name || s.id));
    row.appendChild(el("td", null, s.kind));
    row.appendChild(el("td", "mono muted", s.pkg_path || s.module || "—"));

    const riskCell = el("td", "num");
    riskCell.appendChild(makeMeter(riskScore(s), maxRisk, 12, 28));
    row.appendChild(riskCell);

    row.appendChild(el("td", "num", fmt(s.loc)));

    const cCell = el("td", "num");
    cCell.appendChild(makeMeter(s.complexity || 0, maxComplexity, 10, 20));
    row.appendChild(cCell);

    row.appendChild(el("td", "num", fmt(s.fan_in)));

    const deadCell = el("td");
    deadCell.appendChild(s.dead ? el("span", "pill bad", "dead") : el("span", "muted", "—"));
    row.appendChild(deadCell);

    const stateCell = el("td");
    stateCell.appendChild(stateBadge(s.sync_state));
    row.appendChild(stateCell);

    const effCell = el("td");
    const effects = realSideEffects(s.summary);
    if (effects.length) addPills(effCell, effects, "warn");
    else effCell.appendChild(el("span", "muted", "—"));
    row.appendChild(effCell);

    tbody.appendChild(row);
  }
  table.appendChild(tbody);
  scroller.appendChild(table);
  root.appendChild(scroller);

  const footer = el("div", "table-footer");
  footer.appendChild(el("span", null, "Showing " + fmt(visible.length) + " of " + fmt(rows.length) + " matching symbols"));
  if (rows.length > visible.length) {
    const actions = el("div", "pills");
    actions.appendChild(button("Load +" + PAGE_SIZE, "ghost btn-xs", () => {
      hotspotState.limit += PAGE_SIZE;
      renderHotspots();
    }));
    actions.appendChild(button("Show all (" + fmt(rows.length) + ")", "ghost btn-xs", () => {
      hotspotState.limit = rows.length;
      renderHotspots();
    }));
    footer.appendChild(actions);
  }
  root.appendChild(footer);
}

/* ==================== Interfaces Matrix ==================== */

function renderInterfaces() {
  const root = $("#interfaces-content");
  clear(root);
  const query = ($("#interface-search").value || "").toLowerCase();
  const mode = $("#interface-filter").value || "all";

  const items = (DATA.interfaces || []).filter((iface) => {
    const implCount = (iface.implementations || []).length;
    if (mode === "implemented" && implCount === 0) return false;
    if (mode === "unimplemented" && implCount > 0) return false;
    if (!query) return true;
    const hay = [
      iface.name, iface.id, iface.pkg_path,
      ...(iface.implementations || []).map((i) => i.name + " " + i.pkg_path)
    ].join(" ").toLowerCase();
    return hay.includes(query);
  });

  $("#interface-count").textContent = fmt(items.length) + " of " + fmt((DATA.interfaces || []).length) + " interfaces";
  if (!items.length) {
    root.appendChild(el("div", "empty", "No matching interfaces."));
    return;
  }

  const cards = el("div", "cards");
  for (const iface of items) {
    const sym = symbolById.get(iface.id);
    const card = el("div", "card");
    card.style.setProperty("--card-accent", moduleColor(sym ? sym.module : ""));
    const head = el("div", "card-head");
    head.appendChild(button(iface.name, "linkish mono", () => openInspector(iface.id)));
    const badges = el("div", "pills");
    badges.appendChild(el("span", "pill", iface.methods + " methods"));
    const implCount = (iface.implementations || []).length;
    badges.appendChild(el("span", "pill " + (implCount ? "good" : "warn"), implCount + " impls"));
    head.appendChild(badges);
    card.appendChild(head);
    card.appendChild(el("div", "muted mono", iface.pkg_path));

    const impls = el("div", "relation");
    if (implCount) {
      for (const impl of iface.implementations) {
        const label = impl.name + (impl.via_ptr ? " (*ptr)" : "");
        const btn = button(label, "linkish mono", () => openInspector(impl.id));
        btn.title = impl.pkg_path;
        impls.appendChild(btn);
      }
    } else {
      impls.appendChild(el("span", "summary-empty", "No concrete implementations indexed."));
    }
    card.appendChild(impls);
    cards.appendChild(card);
  }
  root.appendChild(cards);
}

/* ==================== Hierarchy Tree ==================== */

function countLeaves(node) {
  if (!node.children || !node.children.length) return 1;
  let total = 0;
  for (const c of node.children) total += countLeaves(c);
  return total;
}

function renderTree() {
  const root = $("#tree-content");
  clear(root);
  const query = ($("#tree-search").value || "").trim().toLowerCase();
  const tree = DATA.tree || [];

  if (query) {
    const matches = [];
    walkTree(tree, (node) => {
      if ([node.label, node.id, node.kind, node.sync_state].join(" ").toLowerCase().includes(query)) {
        matches.push(node);
      }
    });
    $("#tree-count").textContent = fmt(matches.length) + " matches";
    if (!matches.length) {
      root.appendChild(el("div", "empty", "No hierarchy nodes match \"" + query + "\"."));
      return;
    }
    for (const node of matches.slice(0, 300)) {
      const row = el("div", "leaf");
      row.appendChild(button(node.label, "tree-link mono", () => openInspector(node.id)));
      row.appendChild(el("span", "pill", node.kind));
      row.appendChild(stateBadge(node.sync_state));
      root.appendChild(row);
    }
    if (matches.length > 300) {
      root.appendChild(el("div", "muted", "Showing first 300 of " + fmt(matches.length) + " matches."));
    }
    return;
  }

  let totalNodes = 0;
  walkTree(tree, () => { totalNodes++; });
  $("#tree-count").textContent = fmt(totalNodes) + " nodes";
  for (const node of tree) root.appendChild(treeElement(node));
}

function walkTree(nodes, visit) {
  for (const node of nodes || []) {
    visit(node);
    walkTree(node.children, visit);
  }
}

function treeElement(node) {
  if (!node.children || !node.children.length) {
    const row = el("div", "leaf");
    row.appendChild(button(node.label, "tree-link mono", () => openInspector(node.id)));
    row.appendChild(el("span", "pill", node.kind));
    row.appendChild(stateBadge(node.sync_state));
    return row;
  }
  const details = el("details");
  details.open = node.kind === "module" || node.kind === "package";
  const summary = el("summary");
  summary.appendChild(button(node.label, "tree-link mono", (event) => {
    event.stopPropagation();
    openInspector(node.id);
  }));
  summary.appendChild(el("span", "pill", node.kind));
  summary.appendChild(el("span", "muted", "(" + countLeaves(node) + ")"));
  summary.appendChild(stateBadge(node.sync_state));
  details.appendChild(summary);
  for (const child of node.children) details.appendChild(treeElement(child));
  return details;
}

/* ==================== Interactive Dependency Graph ==================== */

const graphState = {
  mode: oneToOneModules ? "packages" : "modules",
  module: "",
  query: "",
  transitive: false,
  scale: 1,
  tx: 24,
  ty: 24,
  selected: null,
  bounds: { width: 800, height: 500 },
  interactionsBound: false,
};

function setupGraph() {
  const controls = $("#graph-controls");
  clear(controls);

  const mode = el("select");
  mode.id = "graph-mode-select";
  mode.appendChild(new Option("Modules graph", "modules"));
  mode.appendChild(new Option("Packages graph", "packages"));
  mode.value = graphState.mode;
  mode.addEventListener("change", () => {
    graphState.mode = mode.value;
    graphState.selected = null;
    updateGraphControls();
    renderGraph(true);
  });
  controls.appendChild(mode);

  const moduleSelect = el("select");
  moduleSelect.id = "graph-module-filter";
  moduleSelect.addEventListener("change", () => {
    graphState.module = moduleSelect.value;
    graphState.selected = null;
    renderGraph(true);
  });
  controls.appendChild(moduleSelect);

  const search = el("input");
  search.type = "search";
  search.placeholder = "Highlight nodes…";
  search.id = "graph-search";
  search.value = graphState.query;
  search.addEventListener("input", () => {
    graphState.query = search.value.toLowerCase();
    applyGraphSearch($("#graph-svg"));
  });
  controls.appendChild(search);

  const transLabel = el("label", "toggle-chip");
  const transCheck = el("input");
  transCheck.type = "checkbox";
  transCheck.checked = graphState.transitive;
  transCheck.addEventListener("change", () => {
    graphState.transitive = transCheck.checked;
    if (graphState.selected) renderGraph(false);
  });
  transLabel.appendChild(transCheck);
  transLabel.appendChild(document.createTextNode("Transitive highlight"));
  controls.appendChild(transLabel);

  controls.appendChild(el("div", "spacer"));
  controls.appendChild(button("−", "icon-btn", () => zoomGraphBy(0.8)));
  controls.appendChild(button("+", "icon-btn", () => zoomGraphBy(1.25)));
  controls.appendChild(button("Fit", "ghost", () => fitGraphToViewport()));
  controls.appendChild(button("Reset", "ghost", () => {
    graphState.selected = null;
    graphState.query = "";
    search.value = "";
    renderGraph(true);
  }));

  updateGraphControls();
  bindGraphInteractions();
}

function updateGraphControls() {
  const modeSel = $("#graph-mode-select");
  if (modeSel) modeSel.value = graphState.mode;
  const moduleSelect = $("#graph-module-filter");
  if (!moduleSelect) return;
  clear(moduleSelect);
  moduleSelect.appendChild(new Option("All modules", ""));
  for (const mod of DATA.modules || []) moduleSelect.appendChild(new Option(mod.name, mod.name));
  moduleSelect.value = graphState.module;
}

function currentGraph() {
  const graph = (DATA.graphs && DATA.graphs[graphState.mode]) || { nodes: [], edges: [] };
  const nodes = (graph.nodes || []).filter((n) => !graphState.module || n.module === graphState.module);
  const ids = new Set(nodes.map((n) => n.id));
  const edges = (graph.edges || []).filter((e) => ids.has(e.from) && ids.has(e.to));
  return { nodes, edges };
}

function applyViewportTransform() {
  const vp = $("#graph-viewport");
  if (vp) {
    vp.setAttribute("transform", "translate(" + graphState.tx.toFixed(1) + "," + graphState.ty.toFixed(1) + ") scale(" + graphState.scale.toFixed(3) + ")");
  }
}

function zoomGraphBy(factor, cx, cy) {
  const svg = $("#graph-svg");
  const rect = svg.getBoundingClientRect();
  const px = cx !== undefined ? cx : rect.width / 2;
  const py = cy !== undefined ? cy : rect.height / 2;
  const prev = graphState.scale;
  const next = Math.max(0.15, Math.min(3.5, prev * factor));
  const ratio = next / prev;
  graphState.tx = px - (px - graphState.tx) * ratio;
  graphState.ty = py - (py - graphState.ty) * ratio;
  graphState.scale = next;
  applyViewportTransform();
}

function fitGraphToViewport() {
  const svg = $("#graph-svg");
  const rect = svg.getBoundingClientRect();
  if (!rect.width || !rect.height) return;
  const pad = 40;
  const scaleX = (rect.width - pad * 2) / Math.max(1, graphState.bounds.width);
  const scaleY = (rect.height - pad * 2) / Math.max(1, graphState.bounds.height);
  graphState.scale = Math.max(0.2, Math.min(1.25, Math.min(scaleX, scaleY)));
  graphState.tx = (rect.width - graphState.bounds.width * graphState.scale) / 2;
  graphState.ty = Math.max(24, (rect.height - graphState.bounds.height * graphState.scale) / 2);
  applyViewportTransform();
}

function bindGraphInteractions() {
  if (graphState.interactionsBound) return;
  graphState.interactionsBound = true;
  const svg = $("#graph-svg");

  svg.addEventListener("wheel", (event) => {
    event.preventDefault();
    const rect = svg.getBoundingClientRect();
    zoomGraphBy(event.deltaY < 0 ? 1.12 : 0.89, event.clientX - rect.left, event.clientY - rect.top);
  }, { passive: false });

  let dragging = false, moved = false, lastX = 0, lastY = 0;
  svg.addEventListener("pointerdown", (event) => {
    if (event.target.closest && event.target.closest(".graph-node")) return;
    dragging = true;
    moved = false;
    lastX = event.clientX;
    lastY = event.clientY;
    svg.classList.add("dragging");
    svg.setPointerCapture(event.pointerId);
  });
  svg.addEventListener("pointermove", (event) => {
    if (!dragging) return;
    const dx = event.clientX - lastX;
    const dy = event.clientY - lastY;
    if (Math.abs(dx) > 2 || Math.abs(dy) > 2) moved = true;
    graphState.tx += dx;
    graphState.ty += dy;
    lastX = event.clientX;
    lastY = event.clientY;
    applyViewportTransform();
  });
  const endDrag = () => {
    if (!dragging) return;
    dragging = false;
    svg.classList.remove("dragging");
    if (!moved && graphState.selected) {
      graphState.selected = null;
      clearGraphHighlight(svg);
    }
  };
  svg.addEventListener("pointerup", endDrag);
  svg.addEventListener("pointercancel", endDrag);
}

function addArrowMarker(defs, id, color) {
  const marker = svgElement("marker", {
    id, viewBox: "0 0 10 10", refX: 9, refY: 5,
    markerWidth: 6, markerHeight: 6, orient: "auto-start-reverse",
  });
  marker.appendChild(svgElement("path", { d: "M 0 1.5 L 9 5 L 0 8.5 z", fill: color }));
  defs.appendChild(marker);
}

function renderGraph(autoFit = true) {
  const svg = $("#graph-svg");
  const hud = $("#graph-hud");
  clear(svg);
  clear(hud);

  const graph = currentGraph();
  if (!graph.nodes.length) {
    svg.appendChild(svgElement("text", { x: 28, y: 44, fill: "var(--muted)" })).textContent = "No nodes match the current filter.";
    return;
  }

  const defs = svgElement("defs");
  addArrowMarker(defs, "arrow-muted", "var(--muted)");
  addArrowMarker(defs, "arrow-out", "var(--out-edge)");
  addArrowMarker(defs, "arrow-in", "var(--in-edge)");
  addArrowMarker(defs, "arrow-cycle", "var(--bad)");
  svg.appendChild(defs);

  const nodeWidth = 196, nodeHeight = 46, columnGap = 272, rowGap = 72;
  const maxLayer = Math.max(0, ...graph.nodes.map((n) => n.layer || 0));
  const maxOrder = Math.max(0, ...graph.nodes.map((n) => n.order || 0));
  graphState.bounds = {
    width: 40 + maxLayer * columnGap + nodeWidth + 40,
    height: 40 + maxOrder * rowGap + nodeHeight + 40,
  };

  const viewport = svgElement("g", { id: "graph-viewport" });
  svg.appendChild(viewport);

  const positions = new Map();
  const nodeById = new Map();
  for (const node of graph.nodes) {
    nodeById.set(node.id, node);
    positions.set(node.id, {
      x: 40 + (node.layer || 0) * columnGap,
      y: 40 + (node.order || 0) * rowGap,
    });
  }

  const outMap = new Map();
  const inMap = new Map();
  let cycleCount = 0;

  // Edge layer first (behind nodes)
  const edgeLayer = svgElement("g", { class: "edge-layer" });
  for (const edge of graph.edges) {
    const fromPos = positions.get(edge.from);
    const toPos = positions.get(edge.to);
    const fromNode = nodeById.get(edge.from);
    const toNode = nodeById.get(edge.to);
    if (!fromPos || !toPos) continue;

    const isBackEdge = (fromNode.layer || 0) >= (toNode.layer || 0);
    if (isBackEdge) cycleCount++;

    let d;
    if (isBackEdge) {
      const x1 = fromPos.x + nodeWidth / 2;
      const y1 = fromPos.y;
      const x2 = toPos.x + nodeWidth / 2;
      const y2 = toPos.y;
      const arch = 34 + Math.abs(x1 - x2) * 0.14;
      d = "M " + x1 + " " + y1 + " C " + x1 + " " + (y1 - arch) + ", " + x2 + " " + (y2 - arch) + ", " + x2 + " " + (y2 - 2);
    } else {
      const x1 = fromPos.x + nodeWidth;
      const y1 = fromPos.y + nodeHeight / 2;
      const x2 = toPos.x - 2;
      const y2 = toPos.y + nodeHeight / 2;
      const dx = Math.max(36, (x2 - x1) * 0.45);
      d = "M " + x1 + " " + y1 + " C " + (x1 + dx) + " " + y1 + ", " + (x2 - dx) + " " + y2 + ", " + x2 + " " + y2;
    }

    const path = svgElement("path", {
      d,
      class: "graph-edge" + (isBackEdge ? " cycle" : ""),
      "marker-end": isBackEdge ? "url(#arrow-cycle)" : "url(#arrow-muted)",
    });
    path.dataset.from = edge.from;
    path.dataset.to = edge.to;
    path.dataset.cycle = isBackEdge ? "1" : "0";
    edgeLayer.appendChild(path);

    if (!outMap.has(edge.from)) outMap.set(edge.from, []);
    if (!inMap.has(edge.to)) inMap.set(edge.to, []);
    outMap.get(edge.from).push(edge.to);
    inMap.get(edge.to).push(edge.from);
  }
  viewport.appendChild(edgeLayer);

  // Node layer
  const nodeLayer = svgElement("g", { class: "node-layer" });
  for (const node of graph.nodes) {
    const pos = positions.get(node.id);
    const sym = symbolById.get(node.id);
    const group = svgElement("g", {
      class: "graph-node",
      transform: "translate(" + pos.x + "," + pos.y + ")",
      "data-id": node.id,
    });

    group.appendChild(svgElement("rect", { class: "node-bg", width: nodeWidth, height: nodeHeight, rx: 7 }));
    // Module color accent stripe
    group.appendChild(svgElement("rect", {
      x: 0, y: 0, width: 5, height: nodeHeight, rx: 2,
      fill: moduleColor(node.module || node.label),
    }));
    // Sync state dot
    group.appendChild(svgElement("circle", {
      cx: nodeWidth - 11, cy: 13, r: 3.5,
      fill: stateColor(node.sync_state),
    }));

    const label = node.label.length > 23 ? "…" + node.label.slice(-22) : node.label;
    const titleEl = svgElement("text", { class: "node-title", x: 12, y: 19 });
    titleEl.textContent = label;
    group.appendChild(titleEl);

    const degIn = (inMap.get(node.id) || []).length;
    const degOut = (outMap.get(node.id) || []).length;
    const subParts = ["in " + degIn + " · out " + degOut];
    if (sym && sym.loc) subParts.push(fmt(sym.loc) + " LOC");
    const subEl = svgElement("text", { class: "node-sub", x: 12, y: 35 });
    subEl.textContent = subParts.join(" · ");
    group.appendChild(subEl);

    const tooltip = svgElement("title");
    tooltip.textContent = node.label + " (" + node.kind + " · " + (node.sync_state || "missing") + " · in:" + degIn + " out:" + degOut + ")";
    group.appendChild(tooltip);

    group.addEventListener("click", (event) => {
      event.stopPropagation();
      graphState.selected = node.id;
      highlightGraph(svg, node.id, outMap, inMap);
      openInspector(node.id);
    });
    if (graphState.mode === "modules") {
      group.addEventListener("dblclick", (event) => {
        event.stopPropagation();
        graphState.mode = "packages";
        graphState.module = node.label;
        graphState.selected = null;
        updateGraphControls();
        renderGraph(true);
      });
    }
    nodeLayer.appendChild(group);
  }
  viewport.appendChild(nodeLayer);

  // HUD Pills
  hud.appendChild(el("span", "pill", fmt(graph.nodes.length) + " nodes"));
  hud.appendChild(el("span", "pill", fmt(graph.edges.length) + " edges"));
  if (cycleCount > 0) hud.appendChild(el("span", "pill bad", fmt(cycleCount) + " back-edges"));

  if (autoFit) fitGraphToViewport();
  else applyViewportTransform();

  applyGraphSearch(svg);
  if (graphState.selected && nodeById.has(graphState.selected)) {
    highlightGraph(svg, graphState.selected, outMap, inMap);
  }
}

function collectReachable(start, adj, transitive) {
  const visited = new Set();
  const queue = [start];
  while (queue.length) {
    const cur = queue.shift();
    for (const next of adj.get(cur) || []) {
      if (visited.has(next)) continue;
      visited.add(next);
      if (transitive) queue.push(next);
    }
  }
  return visited;
}

function clearGraphHighlight(svg) {
  for (const n of $$(".graph-node", svg)) n.classList.remove("dim", "selected");
  for (const e of $$(".graph-edge", svg)) {
    e.classList.remove("dim", "active-out", "active-in");
    e.setAttribute("marker-end", e.dataset.cycle === "1" ? "url(#arrow-cycle)" : "url(#arrow-muted)");
  }
}

function highlightGraph(svg, id, outMap, inMap) {
  const outNodes = collectReachable(id, outMap, graphState.transitive);
  const inNodes = collectReachable(id, inMap, graphState.transitive);
  const keep = new Set([id, ...outNodes, ...inNodes]);

  for (const node of $$(".graph-node", svg)) {
    const nid = node.dataset.id;
    node.classList.toggle("dim", !keep.has(nid));
    node.classList.toggle("selected", nid === id);
  }
  for (const edge of $$(".graph-edge", svg)) {
    const from = edge.dataset.from;
    const to = edge.dataset.to;
    const isOut = graphState.transitive
      ? (from === id || outNodes.has(from)) && outNodes.has(to)
      : from === id;
    const isIn = graphState.transitive
      ? inNodes.has(from) && (to === id || inNodes.has(to))
      : to === id;

    edge.classList.toggle("active-out", isOut);
    edge.classList.toggle("active-in", !isOut && isIn);
    edge.classList.toggle("dim", !isOut && !isIn);

    if (edge.dataset.cycle === "1") {
      edge.setAttribute("marker-end", "url(#arrow-cycle)");
    } else if (isOut) {
      edge.setAttribute("marker-end", "url(#arrow-out)");
    } else if (isIn) {
      edge.setAttribute("marker-end", "url(#arrow-in)");
    } else {
      edge.setAttribute("marker-end", "url(#arrow-muted)");
    }
  }
}

function applyGraphSearch(svg) {
  for (const node of $$(".graph-node", svg)) {
    const hit = graphState.query && (node.dataset.id || "").toLowerCase().includes(graphState.query);
    node.classList.toggle("match", Boolean(hit));
  }
}

/* ==================== Global Search with Ranking & Keyboard Nav ==================== */

let searchMatches = [];
let searchCursor = -1;

function scoreMatch(symbol, query) {
  const name = (symbol.name || "").toLowerCase();
  if (name === query) return 100;
  if (name.startsWith(query)) return 75;
  if (name.includes(query)) return 50;
  if ((symbol.pkg_path || "").toLowerCase().includes(query)) return 30;
  if ((symbol.file || "").toLowerCase().includes(query)) return 20;
  if ((symbol.id || "").toLowerCase().includes(query)) return 10;
  return 0;
}

function globalSearch(raw) {
  const results = $("#search-results");
  clear(results);
  const query = (raw || "").trim().toLowerCase();
  searchCursor = -1;
  if (!query) {
    searchMatches = [];
    results.hidden = true;
    return;
  }
  searchMatches = symbols
    .map((s) => ({ s, score: scoreMatch(s, query) }))
    .filter((x) => x.score > 0)
    .sort((a, b) => (b.score - a.score) || ((b.s.fan_in || 0) - (a.s.fan_in || 0)) || (a.s.name || "").localeCompare(b.s.name || ""))
    .slice(0, 25)
    .map((x) => x.s);

  results.hidden = false;
  if (!searchMatches.length) {
    results.appendChild(el("div", "empty", "No matching symbols."));
    return;
  }

  searchMatches.forEach((symbol, idx) => {
    const item = el("button", "search-item");
    item.type = "button";
    item.setAttribute("role", "option");
    const main = el("div", "search-item-main");
    main.appendChild(el("div", "search-item-name mono", symbol.name || symbol.id));
    main.appendChild(el("div", "search-item-sub mono", symbol.pkg_path || symbol.file || symbol.module || symbol.id));
    const tags = el("div", "search-item-tags");
    tags.appendChild(el("span", "pill", symbol.kind));
    tags.appendChild(stateBadge(symbol.sync_state));
    item.appendChild(main);
    item.appendChild(tags);
    item.addEventListener("click", () => selectSearchItem(idx));
    results.appendChild(item);
  });
  setSearchCursor(0);
}

function setSearchCursor(idx) {
  const items = $$(".search-item", $("#search-results"));
  if (!items.length) return;
  searchCursor = (idx + items.length) % items.length;
  items.forEach((item, i) => {
    item.classList.toggle("active", i === searchCursor);
    if (i === searchCursor) item.scrollIntoView({ block: "nearest" });
  });
}

function selectSearchItem(idx) {
  const symbol = searchMatches[idx];
  if (!symbol) return;
  $("#search-results").hidden = true;
  $("#global-search").value = "";
  $("#global-search").blur();
  openInspector(symbol.id);
}

/* ==================== Tabs, Hash State & Shortcuts ==================== */

function activeTab() {
  const cur = $(".tab.active");
  return cur ? cur.dataset.tab : "overview";
}

function switchTab(tabName) {
  for (const tab of $$(".tab")) {
    const on = tab.dataset.tab === tabName;
    tab.classList.toggle("active", on);
    tab.setAttribute("aria-selected", on ? "true" : "false");
  }
  for (const view of $$(".view")) view.classList.toggle("active", view.dataset.view === tabName);
  $("#search-results").hidden = true;
  if (tabName === "graph") {
    if (!$("#graph-controls").childNodes.length) setupGraph();
    else updateGraphControls();
    renderGraph(true);
  }
  const openId = $("#inspector").hidden ? null : inspectorHistory.stack[inspectorHistory.index];
  syncUrlHash(tabName, openId);
}

function syncUrlHash(tab, symbolId) {
  const next = "#" + encodeURIComponent(tab) + (symbolId ? "?id=" + encodeURIComponent(symbolId) : "");
  if (window.location.hash !== next) {
    try { history.replaceState(null, "", next); } catch (_) { /* ignore on restricted file:// */ }
  }
}

function restoreFromHash() {
  const raw = (window.location.hash || "").replace(/^#/, "");
  if (!raw) return;
  const [tabPart, queryPart] = raw.split("?");
  const tab = decodeURIComponent(tabPart || "");
  if (["overview", "graph", "hotspots", "interfaces", "tree"].includes(tab)) {
    switchTab(tab);
  }
  if (queryPart) {
    const params = new URLSearchParams(queryPart);
    const id = params.get("id");
    if (id && symbolById.has(id)) openInspector(id);
  }
}

function setupTheme() {
  const saved = localStorage.getItem("goray-theme");
  const prefersDark = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.dataset.theme = saved || (prefersDark ? "dark" : "light");
  const syncLabel = () => {
    $("#theme-toggle").textContent = document.documentElement.dataset.theme === "dark" ? "Light mode" : "Dark mode";
  };
  syncLabel();
  $("#theme-toggle").addEventListener("click", () => {
    document.documentElement.dataset.theme = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
    localStorage.setItem("goray-theme", document.documentElement.dataset.theme);
    syncLabel();
  });
}

function setupShortcuts() {
  const input = $("#global-search");
  input.addEventListener("input", () => globalSearch(input.value));
  input.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown") { event.preventDefault(); setSearchCursor(searchCursor + 1); }
    else if (event.key === "ArrowUp") { event.preventDefault(); setSearchCursor(searchCursor - 1); }
    else if (event.key === "Enter" && searchCursor >= 0) { event.preventDefault(); selectSearchItem(searchCursor); }
    else if (event.key === "Escape") { input.value = ""; globalSearch(""); input.blur(); }
  });

  document.addEventListener("click", (event) => {
    const results = $("#search-results");
    if (!results.hidden && !results.contains(event.target) && event.target !== input) {
      results.hidden = true;
    }
  });

  document.addEventListener("keydown", (event) => {
    const tag = (document.activeElement && document.activeElement.tagName || "").toLowerCase();
    const editing = tag === "input" || tag === "textarea" || tag === "select";

    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
      event.preventDefault();
      input.focus();
      input.select();
      return;
    }
    if (event.key === "/" && !editing) {
      event.preventDefault();
      input.focus();
      input.select();
      return;
    }
    if (event.key === "Escape") {
      if (!$("#search-results").hidden) $("#search-results").hidden = true;
      else if (!$("#inspector").hidden) closeInspector();
      return;
    }
    if (event.altKey && event.key === "ArrowLeft" && inspectorHistory.index > 0) {
      event.preventDefault();
      inspectorHistory.index--;
      openInspector(inspectorHistory.stack[inspectorHistory.index], false);
    }
    if (event.altKey && event.key === "ArrowRight" && inspectorHistory.index < inspectorHistory.stack.length - 1) {
      event.preventDefault();
      inspectorHistory.index++;
      openInspector(inspectorHistory.stack[inspectorHistory.index], false);
    }
  });
}

function setupPanels() {
  for (const tab of $$(".tab")) {
    tab.addEventListener("click", () => switchTab(tab.dataset.tab));
  }
  $("#inspector-close").addEventListener("click", closeInspector);
  $("#inspector-backdrop").addEventListener("click", closeInspector);
  $("#inspector-back").addEventListener("click", () => {
    if (inspectorHistory.index > 0) {
      inspectorHistory.index--;
      openInspector(inspectorHistory.stack[inspectorHistory.index], false);
    }
  });
  $("#inspector-forward").addEventListener("click", () => {
    if (inspectorHistory.index < inspectorHistory.stack.length - 1) {
      inspectorHistory.index++;
      openInspector(inspectorHistory.stack[inspectorHistory.index], false);
    }
  });
  $("#interface-search").addEventListener("input", renderInterfaces);
  $("#interface-filter").addEventListener("change", renderInterfaces);
  $("#tree-search").addEventListener("input", renderTree);
  $("#tree-expand").addEventListener("click", () => {
    for (const d of $$("#tree-content details")) d.open = true;
  });
  $("#tree-collapse").addEventListener("click", () => {
    for (const d of $$("#tree-content details")) d.open = false;
    for (const d of $$("#tree-content > details")) d.open = true;
  });
}

function init() {
  document.title = (DATA.repo || "goray") + " · Architecture Report";
  $("#repo-meta").textContent = [DATA.repo, DATA.module_path, "generated " + DATA.generated_at].filter(Boolean).join(" · ");

  const cov = DATA.coverage || {};
  const badge = $("#tab-summary-badge");
  badge.appendChild(el("span", "pill " + ((cov.percent || 0) >= 80 ? "good" : "accent"), "Coverage " + (cov.percent || 0) + "%"));
  if (DATA.metrics && DATA.metrics.dead > 0) {
    badge.appendChild(el("span", "pill bad", fmt(DATA.metrics.dead) + " dead"));
  }

  setupTheme();
  setupShortcuts();
  setupPanels();
  renderOverview();
  setupHotspots();
  renderInterfaces();
  renderTree();
  setupGraph();
  restoreFromHash();
}

init();
})();