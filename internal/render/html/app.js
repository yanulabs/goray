(() => {
"use strict";

const DATA = JSON.parse(document.getElementById("report-data").textContent);
const SVG_NS = ["http", "://www.w3.org/2000/svg"].join("");
const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));
const fmt = (value) => Number(value || 0).toLocaleString();
const symbolById = new Map(DATA.symbols.map((symbol) => [symbol.id, symbol]));

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
  for (const key of Object.keys(attrs || {})) node.setAttribute(key, attrs[key]);
  return node;
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

function summaryNode(summary, fallbackState) {
  const section = el("div", "section");
  section.appendChild(el("h3", null, "Structured summary"));
  if (!summary) {
    section.appendChild(el("div", "summary-empty", "No cached summary. Static index data is still available."));
    if (fallbackState) section.appendChild(stateBadge(fallbackState));
    return section;
  }
  if (summary.intent) section.appendChild(el("div", "summary-intent", summary.intent));
  addPills(section, summary.side_effects, "warn");
  addList(section, "Invariants", summary.invariants || summary.key_invariants);
  if (summary.concurrency) {
    section.appendChild(el("h3", null, "Concurrency"));
    section.appendChild(el("div", null, summary.concurrency));
  }
  addList(section, "Boundaries", summary.boundaries);
  const meta = el("div", "pills");
  meta.appendChild(stateBadge(summary.sync_state));
  if (summary.model_id) meta.appendChild(el("span", "pill", summary.model_id));
  section.appendChild(meta);
  if (summary.deps_changed) {
    section.appendChild(el("div", "notice", "Dependencies changed; the cached summary is soft-stale."));
  }
  return section;
}

function openInspector(id) {
  const symbol = symbolById.get(id);
  if (!symbol) return;
  const inspector = $("#inspector");
  const body = $("#inspector-body");
  $("#inspector-title").textContent = symbol.name || id;
  clear(body);

  body.appendChild(el("div", "id", symbol.id));
  const facts = el("div", "kv");
  const addFact = (key, value) => {
    facts.appendChild(el("div", "k", key));
    facts.appendChild(el("div", "v", value));
  };
  addFact("Kind", symbol.kind);
  if (symbol.file) addFact("Source", symbol.file + ":" + symbol.start_line + "-" + symbol.end_line);
  addFact("Package", symbol.pkg_path || "—");
  addFact("Module", symbol.module || "—");
  addFact("LOC", fmt(symbol.loc));
  addFact("Complexity", fmt(symbol.complexity));
  addFact("Fan-in / out", fmt(symbol.fan_in) + " / " + fmt(symbol.fan_out));
  addFact("Flags", [symbol.exported ? "exported" : "", symbol.dead ? "dead" : "", symbol.eligible ? "eligible" : "not eligible"].filter(Boolean).join(", ") || "—");
  facts.appendChild(el("div", "k", "Sync state"));
  const stateCell = el("div", "v");
  stateCell.appendChild(stateBadge(symbol.sync_state));
  facts.appendChild(stateCell);
  body.appendChild(facts);
  body.appendChild(summaryNode(symbol.summary, symbol.sync_state));

  const relations = el("div");
  addRelation("Direct callers", symbol.callers);
  addRelation("Direct callees", symbol.callees);

  function addRelation(title, ids) {
    if (!ids || !ids.length) return;
    relations.appendChild(el("h3", null, title));
    const row = el("div", "relation");
    for (const target of ids) {
      const targetSymbol = symbolById.get(target);
      row.appendChild(button(targetSymbol ? targetSymbol.name : target, "linkish", () => openInspector(target)));
    }
    relations.appendChild(row);
  }
  if (relations.childNodes.length) {
    const section = el("div", "section");
    section.appendChild(relations);
    body.appendChild(section);
  }

  const api = apiSymbols(symbol);
  if (api.length) {
    const section = el("div", "section");
    section.appendChild(el("h3", null, "Related exported API"));
    const row = el("div", "relation");
    for (const item of api.slice(0, 120)) {
      const label = item.kind === "method" ? "(" + item.pkg_path.split("/").pop() + ")." + item.name : item.name;
      row.appendChild(button(label, "linkish", () => openInspector(item.id)));
    }
    section.appendChild(row);
    if (api.length > 120) section.appendChild(el("div", "muted", "Showing 120 of " + api.length));
    body.appendChild(section);
  }

  inspector.hidden = false;
  inspector.scrollTop = 0;
}

