import type { ListScreenBlocksResponse, ScreenBlock, ScreenFinalEvent, ScreenLineEvent } from "./schemas";

/** Status of a screen still being written: shown line by line, no buttons. */
export const LIVE_SCREEN_STATUS = "live";

/** Model screens are stored with seq 10+i for the i-th block of a reply. */
const MODEL_BLOCK_SEQ = 10;

export function liveScreenId(taskId: string, block: number): string {
  return `live:${taskId}:${block}`;
}

/** Adds one finished line to its live screen. A line already shown (same
 *  node ids) replaces its nodes, so a redelivered event changes nothing. */
export function applyScreenLine(
  data: ListScreenBlocksResponse | undefined,
  ev: ScreenLineEvent,
): ListScreenBlocksResponse {
  const blocks = data?.blocks ?? [];
  const seq = MODEL_BLOCK_SEQ + ev.block;
  // The stored screen arrived first: the live one is no longer needed.
  if (blocks.some((b) => b.task_id === ev.task_id && b.seq === seq && b.status !== LIVE_SCREEN_STATUS)) {
    return { blocks };
  }
  const id = liveScreenId(ev.task_id, ev.block);
  const prev = blocks.find((b) => b.id === id);
  const incoming = new Set(ev.nodes.map((n) => n.id));
  const components = [...(prev?.a2ui?.components ?? []).filter((n) => !incoming.has(n.id)), ...ev.nodes];
  const root = ev.root || prev?.a2ui?.root || (ev.line === 0 ? ev.nodes[0]?.id : undefined) || "root";
  const live: ScreenBlock = {
    id,
    task_id: ev.task_id,
    chat_session_id: ev.chat_session_id,
    seq,
    kind: "model",
    status: LIVE_SCREEN_STATUS,
    a2ui: { version: "", root, components },
    issues: 0,
    created_at: prev?.created_at ?? new Date().toISOString(),
  };
  return { blocks: prev ? blocks.map((b) => (b.id === id ? live : b)) : [live, ...blocks] };
}

/** Puts the stored screen in place of its live draft. */
export function applyScreenFinal(
  data: ListScreenBlocksResponse | undefined,
  ev: ScreenFinalEvent,
): ListScreenBlocksResponse {
  const fin = ev.block;
  const blocks = (data?.blocks ?? []).filter(
    (b) => b.id !== fin.id && !(b.status === LIVE_SCREEN_STATUS && b.task_id === fin.task_id && b.seq === fin.seq),
  );
  return { blocks: [fin, ...blocks] };
}

/** A reply's screens in the order they were written. */
export function screensOfTask(blocks: ScreenBlock[], taskId: string): ScreenBlock[] {
  return blocks.filter((b) => b.task_id === taskId).sort((a, b) => a.seq - b.seq);
}

/** Screens that belong to no reply (the morning digest), newest first. */
export function standaloneScreens(blocks: ScreenBlock[]): ScreenBlock[] {
  return blocks.filter((b) => !b.task_id);
}

const SCREEN_FENCE = /```openui[^\n]*\n[\s\S]*?(?:```|$)/g;

/** Hides screen source in a reply's text: the screen itself is drawn. */
export function stripScreenSource(content: string): string {
  if (!content.includes("```openui")) return content;
  return content.replace(SCREEN_FENCE, "").replace(/\n{3,}/g, "\n\n").trim();
}
