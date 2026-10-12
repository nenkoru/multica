const DAY_MS = 86_400_000;

function utcDay(date: string): number | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
  if (!match) return null;
  return Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
}

export type SprintTimeline =
  | { phase: "upcoming"; totalDays: number; startsInDays: number }
  | { phase: "running"; totalDays: number; day: number; daysLeft: number }
  | { phase: "over"; totalDays: number; daysOver: number };

/**
 * Where `today` falls in a sprint. Both ends are inclusive calendar days, so a
 * sprint from the 5th to the 12th has 8 days and its last day has 0 left.
 * Returns null for malformed dates.
 */
export function sprintTimeline(
  startDate: string,
  endDate: string,
  today: string,
): SprintTimeline | null {
  const start = utcDay(startDate);
  const end = utcDay(endDate);
  const now = utcDay(today);
  if (start === null || end === null || now === null || end < start) return null;
  const totalDays = Math.round((end - start) / DAY_MS) + 1;
  if (now < start) {
    return { phase: "upcoming", totalDays, startsInDays: Math.round((start - now) / DAY_MS) };
  }
  if (now > end) {
    return { phase: "over", totalDays, daysOver: Math.round((now - end) / DAY_MS) };
  }
  return {
    phase: "running",
    totalDays,
    day: Math.round((now - start) / DAY_MS) + 1,
    daysLeft: Math.round((end - now) / DAY_MS),
  };
}