function apiSymbols(symbol) {
  const wanted = new Set(["func", "method", "type"]);
  return DATA.symbols.filter((item) => {
    if (item.id === symbol.id || !item.exported || !wanted.has(item.kind)) return false;
    if (symbol.kind === "package") return item.pkg_path === symbol.pkg_path;
    if (symbol.kind === "module") return item.module === symbol.module;
    if (symbol.kind === "file") return item.file === symbol.file;
    return false;
  });
}

function closeInspector() { $("#inspector").hidden = true; }

function renderOverview() {
  const root = $("#overview-content");
  clear(root);
  const metrics = DATA.metrics;
  const coverage = DATA.coverage;

  const grid = el("div", "grid");
  const addMetric = (label, value, sub) => {
    const card = el("div", "metric");
    card.appendChild(el("div", "k", label));
    card.appendChild(el("div", "v", value));
    if (sub) card.appendChild(el("div", "s", sub));
    grid.appendChild(card);
  };
  addMetric("Modules", fmt(metrics.modules), "architectural groups");
  addMetric("Packages", fmt(metrics.packages), "Go packages");
  addMetric("Files", fmt(metrics.files), "production and tests");
  addMetric("Types", fmt(metrics.types), "types and interfaces");
  addMetric("Functions", fmt(metrics.funcs + metrics.methods), fmt(metrics.funcs) + " funcs + " + fmt(metrics.methods) + " methods");
  addMetric("Production LOC", fmt(metrics.loc), "indexed production lines");
  addMetric("Dead code", fmt(metrics.dead), "zero-reference functions/methods");
  addMetric("Summary coverage", coverage.percent + "%", fmt(coverage.synced) + " / " + fmt(coverage.eligible) + " eligible");
  root.appendChild(grid);

  const coveragePanel = el("div", "panel");
  coveragePanel.style.marginTop = "14px";
  coveragePanel.appendChild(el("h2", null, "Summary health"));
  const progress = el("div", "progress");
  const bar = el("span");
  bar.style.width = Math.max(0, Math.min(100, coverage.percent)) + "%";
  progress.appendChild(bar);
  coveragePanel.appendChild(progress);
  const coveragePills = el("div", "pills");
  coveragePills.appendChild(el("span", "pill good", "synced " + fmt(coverage.synced)));
  coveragePills.appendChild(el("span", "pill warn", "soft " + fmt(coverage.soft_stale)));
  coveragePills.appendChild(el("span", "pill bad", "hard " + fmt(coverage.hard_stale)));
  coveragePills.appendChild(el("span", "pill accent", "missing " + fmt(coverage.missing)));
  coveragePills.appendChild(el("span", "pill", "not eligible " + fmt(coverage.not_eligible)));
  coveragePanel.appendChild(coveragePills);
  root.appendChild(coveragePanel);

  const columns = el("div", "two-col");
  columns.style.marginTop = "14px";
  const risk = el("div", "panel");
  risk.appendChild(el("h2", null, "Top risk hotspots"));
  const riskTable = el("table");
  const riskHead = el("tr");
  for (const label of ["Symbol", "Kind", "Complexity", "Fan-in"]) riskHead.appendChild(el("th", null, label));
  riskTable.appendChild(el("thead")).appendChild(riskHead);
  const riskBody = el("tbody");
  for (const symbol of DATA.hotspots) {
    const row = el("tr", "clickable");
    row.addEventListener("click", () => openInspector(symbol.id));
    row.appendChild(el("td", "mono", symbol.name));
    row.appendChild(el("td", null, symbol.kind));
    row.appendChild(el("td", "num", fmt(symbol.complexity)));
    row.appendChild(el("td", "num", fmt(symbol.fan_in)));
    riskBody.appendChild(row);
  }
  riskTable.appendChild(riskBody);
  risk.appendChild(DATA.hotspots.length ? riskTable : el("div", "empty", "No complexity hotspots above the current threshold."));
  columns.appendChild(risk);

  const coupled = el("div", "panel");
  coupled.appendChild(el("h2", null, "Most coupled packages"));
  const coupledTable = el("table");
  const coupledHead = el("tr");
  for (const label of ["Package", "Fan-in", "Fan-out"]) coupledHead.appendChild(el("th", null, label));
  coupledTable.appendChild(el("thead")).appendChild(coupledHead);
  const coupledBody = el("tbody");
  for (const pkg of DATA.coupled_packages) {
    const row = el("tr", "clickable");
    row.addEventListener("click", () => openInspector("pkg:" + pkg.pkg));
    row.appendChild(el("td", "mono", pkg.pkg));
    row.appendChild(el("td", "num", fmt(pkg.fan_in)));
    row.appendChild(el("td", "num", fmt(pkg.fan_out)));
    coupledBody.appendChild(row);
  }
  coupledTable.appendChild(coupledBody);
  coupled.appendChild(DATA.coupled_packages.length ? coupledTable : el("div", "empty", "No package coupling data."));
  columns.appendChild(coupled);
  root.appendChild(columns);

  root.appendChild(el("h2", null, "Module architecture"));
  const moduleCards = el("div", "cards");
  for (const module of DATA.modules) {
    const card = el("div", "card clickable");
    card.addEventListener("click", () => openInspector(module.id));
    const heading = el("h3");
    heading.appendChild(el("span", "mono", module.name));
    heading.appendChild(stateBadge(module.sync_state));
    card.appendChild(heading);
    card.appendChild(el("div", "muted", fmt(module.packages) + " packages · " + fmt(module.prod_loc) + " prod LOC · " + fmt(module.out_modules) + " out / " + fmt(module.in_modules) + " in"));
    if (module.summary && module.summary.intent) {
      card.appendChild(el("div", "intent", module.summary.intent));
      addPills(card, module.summary.boundaries);
    } else {
      card.appendChild(el("div", "summary-empty", "No cached module summary; static metrics shown."));
    }
    moduleCards.appendChild(card);
  }
  root.appendChild(moduleCards);

  root.appendChild(el("h2", null, "Package architecture"));
  const packageCards = el("div", "cards");
  for (const pkg of DATA.packages) {
    const card = el("div", "card clickable");
    card.addEventListener("click", () => openInspector(pkg.id));
    const heading = el("h3");
    heading.appendChild(el("span", "mono", pkg.rel_path));
    heading.appendChild(stateBadge(pkg.sync_state));
    card.appendChild(heading);
    card.appendChild(el("div", "muted", fmt(pkg.prod_loc) + " prod LOC · " + fmt(pkg.exported) + " exported · " + fmt(pkg.fan_in) + " fan-in / " + fmt(pkg.fan_out) + " fan-out"));
    if (pkg.summary && pkg.summary.intent) {
      card.appendChild(el("div", "intent", pkg.summary.intent));
      addPills(card, pkg.summary.boundaries);
    } else {
      card.appendChild(el("div", "summary-empty", "No cached package summary; static metrics shown."));
    }
    packageCards.appendChild(card);
  }
  root.appendChild(packageCards);
}

