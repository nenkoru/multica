import { z } from "zod";
import { parseWithFallback } from "./schema";

// Responses of the TV game layer endpoints (RUSL-1786): GET /api/tv/game and
// GET /api/tv/health, both behind the kiosk token. Every list defaults to
// empty and every unknown enum falls back to a value that draws nothing
// alarming, so an older or newer server never raises a false alarm.

const count = z.number().catch(0).default(0);
const nullableString = z.string().nullable().catch(null).default(null);

const agent = z.object({ id: z.string(), name: z.string(), slots: z.number().catch(1).default(1) });
const work = z.object({
  issue_id: z.string(),
  title: z.string(),
  agent_id: z.string(),
  epic_id: nullableString,
  state: z.enum(["running", "in_review", "blocked"]).catch("running"),
  last_activity_at: nullableString,
});
const epic = z.object({
  id: z.string(),
  title: z.string(),
  open: count,
  done_in_sprint: count,
  last_activity_at: nullableString,
});
const done = z.object({
  issue_id: z.string(),
  title: z.string(),
  epic_id: nullableString,
  agent_id: nullableString,
  at: z.string().catch("").default(""),
});
const epicClosed = z.object({ epic_id: z.string(), title: z.string(), at: z.string().catch("").default("") });
const failed = z.object({
  task_id: z.string(),
  issue_id: z.string(),
  title: z.string(),
  agent_id: z.string(),
  agent_name: z.string(),
  epic_id: nullableString,
  at: z.string().catch("").default(""),
});
const list = <T extends z.ZodTypeAny>(item: T) => z.array(item).catch([]).default([]);

export const TVGameSchema = z.object({
  generated_at: z.string().catch("").default(""),
  host_ceiling: z.number().catch(12).default(12),
  running: count,
  queued: count,
  done_in_sprint: count,
  agents: list(agent),
  work: list(work),
  epics: list(epic),
  recent_done: list(done),
  recent_epics_closed: list(epicClosed),
  recent_failed: list(failed),
});
export type TVGame = z.infer<typeof TVGameSchema>;

const healthKey = z.enum(["daemon", "llm", "oom", "site"]);
const healthSignal = z.object({
  key: healthKey,
  // Anything this client does not know is not a warning.
  status: z.enum(["ok", "warning", "unknown"]).catch("unknown"),
  reason: z.string().optional().catch(undefined),
  count: count,
});
export const TVHealthSchema = z.object({
  // A signal with an unknown key is dropped rather than failing the list.
  signals: z
    .array(z.unknown())
    .catch([])
    .default([])
    .transform((items) =>
      items.flatMap((item) => {
        const parsed = healthSignal.safeParse(item);
        return parsed.success ? [parsed.data] : [];
      }),
    ),
  checked_at: z.string().catch("").default(""),
});
export type TVHealth = z.infer<typeof TVHealthSchema>;

export const EMPTY_TV_GAME: TVGame = TVGameSchema.parse({});
export const EMPTY_TV_HEALTH: TVHealth = TVHealthSchema.parse({});

export function parseTVGame(data: unknown): TVGame {
  return parseWithFallback(data, TVGameSchema, EMPTY_TV_GAME, { endpoint: "GET /api/tv/game" });
}

export function parseTVHealth(data: unknown): TVHealth {
  return parseWithFallback(data, TVHealthSchema, EMPTY_TV_HEALTH, { endpoint: "GET /api/tv/health" });
}
