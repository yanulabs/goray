import type { Report } from "../types/report";

const DATA_SCRIPT_ID = "goray-data";

export interface ReportSource {
  report: Report;
  live: boolean;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function parseReport(value: unknown): Report | null {
  if (!isRecord(value)) {
    return null;
  }
  if (
    typeof value.repo !== "string" ||
    typeof value.module_path !== "string" ||
    typeof value.generated_at !== "string"
  ) {
    return null;
  }
  if (!isRecord(value.metrics) || !isRecord(value.coverage) || !isRecord(value.graphs)) {
    return null;
  }
  if (
    !Array.isArray(value.hotspots) ||
    !Array.isArray(value.modules) ||
    !Array.isArray(value.packages) ||
    !Array.isArray(value.symbols) ||
    !Array.isArray(value.interfaces) ||
    !Array.isArray(value.tree) ||
    !Array.isArray(value.module_cycles)
  ) {
    return null;
  }
  const graphs = value.graphs;
  if (!isRecord(graphs.modules) || !isRecord(graphs.packages)) {
    return null;
  }
  return value as unknown as Report;
}

export function readEmbeddedReport(): ReportSource | null {
  const element = document.getElementById(DATA_SCRIPT_ID);
  if (!(element instanceof HTMLScriptElement)) {
    return null;
  }
  const raw = (element.textContent ?? "").trim();
  if (raw === "") {
    return null;
  }
  try {
    const report = parseReport(JSON.parse(raw) as unknown);
    if (!report) {
      return null;
    }
    return { report, live: element.dataset.gorayLive === "true" };
  } catch {
    return null;
  }
}

export async function fetchLiveReport(signal?: AbortSignal): Promise<Report> {
  const response = await fetch("/api/report", {
    cache: "no-store",
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw new Error(`report endpoint returned HTTP ${response.status}`);
  }
  const report = parseReport((await response.json()) as unknown);
  if (!report) {
    throw new Error("report endpoint returned an invalid payload");
  }
  return report;
}