const hotspotState = {
  query: "",
  eligible: false,
  dead: false,
  high: false,
  exported: false,
  sideEffects: false,
  sort: "complexity",
  direction: -1,
};

function setupHotspots() {
  const controls = $("#hotspot-controls");
  clear(controls);
  const search = el("input");
  search.type = "search";
  search.placeholder = "Filter symbols…";
  search.addEventListener("input", () => { hotspotState.query = search.value.toLowerCase(); renderHotspots(); });
  controls.appendChild(search);
  const filters = [
    ["eligible", "Eligible only"],
    ["dead", "Dead code only"],
    ["high", "High complexity (≥10)"],
    ["exported", "Exported only"],
    ["sideEffects", "Has side effects"],
  ];
  for (const [key, label] of filters) {
    const wrapper = el("label");
    const input = el("input");
    input.type = "checkbox";
    input.addEventListener("change", () => { hotspotState[key] = input.checked; renderHotspots(); });
    wrapper.appendChild(input);
    wrapper.appendChild(document.createTextNode(label));
    controls.appendChild(wrapper);
  }
  renderHotspots();
}

function renderHotspots() {
  const root = $("#hotspot-table");
  clear(root);
  const kinds = new Set(["func", "method", "type", "file"]);
  let rows = DATA.symbols.filter((symbol) => {
    if (!kinds.has(symbol.kind)) return false;
    if (hotspotState.eligible && !symbol.eligible) return false;
    if (hotspotState.dead && !symbol.dead) return false;
    if (hotspotState.high && symbol.complexity < 10) return false;
    if (hotspotState.exported && !symbol.exported) return false;
    if (hotspotState.sideEffects && !(symbol.summary && symbol.summary.side_effects && symbol.summary.side_effects.length)) return false;
    if (!hotspotState.query) return true;
    return [symbol.name, symbol.id, symbol.pkg_path, symbol.file, symbol.module].join(" ").toLowerCase().includes(hotspotState.query);
  });
  const value = (symbol, key) => {
    switch (key) {
      case "symbol": return symbol.name || "";
      case "kind": return symbol.kind || "";
      case "package": return symbol.pkg_path || "";
      case "loc": return symbol.loc || 0;
      case "complexity": return symbol.complexity || 0;
      case "fanIn": return symbol.fan_in || 0;
      case "dead": return symbol.dead ? 1 : 0;
      case "state": return symbol.sync_state || "";
      case "sideEffects": return (symbol.summary && symbol.summary.side_effects || []).join(",");
      default: return "";
    }
  };
  rows.sort((a, b) => {
    const av = value(a, hotspotState.sort), bv = value(b, hotspotState.sort);
    if (av === bv) return (a.name || a.id).localeCompare(b.name || b.id);
    if (typeof av === "number" && typeof bv === "number") return (av - bv) * hotspotState.direction;
    return String(av).localeCompare(String(bv)) * hotspotState.direction;
  });

  const count = el("div", "muted");
  count.style.marginBottom = "8px";
  count.textContent = "Showing " + fmt(rows.length) + " symbols";
  root.appendChild(count);
  if (!rows.length) { root.appendChild(el("div", "empty", "No symbols match the current filters.")); return; }

  const table = el("table");
  const thead = el("thead");
  const headRow = el("tr");
  const columns = [["symbol", "Symbol"], ["kind", "Kind"], ["package", "Package"], ["loc", "LOC"], ["complexity", "Complexity"], ["fanIn", "Fan-In"], ["dead", "Dead"], ["state", "Summary state"], ["sideEffects", "Side effects"]];
  for (const [key, label] of columns) {
    const th = el("th", null, label);
    th.addEventListener("click", () => {
      if (hotspotState.sort === key) hotspotState.direction *= -1;
      else { hotspotState.sort = key; hotspotState.direction = key === "symbol" || key === "kind" || key === "package" || key === "state" ? 1 : -1; }
      renderHotspots();
    });
    headRow.appendChild(th);
  }
  thead.appendChild(headRow);
  table.appendChild(thead);
  const tbody = el("tbody");
  for (const symbol of rows) {
    const row = el("tr", "clickable");
    row.addEventListener("click", () => openInspector(symbol.id));
    const nameCell = el("td", "mono", symbol.name || symbol.id);
    row.appendChild(nameCell);
    row.appendChild(el("td", null, symbol.kind));
    row.appendChild(el("td", "mono", symbol.pkg_path || symbol.module || "—"));
    row.appendChild(el("td", "num", fmt(symbol.loc)));
    row.appendChild(el("td", "num", fmt(symbol.complexity)));
    row.appendChild(el("td", "num", fmt(symbol.fan_in)));
    row.appendChild(el("td", symbol.dead ? "bad" : null, symbol.dead ? "yes" : "no"));
    const stateCell = el("td");
    stateCell.appendChild(stateBadge(symbol.sync_state));
    row.appendChild(stateCell);
    const effectsCell = el("td");
    if (symbol.summary && symbol.summary.side_effects && symbol.summary.side_effects.length) {
      const pills = el("div", "pills");
      for (const effect of symbol.summary.side_effects) pills.appendChild(el("span", "pill warn", effect));
      effectsCell.appendChild(pills);
    } else effectsCell.textContent = "—";
    row.appendChild(effectsCell);
    tbody.appendChild(row);
  }
  table.appendChild(tbody);
  root.appendChild(table);
}

