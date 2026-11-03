import type { PlanningProgress } from "./planning";

export type EpicStatus = "open" | "closed";

export interface Epic {
  id: string;
  workspace_id: string;
  title: string;
  description: string | null;
  color: string;
  status: EpicStatus;
  issue_count: number;
  done_count: number;
  /** Per-status breakdown; absent from servers that predate it. */
  progress?: PlanningProgress;
  created_at: string;
  updated_at: string;
}
