import { z } from "zod";
import type {
  CompleteSprintResult,
  Epic,
  EpicOverview,
  PlanningProgress,
  Sprint,
  SprintOverview,
} from "../types";

export const EMPTY_PLANNING_PROGRESS: PlanningProgress = {
  total: 0, done: 0, in_progress: 0, in_review: 0, blocked: 0, not_started: 0, cancelled: 0,
};
const count = z.number().catch(0).default(0);
// An older server sends no breakdown at all; the bar then falls back to the
// issue_count/done_count pair every server version carries.
export const PlanningProgressSchema = z.object({
  total: count, done: count, in_progress: count, in_review: count,
  blocked: count, not_started: count, cancelled: count,
}).catch(EMPTY_PLANNING_PROGRESS).default(EMPTY_PLANNING_PROGRESS);

const planningFields = {
  id: z.string(), workspace_id: z.string(),
  issue_count: z.number().default(0), done_count: z.number().default(0),
  progress: PlanningProgressSchema,
  created_at: z.string(), updated_at: z.string(),
};
const epicStatus = z.enum(["open", "closed"]).catch("open");
const sprintStatus = z.enum(["planned", "active", "completed", "cancelled"]).catch("planned");
export const EpicSchema = z.object({
  ...planningFields, title: z.string(), description: z.string().nullable().default(null),
  color: z.string().default("#6366f1"), status: epicStatus,
});
export const SprintSchema = z.object({
  ...planningFields, name: z.string(), goal: z.string().nullable().default(null),
  start_date: z.string(), end_date: z.string(),
  status: sprintStatus,
});
export const EpicListSchema = z.object({ epics: z.array(EpicSchema), total: z.number() });
export const SprintListSchema = z.object({ sprints: z.array(SprintSchema), total: z.number() });
export const EMPTY_EPIC: Epic = {
  id: "", workspace_id: "", title: "", description: null, color: "#6366f1", status: "open",
  issue_count: 0, done_count: 0, progress: EMPTY_PLANNING_PROGRESS, created_at: "", updated_at: "",
};
export const EMPTY_SPRINT: Sprint = {
  id: "", workspace_id: "", name: "", goal: null, start_date: "", end_date: "", status: "planned",
  issue_count: 0, done_count: 0, progress: EMPTY_PLANNING_PROGRESS, created_at: "", updated_at: "",
};

const nullableString = z.string().nullable().default(null);
const assignees = z.array(z.object({
  assignee_type: nullableString, assignee_id: nullableString,
  total: count, done: count, open: count,
})).catch([]).default([]);
const attention = z.array(z.object({
  id: z.string(), identifier: z.string().default(""), title: z.string().default(""),
  status: z.string().default(""), bucket: z.enum(["blocked", "in_review"]).catch("blocked"),
  assignee_type: nullableString, assignee_id: nullableString, updated_at: z.string().default(""),
})).catch([]).default([]);
const overviewFields = {
  progress: PlanningProgressSchema,
  last_activity_at: nullableString,
  assignees,
  attention,
};

export const EpicOverviewSchema = z.object({
  ...overviewFields,
  epic_id: z.string().default(""),
  sprints: z.array(z.object({
    sprint_id: nullableString, name: z.string().default(""), status: sprintStatus,
    start_date: z.string().default(""), end_date: z.string().default(""),
    total: count, done: count,
  })).catch([]).default([]),
  activity: z.array(z.object({
    id: z.string(), issue_id: z.string(), issue_identifier: z.string().default(""),
    issue_title: z.string().default(""), actor_type: nullableString, actor_id: nullableString,
    action: z.string(), details: z.record(z.string(), z.unknown()).catch({}).default({}),
    created_at: z.string(),
  })).catch([]).default([]),
});
export const SprintOverviewSchema = z.object({
  ...overviewFields,
  sprint_id: z.string().default(""),
  epics: z.array(z.object({
    epic_id: nullableString, title: z.string().default(""), color: z.string().default("#6366f1"),
    status: epicStatus, total: count, done: count,
  })).catch([]).default([]),
  burndown: z.object({
    total: count, start_date: z.string(), end_date: z.string(),
    days: z.array(z.object({ date: z.string(), remaining: z.number() })),
  }).nullable().catch(null).default(null),
});
export const EMPTY_EPIC_OVERVIEW: EpicOverview = {
  epic_id: "", progress: EMPTY_PLANNING_PROGRESS, last_activity_at: null,
  assignees: [], sprints: [], attention: [], activity: [],
};
export const EMPTY_SPRINT_OVERVIEW: SprintOverview = {
  sprint_id: "", progress: EMPTY_PLANNING_PROGRESS, last_activity_at: null,
  assignees: [], epics: [], attention: [], burndown: null,
};

export const CompleteSprintResultSchema = z.object({
  sprint: SprintSchema,
  open_issues: z.enum(["keep", "move", "unlink"]).catch("keep"),
  target_sprint: SprintSchema.nullable().catch(null).default(null),
  moved_count: count, unlinked_count: count, kept_count: count,
});
export const EMPTY_COMPLETE_SPRINT_RESULT: CompleteSprintResult = {
  sprint: EMPTY_SPRINT, open_issues: "keep", target_sprint: null,
  moved_count: 0, unlinked_count: 0, kept_count: 0,
};
