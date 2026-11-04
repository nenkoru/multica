import { z } from "zod";

// Fabrika streams (fork-owned, server/internal/streams). Kept in its own
// module so upstream merges of api/schemas.ts stay conflict-free.

// A missing or malformed state reads as "unknown" (a neutral dot), never as
// a healthy "running".
export const StreamStateSchema = z
  .enum(["running", "waiting", "asking", "budget", "paused", "broken"])
  .or(z.string())
  .catch("unknown");

export const StreamSchema = z
  .object({
    id: z.string(),
    slug: z.string(),
    title: z.string().default(""),
    state: StreamStateSchema.default("unknown"),
    state_label: z.string().default(""),
    reason: z.string().default(""),
    until: z.string().nullish(),
    summary: z.string().default(""),
    active: z.boolean().default(true),
    last_milestone_at: z.string().nullish(),
  })
  .loose();

export const ListStreamsResponseSchema = z
  .object({ streams: z.array(StreamSchema).default([]) })
  .loose();

export const MultikaSchema = z
  .object({
    enabled: z.boolean().default(false),
    agent_id: z.string().optional(),
    chat_session_id: z.string().optional(),
    focus_slug: z.string().optional(),
    focus_title: z.string().optional(),
    focus_until: z.string().nullish(),
  })
  .loose();

export type Stream = z.infer<typeof StreamSchema>;
export type ListStreamsResponse = z.infer<typeof ListStreamsResponseSchema>;
export type Multika = z.infer<typeof MultikaSchema>;

export const EMPTY_LIST_STREAMS_RESPONSE: ListStreamsResponse = { streams: [] };
export const EMPTY_MULTIKA: Multika = { enabled: false };
