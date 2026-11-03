"use client";

import type { PlanningBurndown } from "@multica/core/types";
import { formatDateOnly } from "@multica/core/issues/date";
import { useLocale, useT } from "../i18n";

// Drawn at about its natural size (the caller caps the width), so the axis
// labels stay the size of the surrounding caption text.
const WIDTH = 560;
const HEIGHT = 160;
const PAD = { top: 10, right: 12, bottom: 22, left: 30 };

const DAY_MS = 86_400_000;

function dayIndex(date: string, start: string): number {
  return Math.round((Date.parse(`${date}T00:00:00Z`) - Date.parse(`${start}T00:00:00Z`)) / DAY_MS);
}

/**
 * Remaining issues per day against the straight "ideal" line. Drawn only from
 * data the server sent: the caller hides the chart when there is none.
 */
export function BurndownChart({ burndown }: { burndown: PlanningBurndown }) {
  const { t } = useT("planning");
  const locale = useLocale();
  const lastDay = Math.max(1, dayIndex(burndown.end_date, burndown.start_date));
  const top = Math.max(1, burndown.total);
  const plotWidth = WIDTH - PAD.left - PAD.right;
  const plotHeight = HEIGHT - PAD.top - PAD.bottom;
  const x = (day: number) => PAD.left + (Math.min(day, lastDay) / lastDay) * plotWidth;
  const y = (remaining: number) => PAD.top + (1 - Math.max(0, remaining) / top) * plotHeight;

  const points = burndown.days.map((day) => ({
    x: x(dayIndex(day.date, burndown.start_date)),
    y: y(day.remaining),
  }));
  const last = burndown.days[burndown.days.length - 1];
  const lastPoint = points[points.length - 1];
  if (!last || !lastPoint) return null;

  const format = (date: string) => formatDateOnly(date, { month: "short", day: "numeric" }, locale);
  return (
    <figure className="space-y-1.5">
      <figcaption className="flex items-center justify-between text-caption text-muted-foreground">
        <span className="font-medium text-foreground">{t(($) => $.sprint.burndown)}</span>
        <span className="flex items-center gap-3">
          <span className="flex items-center gap-1">
            <span className="h-0.5 w-3 rounded bg-info" />
            {t(($) => $.sprint.burndown_remaining)}
          </span>
          <span className="flex items-center gap-1">
            <span className="h-0 w-3 border-t border-dashed border-muted-foreground" />
            {t(($) => $.sprint.burndown_ideal)}
          </span>
        </span>
      </figcaption>
      <svg
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        role="img"
        aria-label={t(($) => $.sprint.burndown_label, {
          remaining: last.remaining,
          total: burndown.total,
          date: format(last.date),
        })}
        className="w-full text-info"
      >
        <line x1={PAD.left} y1={y(0)} x2={WIDTH - PAD.right} y2={y(0)} className="stroke-border" strokeWidth="1" />
        <line x1={PAD.left} y1={PAD.top} x2={PAD.left} y2={y(0)} className="stroke-border" strokeWidth="1" />
        <line
          x1={x(0)}
          y1={y(burndown.total)}
          x2={x(lastDay)}
          y2={y(0)}
          className="stroke-muted-foreground"
          strokeWidth="1"
          strokeDasharray="3 3"
        />
        {points.length > 1 ? (
          <polyline
            points={points.map((point) => `${point.x},${point.y}`).join(" ")}
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        ) : null}
        <circle cx={lastPoint.x} cy={lastPoint.y} r="3" fill="currentColor" />
        <text x={PAD.left - 6} y={y(burndown.total) + 4} textAnchor="end" className="fill-muted-foreground text-[11px] tabular-nums">
          {burndown.total}
        </text>
        <text x={PAD.left - 6} y={y(0) + 4} textAnchor="end" className="fill-muted-foreground text-[11px] tabular-nums">
          0
        </text>
        <text x={PAD.left} y={HEIGHT - 6} textAnchor="start" className="fill-muted-foreground text-[11px]">
          {format(burndown.start_date)}
        </text>
        <text x={WIDTH - PAD.right} y={HEIGHT - 6} textAnchor="end" className="fill-muted-foreground text-[11px]">
          {format(burndown.end_date)}
        </text>
      </svg>
    </figure>
  );
}
