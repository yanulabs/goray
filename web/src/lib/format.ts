import type { ReportSymbol, SyncState } from "../types/report";

const integerFormat = new Intl.NumberFormat("en-US");

export function formatInt(value: number): string {
  return integerFormat.format(Math.round(value));
}

export function formatCompact(value: number): string {
  const abs = Math.abs(value);
  if (abs >= 1_000_000) {
    return `${trim(value / 1_000_000)}m`;
  }
  if (abs >= 10_000) {
    return `${trim(value / 1_000)}k`;
  }
  return formatInt(value);
}

function trim(value: number): string {
  const fixed = value >= 100 ? value.toFixed(0) : value.toFixed(1);
  return fixed.replace(/\.0$/, "");
}

export function formatPercent(value: number): string {
  const fixed = value >= 100 ? value.toFixed(0) : value.toFixed(1);
  return `${fixed.replace(/\.0$/, "")}%`;
}

export function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

export function basename(path: string): string {
  const parts = path.split("/");
  return parts[parts.length - 1] ?? path;
}

export function truncateMiddle(value: string, max: number): string {
  if (value.length <= max) {
    return value;
  }
  const head = Math.ceil((max - 1) / 2);
  const tail = Math.floor((max - 1) / 2);
  return `${value.slice(0, head)}…${value.slice(value.length - tail)}`;
}

export function fileLine(file: string, line: number | undefined): string {
  if (!file) {
    return "";
  }
  return line && line > 0 ? `${file}:${line}` : file;
}

export function riskScore(symbol: ReportSymbol): number {
  return symbol.complexity * 2 + symbol.fan_in;
}

export type Tone = "zinc" | "red" | "amber" | "emerald" | "indigo" | "blue";

export function syncTone(state: SyncState): Tone {
  switch (state) {
    case "synced":
      return "emerald";
    case "soft_stale":
    case "hard_stale":
      return "amber";
    case "missing":
      return "zinc";
    case "not_eligible":
      return "zinc";
    default:
      return "zinc";
  }
}

export function syncLabel(state: SyncState): string {
  switch (state) {
    case "synced":
      return "Synced summary";
    case "soft_stale":
      return "Stale summary; dependencies changed";
    case "hard_stale":
      return "Stale summary; symbol changed";
    case "not_eligible":
      return "Summary not eligible";
    default:
      return "No cached summary";
  }
}

export function dotClass(tone: Tone): string {
  switch (tone) {
    case "red":
      return "bg-red-500";
    case "amber":
      return "bg-amber-500";
    case "emerald":
      return "bg-emerald-500";
    case "indigo":
      return "bg-indigo-500";
    case "blue":
      return "bg-blue-500";
    default:
      return "bg-zinc-400 dark:bg-zinc-600";
  }
}

export function kindLabel(kind: string): string {
  switch (kind) {
    case "interface":
      return "iface";
    case "package":
      return "pkg";
    case "module":
      return "mod";
    case "struct":
      return "struct";
    case "type":
      return "type";
    case "func":
      return "func";
    case "method":
      return "method";
    case "file":
      return "file";
    default:
      return kind.slice(0, 6);
  }
}

export async function copyText(value: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(value);
      return true;
    }
  } catch {
    // Fall through to the legacy path.
  }
  try {
    const area = document.createElement("textarea");
    area.value = value;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.select();
    const copied = document.execCommand("copy");
    document.body.removeChild(area);
    return copied;
  } catch {
    return false;
  }
}

export function shortSymbolName(id: string): string {
  const value = id.startsWith("sym:") ? id.slice(4) : id;
  const dot = value.lastIndexOf(".");
  return dot >= 0 ? value.slice(dot + 1) : value;
}
