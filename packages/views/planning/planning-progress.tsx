"use client";

import type { PlanningBucket, PlanningProgress } from "@multica/core/types";
import {
  PLANNING_BUCKET_ORDER,
  planningPercentDone,
  planningSegments,
} from "@multica/core/planning";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";

export const PLANNING_BUCKET_COLOR: Record<PlanningBucket, string> = {
  done: "bg-success",
  in_review: "bg-info",
  in_progress: "bg-warning",
  blocked: "bg-destructive",
  not_started: "bg-muted-foreground/35",
  cancelled: "bg-muted-foreground/15",
};

/** Segmented bar: one segment per non-empty status bucket. */
export function PlanningProgressBar({
  progress,
  className,
}: {
  progress: PlanningProgress;
  className?: string;
}) {
  const { t } = useT("planning");
  const segments = planningSegments(progress);
  const breakdown = segments
    .map((segment) => `${t(($) => $.progress.bucket[segment.bucket])}: ${segment.count}`)
    .join(", ");
  return (
    <div
      role="img"
      aria-label={breakdown ? `${t(($) => $.progress.bar_label)}. ${breakdown}` : t(($) => $.progress.bar_label)}
      className={cn("flex h-2 w-full overflow-hidden rounded-full bg-muted", className)}
    >
      {segments.map((segment) => (
        <div
          key={segment.bucket}
          data-bucket={segment.bucket}
          className={cn("h-full", PLANNING_BUCKET_COLOR[segment.bucket])}
          style={{ width: `${segment.percent}%` }}
        />
      ))}
    </div>
  );
}

/** Bar plus the percent and a per-bucket legend with exact counts. */
export function PlanningProgressSummary({ progress }: { progress: PlanningProgress }) {
  const { t } = useT("planning");
  // The five working buckets are always listed so the legend does not jump as
  // issues move; cancelled only appears once something is cancelled.
  const legend = PLANNING_BUCKET_ORDER.filter(
    (bucket) => bucket !== "cancelled" || progress.cancelled > 0,
  );
  return (
    <section aria-label={t(($) => $.progress.title)} className="space-y-2">
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="text-label font-medium">{t(($) => $.progress.title)}</h2>
        <div className="flex items-baseline gap-2 text-caption text-muted-foreground tabular-nums">
          <span className="text-body font-medium text-foreground">
            {t(($) => $.progress.percent_done, { percent: planningPercentDone(progress) })}
          </span>
          <span>{t(($) => $.progress.done_of_total, { done: progress.done, total: progress.total })}</span>
        </div>
      </div>
      <PlanningProgressBar progress={progress} className="h-2.5" />
      <ul className="flex flex-wrap gap-x-4 gap-y-1 text-caption text-muted-foreground">
        {legend.map((bucket) => (
          <li key={bucket} className="flex items-center gap-1.5">
            <span className={cn("size-2 rounded-full", PLANNING_BUCKET_COLOR[bucket])} />
            <span>{t(($) => $.progress.bucket[bucket])}</span>
            <span className="tabular-nums text-foreground">{progress[bucket]}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Compact bar with counts for a row of the epics or sprints list. */
export function PlanningListProgress({ progress }: { progress: PlanningProgress }) {
  const { t } = useT("planning");
  if (progress.total === 0) {
    return (
      <span className="w-32 shrink-0 text-caption text-muted-foreground sm:w-52">
        {t(($) => $.list.no_issues)}
      </span>
    );
  }
  return (
    <span className="flex w-32 shrink-0 items-center gap-2 sm:w-52">
      <PlanningProgressBar progress={progress} className="h-1.5" />
      <span className="shrink-0 text-caption text-muted-foreground tabular-nums">
        {t(($) => $.progress.done_of_total, { done: progress.done, total: progress.total })}
        <span className="hidden sm:inline"> · {planningPercentDone(progress)}%</span>
      </span>
    </span>
  );
}
