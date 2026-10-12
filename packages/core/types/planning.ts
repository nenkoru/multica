import type { Sprint, SprintStatus } from "./sprint";
import type { EpicStatus } from "./epic";

/** Server-owned status breakdown of an epic's or sprint's issues. */
export interface PlanningProgress {
  total: number;
  done: number;
  in_progress: number;
  in_review: number;
  blocked: number;
  not_started: number;
  cancelled: number;
}

export type PlanningBucket = Exclude<keyof PlanningProgress, "total">;

export interface PlanningAssigneeBreakdown {
  /** Null together with `assignee_id` for unassigned issues. */
  assignee_type: string | null;
  assignee_id: string | null;
  total: number;
  done: number;
  open: number;
}

export interface PlanningSprintBreakdown {
  /** Null for the epic's issues that sit in no sprint. */
  sprint_id: string | null;
  name: string;
  status: SprintStatus;
  start_date: string;
  end_date: string;
  total: number;
  done: number;
}

export interface PlanningEpicBreakdown {
  /** Null for the sprint's issues that belong to no epic. */
  epic_id: string | null;
  title: string;
  color: string;
  status: EpicStatus;
  total: number;
  done: number;
}

export interface PlanningAttentionIssue {
  id: string;
  identifier: string;
  title: string;
  status: string;
  bucket: "blocked" | "in_review";
  assignee_type: string | null;
  assignee_id: string | null;
  updated_at: string;
}

export interface PlanningActivityEntry {
  id: string;
  issue_id: string;
  issue_identifier: string;
  issue_title: string;
  actor_type: string | null;
  actor_id: string | null;
  action: string;
  details: Record<string, unknown>;
  created_at: string;
}

export interface PlanningBurndown {
  total: number;
  start_date: string;
  end_date: string;
  days: { date: string; remaining: number }[];
}

export interface EpicOverview {
  epic_id: string;
  progress: PlanningProgress;
  last_activity_at: string | null;
  assignees: PlanningAssigneeBreakdown[];
  sprints: PlanningSprintBreakdown[];
  attention: PlanningAttentionIssue[];
  activity: PlanningActivityEntry[];
}

export interface SprintOverview {
  sprint_id: string;
  progress: PlanningProgress;
  last_activity_at: string | null;
  assignees: PlanningAssigneeBreakdown[];
  epics: PlanningEpicBreakdown[];
  attention: PlanningAttentionIssue[];
  /** Null when there is nothing to chart: no issues, or not started yet. */
  burndown: PlanningBurndown | null;
}

/** What happens to a sprint's unfinished issues when it is completed. */
export type SprintOpenIssuesMode = "keep" | "move" | "unlink";

export interface CompleteSprintRequest {
  open_issues: SprintOpenIssuesMode;
  /** Only with `move`; omitted means the next planned sprint. */
  target_sprint_id?: string;
}

export interface CompleteSprintResult {
  sprint: Sprint;
  open_issues: SprintOpenIssuesMode;
  target_sprint: Sprint | null;
  moved_count: number;
  unlinked_count: number;
  kept_count: number;
}