function renderInterfaces() {
  const root = $("#interfaces-content");
  clear(root);
  const query = ($("#interface-search").value || "").toLowerCase();
  const items = DATA.interfaces.filter((iface) => {
    if (!query) return true;
    const haystack = [iface.name, iface.id, iface.pkg_path, ...iface.implementations.map((impl) => impl.name + " " + impl.pkg_path)].join(" ").toLowerCase();
    return haystack.includes(query);
  });
  if (!items.length) { root.appendChild(el("div", "empty", "No matching interfaces.")); return; }
  const cards = el("div", "cards");
  for (const iface of items) {
    const card = el("div", "card");
    const heading = el("h3");
    const name = button(iface.name, "linkish mono", () => openInspector(iface.id));
    heading.appendChild(name);
    heading.appendChild(el("span", "pill", iface.methods + " methods"));
    card.appendChild(heading);
    card.appendChild(el("div", "muted mono", iface.pkg_path));
    const impls = el("div", "relation");
    if (iface.implementations.length) {
      for (const impl of iface.implementations) {
        const label = impl.name + (impl.via_ptr ? " (ptr)" : "");
        impls.appendChild(button(label, "linkish", () => openInspector(impl.id)));
      }
    } else {
      impls.appendChild(el("span", "summary-empty", "No concrete implementations found."));
    }
    card.appendChild(impls);
    cards.appendChild(card);
  }
  root.appendChild(cards);
}

