import { issueColumnCategory } from "../issues/status-category";
import type { Issue, PlanningBucket, PlanningProgress } from "../types";

/** Display order of the progress bar segments, most finished first. */
export const PLANNING_BUCKET_ORDER: readonly PlanningBucket[] = [
  "done",
  "in_review",
  "in_progress",
  "blocked",
  "not_started",
  "cancelled",
];

/** Buckets that still need work; the rest are finished one way or another. */
export const PLANNING_OPEN_BUCKETS: readonly PlanningBucket[] = [
  "in_review",
  "in_progress",
  "blocked",
  "not_started",
];

/**
 * Client twin of the server's bucket rule: a built-in status key decides
 * first, a custom status falls back to its lifecycle category.
 */
export function planningBucketOf(
  issue: Pick<Issue, "status" | "status_category">,
): PlanningBucket {
  switch (issue.status) {
    case "done":
      return "done";
    case "cancelled":
      return "cancelled";
    case "in_review":
      return "in_review";
    case "blocked":
      return "blocked";
    case "in_progress":
      return "in_progress";
    case "todo":
    case "backlog":
      return "not_started";
  }
  switch (issueColumnCategory(issue)) {
    case "done":
      return "done";
    case "closed":
      return "cancelled";
    case "started":
      return "in_progress";
    default:
      return "not_started";
  }
}

export function isPlanningIssueOpen(
  issue: Pick<Issue, "status" | "status_category">,
): boolean {
  return PLANNING_OPEN_BUCKETS.includes(planningBucketOf(issue));
}

/**
 * The breakdown to draw for an epic or sprint. A server that predates the
 * breakdown still sends `issue_count`/`done_count`, where "done" also covers
 * cancelled work; that pair degrades to a two-segment bar instead of an empty
 * one.
 */
export function resolvePlanningProgress(entity: {
  issue_count: number;
  done_count: number;
  progress?: PlanningProgress;
}): PlanningProgress {
  const progress = entity.progress;
  if (progress && (progress.total > 0 || entity.issue_count === 0)) return progress;
  const done = Math.min(entity.done_count, entity.issue_count);
  return {
    total: entity.issue_count,
    done,
    in_progress: 0,
    in_review: 0,
    blocked: 0,
    not_started: entity.issue_count - done,
    cancelled: 0,
  };
}

/**
 * Share of the work that is done, as a whole percent. Cancelled issues are not
 * work that remains nor work that was delivered, so they leave the base.
 */
export function planningPercentDone(progress: PlanningProgress): number {
  const base = progress.total - progress.cancelled;
  if (base <= 0) return 0;
  return Math.round((progress.done / base) * 100);
}

export interface PlanningSegment {
  bucket: PlanningBucket;
  count: number;
  /** Width of the segment as a percentage of the whole bar. */
  percent: number;
}

/** Non-empty segments in display order; widths sum to 100. */
export function planningSegments(progress: PlanningProgress): PlanningSegment[] {
  if (progress.total <= 0) return [];
  return PLANNING_BUCKET_ORDER.filter((bucket) => progress[bucket] > 0).map((bucket) => ({
    bucket,
    count: progress[bucket],
    percent: (progress[bucket] / progress.total) * 100,
  }));
}
