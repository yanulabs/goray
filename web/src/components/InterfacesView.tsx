import { Search, X } from "lucide-react";
import { useMemo, useState } from "react";

import type { EntityIndex } from "../lib/derive";
import { formatInt, syncLabel, truncateMiddle } from "../lib/format";
import type { Report, ReportImpl, ReportInterface } from "../types/report";
import { Badge, EmptyState, Segmented, SyncDot } from "./ui";

type ImplFilter = "all" | "zero" | "single" | "multi";

function ImplChip({
  implementation,
  onSelect,
}: {
  implementation: ReportImpl;
  onSelect: (id: string) => void;
}) {
  return (
    <button
      type="button"
      onClick={(event) => {
        event.stopPropagation();
        onSelect(implementation.id);
      }}
      title={`${implementation.pkg_path}.${implementation.name}${implementation.via_ptr ? " (pointer receiver)" : ""}`}
      className="inline-flex h-5 max-w-full items-center gap-1 rounded border border-zinc-200 bg-zinc-50 px-1.5 font-mono text-[10px] text-zinc-600 hover:border-indigo-300 hover:bg-indigo-50 hover:text-indigo-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400 dark:hover:border-indigo-800 dark:hover:bg-indigo-950 dark:hover:text-indigo-300"
    >
      {implementation.via_ptr ? <span className="text-amber-600 dark:text-amber-400">*</span> : null}
      <span className="truncate">{implementation.name}</span>
    </button>
  );
}

function InterfaceRow({
  iface,
  index,
  onSelect,
}: {
  iface: ReportInterface;
  index: EntityIndex;
  onSelect: (id: string) => void;
}) {
  const implementations = iface.implementations;
  const zero = implementations.length === 0;
  return (
    <tr
      tabIndex={0}
      onClick={() => onSelect(iface.id)}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onSelect(iface.id);
        }
      }}
      className="cursor-pointer border-b border-zinc-100 align-top outline-none hover:bg-zinc-50 focus-visible:bg-indigo-50 dark:border-zinc-900 dark:hover:bg-zinc-900 dark:focus-visible:bg-indigo-950"
    >
      <td className="w-[300px] px-2 py-1.5">
        <div className="flex min-w-0 items-center gap-1.5">
          <SyncDot
            state={index.entitiesById.get(iface.id)?.syncState ?? "not_eligible"}
            label={syncLabel(index.entitiesById.get(iface.id)?.syncState ?? "not_eligible")}
          />
          <span className="truncate font-mono text-[11px] font-medium text-zinc-800 dark:text-zinc-100">
            {iface.name}
          </span>
          <Badge>{iface.methods} meth</Badge>
        </div>
        <div
          className="mt-0.5 truncate font-mono text-[9px] text-zinc-400 dark:text-zinc-500"
          title={iface.pkg_path}
        >
          {truncateMiddle(iface.pkg_path, 52)}
        </div>
      </td>
      <td className="w-[110px] px-2 py-1.5 font-mono text-[10px] text-zinc-500 dark:text-zinc-400">
        {iface.module || "—"}
      </td>
      <td className="px-2 py-1.5">
        <div className="flex flex-wrap items-center gap-1">
          {zero ? (
            <Badge tone="amber" title="No go/types-verified implementations">
              0 impls
            </Badge>
          ) : (
            implementations.map((implementation) => (
              <ImplChip
                key={`${iface.id}:${implementation.id}`}
                implementation={implementation}
                onSelect={onSelect}
              />
            ))
          )}
        </div>
      </td>
    </tr>
  );
}

export function InterfacesView({
  report,
  index,
  onSelect,
}: {
  report: Report;
  index: EntityIndex;
  onSelect: (id: string) => void;
}) {
  const [search, setSearch] = useState("");
  const [moduleFilter, setModuleFilter] = useState("");
  const [implFilter, setImplFilter] = useState<ImplFilter>("all");

  const rows = useMemo(() => {
    const query = search.trim().toLowerCase();
    return report.interfaces.filter((iface) => {
      if (moduleFilter !== "" && iface.module !== moduleFilter) {
        return false;
      }
      const count = iface.implementations.length;
      if (implFilter === "zero" && count !== 0) {
        return false;
      }
      if (implFilter === "single" && count !== 1) {
        return false;
      }
      if (implFilter === "multi" && count < 2) {
        return false;
      }
      if (query !== "") {
        const names = iface.implementations.map((implementation) => implementation.name).join(" ");
        const haystack = `${iface.name} ${iface.pkg_path} ${iface.module} ${names}`.toLowerCase();
        if (!haystack.includes(query)) {
          return false;
        }
      }
      return true;
    });
  }, [report.interfaces, search, moduleFilter, implFilter]);

  const modules = useMemo(
    () => [...new Set(report.interfaces.map((iface) => iface.module))].filter((name) => name !== "").sort((a, b) => a.localeCompare(b)),
    [report.interfaces],
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-zinc-200 px-2 py-1.5 dark:border-zinc-800">
        <div className="relative">
          <Search
            size={12}
            className="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-zinc-400"
          />
          <input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Filter interfaces and implementers…"
            className="h-6 w-64 rounded border border-zinc-200 bg-white pl-6 pr-6 font-mono text-[11px] text-zinc-800 placeholder:text-zinc-400 focus:border-indigo-500 focus:outline-none dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-200 dark:placeholder:text-zinc-600"
          />
          {search !== "" ? (
            <button
              type="button"
              onClick={() => setSearch("")}
              className="absolute right-1 top-1/2 -translate-y-1/2 rounded p-0.5 text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-200"
              aria-label="Clear filter"
            >
              <X size={11} />
            </button>
          ) : null}
        </div>

        <select
          value={moduleFilter}
          onChange={(event) => setModuleFilter(event.target.value)}
          aria-label="Filter by module"
          className="h-6 rounded border border-zinc-200 bg-white px-1.5 font-mono text-[10px] text-zinc-600 focus:border-indigo-500 focus:outline-none dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-300"
        >
          <option value="">all modules</option>
          {modules.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>

        <Segmented<ImplFilter>
          compact
          value={implFilter}
          onChange={setImplFilter}
          options={[
            { value: "all", label: "all" },
            { value: "zero", label: "0 impl" },
            { value: "single", label: "1 impl" },
            { value: "multi", label: "2+" },
          ]}
        />

        <div className="ml-auto font-mono text-[10px] tabular-nums text-zinc-400 dark:text-zinc-500">
          {formatInt(rows.length)} / {formatInt(report.interfaces.length)} interfaces
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-auto">
        {rows.length === 0 ? (
          <EmptyState
            title="No matching interfaces"
            detail="Relax the implementation filter or clear the search."
          />
        ) : (
          <table className="w-full min-w-[720px] border-collapse text-left">
            <thead>
              <tr>
                <th className="sticky top-0 z-10 h-7 border-b border-zinc-200 bg-zinc-50 px-2 text-left font-mono text-[9px] font-medium uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400">
                  Interface
                </th>
                <th className="sticky top-0 z-10 h-7 w-[110px] border-b border-zinc-200 bg-zinc-50 px-2 text-left font-mono text-[9px] font-medium uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400">
                  Module
                </th>
                <th className="sticky top-0 z-10 h-7 border-b border-zinc-200 bg-zinc-50 px-2 text-left font-mono text-[9px] font-medium uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400">
                  Implementations
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((iface) => (
                <InterfaceRow key={iface.id} iface={iface} index={index} onSelect={onSelect} />
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