function renderTree() {
  const root = $("#tree-content");
  clear(root);
  const query = ($("#tree-search").value || "").toLowerCase();
  if (query) {
    const matches = [];
    walkTree(DATA.tree, (node) => {
      if ([node.label, node.id].join(" ").toLowerCase().includes(query)) matches.push(node);
    });
    if (!matches.length) { root.appendChild(el("div", "empty", "No hierarchy nodes match.")); return; }
    for (const node of matches.slice(0, 300)) {
      const row = el("div", "leaf");
      row.appendChild(button(node.label, "linkish mono", () => openInspector(node.id)));
      row.appendChild(document.createTextNode(" "));
      row.appendChild(el("span", "pill", node.kind));
      row.appendChild(stateBadge(node.sync_state));
      root.appendChild(row);
    }
    return;
  }
  for (const node of DATA.tree) root.appendChild(treeElement(node));
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
    row.appendChild(button(node.label, "linkish mono", () => openInspector(node.id)));
    row.appendChild(document.createTextNode(" "));
    row.appendChild(el("span", "pill", node.kind));
    row.appendChild(stateBadge(node.sync_state));
    return row;
  }
  const details = el("details");
  details.open = node.kind !== "file";
  const summary = el("summary");
  summary.appendChild(button(node.label, "linkish mono", (event) => { event.stopPropagation(); openInspector(node.id); }));
  summary.appendChild(document.createTextNode(" "));
  summary.appendChild(el("span", "pill", node.kind));
  summary.appendChild(stateBadge(node.sync_state));
  details.appendChild(summary);
  for (const child of node.children) details.appendChild(treeElement(child));
  return details;
}

const graphState = { mode: "modules", module: "", query: "", scale: 1, tx: 0, ty: 0, selected: null };

function setupGraph() {
  const controls = $("#graph-controls");
  clear(controls);
  const mode = el("select");
  mode.appendChild(new Option("Modules", "modules"));
  mode.appendChild(new Option("Packages", "packages"));
  mode.value = graphState.mode;
  mode.addEventListener("change", () => { graphState.mode = mode.value; updateGraphControls(); renderGraph(); });
  controls.appendChild(mode);

  const moduleSelect = el("select");
  moduleSelect.id = "graph-module-filter";
  moduleSelect.addEventListener("change", () => { graphState.module = moduleSelect.value; renderGraph(); });
  controls.appendChild(moduleSelect);

  const search = el("input");
  search.type = "search";
  search.placeholder = "Highlight nodes…";
  search.id = "graph-search";
  search.addEventListener("input", () => { graphState.query = search.value.toLowerCase(); renderGraph(); });
  controls.appendChild(search);
  controls.appendChild(button("Reset view", "ghost", () => {
    graphState.scale = 1; graphState.tx = 0; graphState.ty = 0; graphState.selected = null;
    graphState.query = ""; search.value = "";
    renderGraph();
  }));
  updateGraphControls();
}

function updateGraphControls() {
  const moduleSelect = $("#graph-module-filter");
  if (!moduleSelect) return;
  clear(moduleSelect);
  moduleSelect.appendChild(new Option("All modules", ""));
  for (const module of DATA.modules) moduleSelect.appendChild(new Option(module.name, module.name));
  moduleSelect.value = graphState.module;
}

