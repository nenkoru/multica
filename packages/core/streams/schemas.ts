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
    voice: z.boolean().default(false),
    screens: z.boolean().default(false),
  })
  .loose();

/** One screen node as stored (A2UI-like): `{id, component: {Name: props}}`.
 *  Props stay opaque here; the renderer validates each component's own. */
export const ScreenNodeSchema = z.object({
  id: z.string(),
  component: z.record(z.string(), z.record(z.string(), z.unknown())),
});

export const ScreenDocSchema = z
  .object({
    version: z.string().default(""),
    root: z.string().default(""),
    components: z.array(ScreenNodeSchema).default([]),
  })
  .loose();

export const ScreenBlockSchema = z
  .object({
    id: z.string(),
    task_id: z.string().default(""),
    chat_session_id: z.string().default(""),
    message_id: z.string().optional(),
    seq: z.number().default(0),
    kind: z.string().default("model"),
    status: z.string().default("valid"),
    // A malformed doc hides that one screen, not the whole list.
    a2ui: ScreenDocSchema.nullish().catch(null),
    issues: z.number().default(0),
    created_at: z.string().default(""),
  })
  .loose();

export const ListScreenBlocksResponseSchema = z.object({ blocks: z.array(ScreenBlockSchema).default([]) }).loose();

export const ScreenPressResultSchema = z
  .object({
    status: z.string(),
    confirm: z.object({ title: z.string().default(""), text: z.string().default("") }).nullish(),
    result: z.record(z.string(), z.unknown()).nullish(),
  })
  .loose();

export const TranscriptionSchema = z.object({ text: z.string().default("") }).loose();

/** chat:ui_line — the nodes of one finished line of a screen being written. */
export const ScreenLineEventSchema = z.object({
  task_id: z.string(),
  chat_session_id: z.string(),
  block: z.number(),
  line: z.number(),
  nodes: z.array(ScreenNodeSchema),
  root: z.string().optional(),
});

/** chat:ui_final — a screen checked, repaired and stored. */
export const ScreenFinalEventSchema = z.object({
  task_id: z.string().default(""),
  chat_session_id: z.string(),
  message_id: z.string().optional(),
  block: ScreenBlockSchema,
});

export type Stream = z.infer<typeof StreamSchema>;
export type ListStreamsResponse = z.infer<typeof ListStreamsResponseSchema>;
export type Multika = z.infer<typeof MultikaSchema>;
export type Transcription = z.infer<typeof TranscriptionSchema>;
export type ScreenNode = z.infer<typeof ScreenNodeSchema>;
export type ScreenDoc = z.infer<typeof ScreenDocSchema>;
export type ScreenBlock = z.infer<typeof ScreenBlockSchema>;
export type ListScreenBlocksResponse = z.infer<typeof ListScreenBlocksResponseSchema>;
export type ScreenPressResult = z.infer<typeof ScreenPressResultSchema>;
export type ScreenLineEvent = z.infer<typeof ScreenLineEventSchema>;
export type ScreenFinalEvent = z.infer<typeof ScreenFinalEventSchema>;

export const EMPTY_LIST_STREAMS_RESPONSE: ListStreamsResponse = { streams: [] };
export const EMPTY_MULTIKA: Multika = { enabled: false, voice: false, screens: false };
export const EMPTY_LIST_SCREEN_BLOCKS: ListScreenBlocksResponse = { blocks: [] };
/** An unreadable press answer is treated as a failure, never as done. */
export const FAILED_SCREEN_PRESS: ScreenPressResult = { status: "failed" };
export const EMPTY_TRANSCRIPTION: Transcription = { text: "" };
