import type { PlanningProgress } from "./planning";

export type SprintStatus = "planned" | "active" | "completed" | "cancelled";

export interface Sprint {
  id: string;
  workspace_id: string;
  name: string;
  goal: string | null;
  start_date: string;
  end_date: string;
  status: SprintStatus;
  issue_count: number;
  done_count: number;
  /** Per-status breakdown; absent from servers that predate it. */
  progress?: PlanningProgress;
  created_at: string;
  updated_at: string;
}
