import { z } from "zod";
import type { Epic, Sprint } from "../types";

const planningFields = {
  id: z.string(), workspace_id: z.string(),
  issue_count: z.number().default(0), done_count: z.number().default(0),
  created_at: z.string(), updated_at: z.string(),
};
export const EpicSchema = z.object({
  ...planningFields, title: z.string(), description: z.string().nullable().default(null),
  color: z.string().default("#6366f1"), status: z.enum(["open", "closed"]).catch("open"),
});
export const SprintSchema = z.object({
  ...planningFields, name: z.string(), goal: z.string().nullable().default(null),
  start_date: z.string(), end_date: z.string(),
  status: z.enum(["planned", "active", "completed", "cancelled"]).catch("planned"),
});
export const EpicListSchema = z.object({ epics: z.array(EpicSchema), total: z.number() });
export const SprintListSchema = z.object({ sprints: z.array(SprintSchema), total: z.number() });
export const EMPTY_EPIC: Epic = {
  id: "", workspace_id: "", title: "", description: null, color: "#6366f1", status: "open",
  issue_count: 0, done_count: 0, created_at: "", updated_at: "",
};
export const EMPTY_SPRINT: Sprint = {
  id: "", workspace_id: "", name: "", goal: null, start_date: "", end_date: "", status: "planned",
  issue_count: 0, done_count: 0, created_at: "", updated_at: "",
};