function currentGraph() {
  const graph = DATA.graphs[graphState.mode];
  const nodes = graph.nodes.filter((node) => !graphState.module || node.module === graphState.module);
  const ids = new Set(nodes.map((node) => node.id));
  const edges = graph.edges.filter((edge) => ids.has(edge.from) && ids.has(edge.to));
  return { nodes, edges };
}

function renderGraph() {
  const svg = $("#graph-svg");
  clear(svg);
  const graph = currentGraph();
  if (!graph.nodes.length) {
    svg.appendChild(svgElement("text", { x: 24, y: 40, fill: "currentColor" })).textContent = "No nodes match the current filter.";
    return;
  }
  const nodeWidth = 170, nodeHeight = 36, columnGap = 230, rowGap = 64;
  const maxLayer = Math.max(...graph.nodes.map((node) => node.layer || 0));
  const maxOrder = Math.max(...graph.nodes.map((node) => node.order || 0));
  const width = 60 + maxLayer * columnGap + nodeWidth;
  const height = 50 + maxOrder * rowGap + nodeHeight;
  svg.setAttribute("viewBox", "0 0 " + width + " " + height);
  const viewport = svgElement("g", { transform: "translate(" + graphState.tx + "," + graphState.ty + ") scale(" + graphState.scale + ")" });
  svg.appendChild(viewport);
  const positions = new Map();
  for (const node of graph.nodes) {
    positions.set(node.id, { x: 40 + (node.layer || 0) * columnGap, y: 35 + (node.order || 0) * rowGap });
  }
  const edgeNodes = new Map();
  for (const edge of graph.edges) {
    const from = positions.get(edge.from), to = positions.get(edge.to);
    if (!from || !to) continue;
    const line = svgElement("line", {
      x1: from.x + nodeWidth, y1: from.y + nodeHeight / 2,
      x2: to.x, y2: to.y + nodeHeight / 2, class: "graph-edge",
    });
    line.dataset.from = edge.from; line.dataset.to = edge.to;
    viewport.appendChild(line);
    if (!edgeNodes.has(edge.from)) edgeNodes.set(edge.from, []);
    if (!edgeNodes.has(edge.to)) edgeNodes.set(edge.to, []);
    edgeNodes.get(edge.from).push(edge);
    edgeNodes.get(edge.to).push(edge);
  }
  for (const node of graph.nodes) {
    const pos = positions.get(node.id);
    const group = svgElement("g", { class: "graph-node", transform: "translate(" + pos.x + "," + pos.y + ")", "data-id": node.id });
    group.appendChild(svgElement("rect", { width: nodeWidth, height: nodeHeight, rx: 6 }));
    const label = node.label.length > 23 ? node.label.slice(0, 22) + "…" : node.label;
    const text = svgElement("text", { x: 8, y: 22 });
    text.textContent = label;
    group.appendChild(text);
    const title = svgElement("title");
    title.textContent = node.label + " · " + node.kind + " · " + node.sync_state;
    group.appendChild(title);
    group.addEventListener("click", (event) => {
      event.stopPropagation();
      graphState.selected = node.id;
      highlightGraph(svg, node.id, edgeNodes);
      openInspector(node.id);
    });
    viewport.appendChild(group);
  }
  applyGraphSearch(svg);
  applyGraphSelection(svg);
  svg.onwheel = (event) => {
    event.preventDefault();
    graphState.scale = Math.max(0.2, Math.min(3, graphState.scale * (event.deltaY < 0 ? 1.1 : 0.9)));
    viewport.setAttribute("transform", "translate(" + graphState.tx + "," + graphState.ty + ") scale(" + graphState.scale + ")");
  };
  let dragging = false, lastX = 0, lastY = 0;
  svg.onpointerdown = (event) => {
    if (event.target.closest && event.target.closest(".graph-node")) return;
    dragging = true; lastX = event.clientX; lastY = event.clientY;
    svg.classList.add("dragging"); svg.setPointerCapture(event.pointerId);
  };
  svg.onpointermove = (event) => {
    if (!dragging) return;
    graphState.tx += event.clientX - lastX; graphState.ty += event.clientY - lastY;
    lastX = event.clientX; lastY = event.clientY;
    viewport.setAttribute("transform", "translate(" + graphState.tx + "," + graphState.ty + ") scale(" + graphState.scale + ")");
  };
  svg.onpointerup = () => { dragging = false; svg.classList.remove("dragging"); };
}

