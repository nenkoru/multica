import { z } from "zod";

export interface TvSprint {
  id: string;
  name: string;
  status: string;
  start_date: string;
  end_date: string;
  total: number;
  done: number;
}

export interface TvIssue {
  id: string;
  identifier: string;
  title: string;
  status: string;
  assignee_type: string | null;
  assignee_name: string | null;
  since: string;
  last_activity_at: string;
  stale: boolean;
}

export interface TvEpic {
  id: string;
  title: string;
  in_progress: number;
  in_review: number;
  blocked: number;
  done_in_window: number;
  last_activity_at: string | null;
  issues: TvIssue[];
}

export type TvEventKind = "done" | "run_failed";

export interface TvEvent {
  kind: TvEventKind;
  at: string;
  issue_identifier: string | null;
  issue_title: string | null;
  actor_name: string | null;
  failure_reason: string | null;
}

export interface TvOverview {
  generated_at: string;
  workspace_name: string;
  owner_label: string;
  sprint: TvSprint | null;
  done_window_start: string;
  agents_working: number;
  needs_owner_total: number;
  stale_total: number;
  no_epic_active: number;
  epics: TvEpic[];
  epics_hidden: number;
  needs_owner: TvIssue[];
  feed: TvEvent[];
}

const count = z.number().catch(0).default(0);
const text = z.string().catch("").default("");
const nullableText = z.string().nullable().catch(null).default(null);

const TvIssueSchema = z.object({
  id: z.string(),
  identifier: text,
  title: text,
  status: text,
  assignee_type: nullableText,
  assignee_name: nullableText,
  since: text,
  last_activity_at: text,
  stale: z.boolean().catch(false).default(false),
});

const issues = z.array(TvIssueSchema).catch([]).default([]);

const TvEpicSchema = z.object({
  id: z.string(),
  title: text,
  in_progress: count,
  in_review: count,
  blocked: count,
  done_in_window: count,
  last_activity_at: nullableText,
  issues,
});

const TvEventSchema = z.object({
  kind: z.enum(["done", "run_failed"]),
  at: text,
  issue_identifier: nullableText,
  issue_title: nullableText,
  actor_name: nullableText,
  failure_reason: nullableText,
});

const TvSprintSchema = z.object({
  id: z.string(),
  name: text,
  status: text,
  start_date: z.string(),
  end_date: z.string(),
  total: count,
  done: count,
});

export const TvOverviewSchema = z.object({
  generated_at: text,
  workspace_name: text,
  owner_label: text,
  sprint: TvSprintSchema.nullable().catch(null).default(null),
  done_window_start: text,
  agents_working: count,
  needs_owner_total: count,
  stale_total: count,
  no_epic_active: count,
  epics: z.array(TvEpicSchema).catch([]).default([]),
  epics_hidden: count,
  needs_owner: issues,
  // An event kind this client does not know is dropped, not the whole feed.
  feed: z
    .array(z.unknown())
    .catch([])
    .default([])
    .transform((items) =>
      items.flatMap((item) => {
        const parsed = TvEventSchema.safeParse(item);
        return parsed.success ? [parsed.data] : [];
      }),
    ),
});

export const EMPTY_TV_OVERVIEW: TvOverview = {
  generated_at: "",
  workspace_name: "",
  owner_label: "",
  sprint: null,
  done_window_start: "",
  agents_working: 0,
  needs_owner_total: 0,
  stale_total: 0,
  no_epic_active: 0,
  epics: [],
  epics_hidden: 0,
  needs_owner: [],
  feed: [],
};
