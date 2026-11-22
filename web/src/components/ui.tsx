import type { ReactNode } from "react";

import { dotClass, type Tone } from "../lib/format";

const badgeTones: Record<Tone, string> = {
  zinc: "border-zinc-300 bg-zinc-100 text-zinc-600 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-400",
  red: "border-red-300 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950 dark:text-red-300",
  amber: "border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300",
  emerald:
    "border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-300",
  indigo:
    "border-indigo-300 bg-indigo-50 text-indigo-700 dark:border-indigo-900 dark:bg-indigo-950 dark:text-indigo-300",
  blue: "border-blue-300 bg-blue-50 text-blue-700 dark:border-blue-900 dark:bg-blue-950 dark:text-blue-300",
};

export function Badge({
  tone = "zinc",
  title,
  className = "",
  children,
}: {
  tone?: Tone;
  title?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <span
      title={title}
      className={`inline-flex shrink-0 items-center gap-1 rounded border px-1.5 py-px font-mono text-[10px] leading-4 ${badgeTones[tone]} ${className}`}
    >
      {children}
    </span>
  );
}

export function KindBadge({ kind }: { kind: string }) {
  const label =
    kind === "interface"
      ? "iface"
      : kind === "package"
        ? "pkg"
        : kind === "module"
          ? "mod"
          : kind.slice(0, 6);
  return (
    <span className="inline-flex h-4 w-[42px] shrink-0 items-center justify-center rounded border border-zinc-200 bg-zinc-100 font-mono text-[9px] uppercase leading-none tracking-wide text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
      {label}
    </span>
  );
}

export function SyncDot({ state, label }: { state: string; label: string }) {
  const tone: Tone =
    state === "synced"
      ? "emerald"
      : state === "soft_stale" || state === "hard_stale"
        ? "amber"
        : "zinc";
  return (
    <span
      role="img"
      aria-label={label}
      title={label}
      className={`inline-block h-1.5 w-1.5 shrink-0 rounded-full ${dotClass(tone)}`}
    />
  );
}

export function IconButton({
  label,
  onClick,
  active = false,
  disabled = false,
  children,
}: {
  label: string;
  onClick: () => void;
  active?: boolean;
  disabled?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      aria-pressed={active}
      disabled={disabled}
      onClick={onClick}
      className={`inline-flex h-7 w-7 items-center justify-center rounded border text-zinc-600 transition-colors disabled:cursor-not-allowed disabled:opacity-40 dark:text-zinc-400 ${
        active
          ? "border-indigo-400 bg-indigo-50 text-indigo-700 dark:border-indigo-700 dark:bg-indigo-950 dark:text-indigo-300"
          : "border-zinc-200 bg-white hover:border-zinc-300 hover:bg-zinc-100 dark:border-zinc-800 dark:bg-zinc-950 dark:hover:border-zinc-700 dark:hover:bg-zinc-900"
      }`}
    >
      {children}
    </button>
  );
}

export interface SegmentOption<T extends string> {
  value: T;
  label: string;
  icon?: ReactNode;
  title?: string;
}

export function Segmented<T extends string>({
  options,
  value,
  onChange,
  compact = false,
}: {
  options: Array<SegmentOption<T>>;
  value: T;
  onChange: (value: T) => void;
  compact?: boolean;
}) {
  return (
    <div className="inline-flex items-center rounded border border-zinc-200 bg-zinc-100 p-px dark:border-zinc-800 dark:bg-zinc-900">
      {options.map((option) => {
        const selected = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            title={option.title ?? option.label}
            aria-pressed={selected}
            onClick={() => onChange(option.value)}
            className={`inline-flex items-center gap-1 rounded-sm leading-none transition-colors ${
              compact ? "h-5 px-1.5 text-[10px]" : "h-6 px-2 text-[11px]"
            } ${
              selected
                ? "bg-white font-medium text-zinc-900 dark:bg-zinc-700 dark:text-zinc-50"
                : "text-zinc-500 hover:text-zinc-800 dark:text-zinc-400 dark:hover:text-zinc-200"
            }`}
          >
            {option.icon}
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

export function EmptyState({
  title,
  detail,
  children,
}: {
  title: string;
  detail?: string;
  children?: ReactNode;
}) {
  return (
    <div className="flex h-full min-h-48 flex-col items-center justify-center gap-2 p-8 text-center">
      <div className="font-mono text-[11px] uppercase tracking-widest text-zinc-400 dark:text-zinc-500">
        {title}
      </div>
      {detail ? (
        <p className="max-w-md text-xs leading-5 text-zinc-500 dark:text-zinc-400">{detail}</p>
      ) : null}
      {children}
    </div>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="inline-flex h-4 min-w-4 items-center justify-center rounded border border-zinc-300 bg-white px-1 font-mono text-[9px] leading-none text-zinc-500 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-400">
      {children}
    </kbd>
  );
}
