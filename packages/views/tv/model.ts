import type { TvEpic } from "@multica/core/tv";

// Every time on the TV screen is Moscow time, whatever the TV's own clock
// zone is set to.
export const TV_TIME_ZONE = "Europe/Moscow";

export const TV_TITLE_LIMIT = 60;

const dateFormatter = new Intl.DateTimeFormat("en-CA", {
  timeZone: TV_TIME_ZONE,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
});

const timeFormatter = new Intl.DateTimeFormat("ru-RU", {
  timeZone: TV_TIME_ZONE,
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});

/** YYYY-MM-DD of `at` in Moscow. */
export function tvDate(at: Date): string {
  return dateFormatter.format(at);
}

/** HH:MM of `at` in Moscow; empty for an unparsable timestamp. */
export function tvTime(at: Date | string): string {
  const date = typeof at === "string" ? new Date(at) : at;
  return Number.isNaN(date.getTime()) ? "" : timeFormatter.format(date);
}

/** Milliseconds from an ISO timestamp to now; 0 when unknown or in the future. */
export function elapsedMs(iso: string, now: Date): number {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? 0 : Math.max(0, now.getTime() - t);
}

export function truncateTitle(title: string, limit = TV_TITLE_LIMIT): string {
  const chars = Array.from(title.trim());
  return chars.length <= limit ? chars.join("") : `${chars.slice(0, limit - 1).join("").trimEnd()}…`;
}

export type TvTone = "neutral" | "good" | "warning" | "serious" | "critical";

export type TvSegmentKey = "in_progress" | "in_review" | "blocked" | "done";

export interface TvSegment {
  key: TvSegmentKey;
  count: number;
  tone: TvTone;
}

/** The composition bar of an epic row: work in flight, then done. */
export function epicSegments(epic: TvEpic): TvSegment[] {
  return [
    { key: "in_progress" as const, count: epic.in_progress, tone: "neutral" as const },
    { key: "in_review" as const, count: epic.in_review, tone: "warning" as const },
    { key: "blocked" as const, count: epic.blocked, tone: "serious" as const },
    { key: "done" as const, count: epic.done_in_window, tone: "good" as const },
  ];
}

export function percent(part: number, total: number): number {
  return total > 0 ? Math.round((part / total) * 100) : 0;
}
