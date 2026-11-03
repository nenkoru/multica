import type { TvEpic, TvLlmCacheHour } from "@multica/core/tv";

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

const HOUR_MS = 3_600_000;

/** Tone of a cache hit rate against its goal: at it, within 3 points, or further. */
export function cacheTone(rate: number | null, target: number): TvTone {
  if (rate === null) return "neutral";
  if (rate >= target) return "good";
  if (rate >= target - 3) return "warning";
  return "serious";
}

export interface TvCacheSlot {
  at: Date;
  hour: TvLlmCacheHour | null;
}

/** The last `count` whole hours up to the current one, each with its numbers or null when idle. */
export function cacheSlots(hourly: TvLlmCacheHour[], now: Date, count = 24): TvCacheSlot[] {
  const end = Math.floor(now.getTime() / HOUR_MS) * HOUR_MS;
  const byHour = new Map(hourly.map((h) => [Math.floor(Date.parse(h.at) / HOUR_MS) * HOUR_MS, h]));
  return Array.from({ length: count }, (_, n) => {
    const at = end - (count - 1 - n) * HOUR_MS;
    return { at: new Date(at), hour: byHour.get(at) ?? null };
  });
}

/** Bar height in % on the band from `floor` to 100 %, where a working cache lives; never invisible. */
export function cacheBar(rate: number | null, floor = 90): number {
  if (rate === null) return 0;
  return Math.max(4, Math.min(100, ((rate - floor) / (100 - floor)) * 100));
}