function highlightGraph(svg, id, edgeNodes) {
  const adjacent = new Set([id]);
  for (const edge of edgeNodes.get(id) || []) { adjacent.add(edge.from); adjacent.add(edge.to); }
  for (const node of $$(".graph-node", svg)) node.classList.toggle("dim", !adjacent.has(node.dataset.id));
  for (const edge of $$(".graph-edge", svg)) {
    const active = edge.dataset.from === id || edge.dataset.to === id;
    edge.classList.toggle("active", active);
    edge.classList.toggle("dim", !active);
  }
  for (const node of $$(".graph-node", svg)) node.classList.toggle("selected", node.dataset.id === id);
}

function applyGraphSearch(svg) {
  for (const node of $$(".graph-node", svg)) {
    const hit = graphState.query && (node.dataset.id || "").toLowerCase().includes(graphState.query);
    node.classList.toggle("match", Boolean(hit));
  }
}

function applyGraphSelection(svg) {
  if (!graphState.selected) return;
  for (const node of $$(".graph-node", svg)) node.classList.toggle("selected", node.dataset.id === graphState.selected);
}

function globalSearch(query) {
  const results = $("#search-results");
  clear(results);
  query = (query || "").trim().toLowerCase();
  if (!query) { results.hidden = true; return; }
  const matches = DATA.symbols.filter((symbol) => [symbol.name, symbol.id, symbol.pkg_path, symbol.file, symbol.module].join(" ").toLowerCase().includes(query)).slice(0, 20);
  if (!matches.length) {
    results.hidden = false;
    results.appendChild(el("div", "empty", "No symbols found."));
    return;
  }
  results.hidden = false;
  for (const symbol of matches) {
    results.appendChild(button(symbol.name + " · " + symbol.kind + " · " + (symbol.pkg_path || symbol.module || ""), null, () => {
      results.hidden = true;
      $("#global-search").value = "";
      openInspector(symbol.id);
    }));
  }
}

function setupTabs() {
  for (const tab of $$(".tab")) {
    tab.addEventListener("click", () => {
      for (const other of $$(".tab")) other.classList.toggle("active", other === tab);
      for (const view of $$(".view")) view.classList.toggle("active", view.dataset.view === tab.dataset.tab);
      if (tab.dataset.tab === "graph") { setupGraphControls(); renderGraph(); }
      $("#search-results").hidden = true;
    });
  }
}

function setupGraphControls() {
  if (!$("#graph-controls").childNodes.length) setupGraph();
}

function setupTheme() {
  const saved = localStorage.getItem("go-mcp-theme");
  if (saved) document.documentElement.dataset.theme = saved;
  $("#theme-toggle").addEventListener("click", () => {
    const dark = document.documentElement.dataset.theme === "dark";
    document.documentElement.dataset.theme = dark ? "light" : "dark";
    localStorage.setItem("go-mcp-theme", document.documentElement.dataset.theme);
  });
}

function setupSearchShortcuts() {
  const input = $("#global-search");
  input.addEventListener("input", () => globalSearch(input.value));
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      const first = $("#search-results button");
      if (first) first.click();
    }
    if (event.key === "Escape") { input.value = ""; globalSearch(""); input.blur(); }
  });
  document.addEventListener("keydown", (event) => {
    const tag = (document.activeElement && document.activeElement.tagName || "").toLowerCase();
    if (event.key === "/" && tag !== "input" && tag !== "textarea") {
      event.preventDefault(); input.focus(); input.select();
    }
    if (event.key === "Escape") {
      closeInspector();
      $("#search-results").hidden = true;
    }
  });
}

function setupPanels() {
  $("#inspector-close").addEventListener("click", closeInspector);
  $("#interface-search").addEventListener("input", renderInterfaces);
  $("#tree-search").addEventListener("input", renderTree);
}

function init() {
  $("#repo-meta").textContent = DATA.repo + " · " + DATA.module_path + " · generated " + DATA.generated_at;
  setupTheme();
  setupTabs();
  setupSearchShortcuts();
  setupPanels();
  renderOverview();
  setupHotspots();
  renderInterfaces();
  renderTree();
  setupGraph();
}
init();
})();
