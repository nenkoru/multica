// Input contract of the TV game layer (RUSL-1786). The base TV screen
// (RUSL-1784) maps its aggregate into a snapshot on every refresh and pushes
// the events it already receives over the WebSocket (failed runs, status
// changes). Everything here is plain data so the layer stays testable without
// a server and the demo mode can drive it from a script.

export type TvGameWorkState = "running" | "in_review" | "blocked";

export interface TvGameEpic {
  id: string;
  title: string;
  /** Issues still open in the epic. Drives the size of the crystal field. */
  open: number;
  /** Issues finished during the current sprint. */
  doneInSprint: number;
  /** ISO timestamp of the last issue activity in the epic. */
  lastActivityAt: string | null;
}

export interface TvGameAgent {
  id: string;
  name: string;
  /** Concurrent run slots; the map draws up to this many workers. */
  slots: number;
}

export interface TvGameWork {
  issueId: string;
  title: string;
  agentId: string;
  epicId: string | null;
  state: TvGameWorkState;
  /** Blocked on an action that only Ruslan can take. */
  needsYou?: boolean;
  /** ISO timestamp of the issue's last activity. */
  lastActivityAt: string | null;
}

export type TvHealthKey = "daemon" | "llm" | "oom" | "site";
export type TvHealthStatus = "ok" | "warning" | "unknown";

export interface TvHealthSignal {
  key: TvHealthKey;
  status: TvHealthStatus;
  reason?: string;
  count: number;
}

export interface TvGameSnapshot {
  /** When the snapshot was taken, epoch ms. */
  at: number;
  epics: TvGameEpic[];
  agents: TvGameAgent[];
  work: TvGameWork[];
  /** Issues done in the current sprint — the "resources" counter. */
  doneInSprint: number;
  /** Runs executing right now. */
  runningRuns: number;
  /** Real concurrency ceiling of the host, not the sum of agent slots. */
  hostCeiling: number;
  /** Issues queued for an agent but not started. */
  queued: number;
  /** Issues closed recently, newest first; used to detect new completions. */
  recentDone: { issueId: string; title: string; epicId: string | null; agentId: string | null }[];
  /** Epics closed recently, newest first. */
  recentEpicsClosed: { epicId: string; title: string }[];
  health: TvHealthSignal[];
}

export type TvGameEvent =
  | { type: "issue_done"; issueId: string; title: string; epicId: string | null; agentId: string | null }
  | { type: "epic_closed"; epicId: string; title: string }
  | { type: "run_failed"; issueId: string; title: string; agentId: string; agentName: string; epicId: string | null }
  | { type: "need_you"; issueId: string; title: string }
  | { type: "stuck"; issueId: string; title: string }
  | { type: "no_agents"; queued: number };

export const STUCK_AFTER_MS = 24 * 60 * 60 * 1000;

export function isStale(lastActivityAt: string | null, now: number): boolean {
  if (!lastActivityAt) return false;
  const at = Date.parse(lastActivityAt);
  return Number.isFinite(at) && now - at > STUCK_AFTER_MS;
}

export function emptySnapshot(at = 0): TvGameSnapshot {
  return {
    at,
    epics: [],
    agents: [],
    work: [],
    doneInSprint: 0,
    runningRuns: 0,
    hostCeiling: 12,
    queued: 0,
    recentDone: [],
    recentEpicsClosed: [],
    health: [],
  };
}

/**
 * Turns two consecutive snapshots into the info and caution transitions the
 * alarm layer reacts to. Infrastructure health is a state, not an event: the
 * alarm engine reads it from every snapshot, so a warning that is already
 * true when the screen opens still sounds. Only transitions produce events: a state that was already true
 * in `prev` is silent, which is the cockpit principle the sounds follow.
 * The first snapshot after load (`prev === null`) never produces events, so
 * opening the screen does not replay the whole board as alarms.
 */
export function deriveEvents(prev: TvGameSnapshot | null, next: TvGameSnapshot): TvGameEvent[] {
  if (!prev) return [];
  const events: TvGameEvent[] = [];

  const seenDone = new Set(prev.recentDone.map((d) => d.issueId));
  for (const done of next.recentDone) {
    if (!seenDone.has(done.issueId)) events.push({ type: "issue_done", ...done });
  }

  const seenEpics = new Set(prev.recentEpicsClosed.map((e) => e.epicId));
  for (const epic of next.recentEpicsClosed) {
    if (!seenEpics.has(epic.epicId)) events.push({ type: "epic_closed", ...epic });
  }

  const prevWork = new Map(prev.work.map((w) => [w.issueId, w]));
  for (const work of next.work) {
    const before = prevWork.get(work.issueId);
    if (work.needsYou && !before?.needsYou) {
      events.push({ type: "need_you", issueId: work.issueId, title: work.title });
    }
    // A stuck issue is reported once, when it crosses the 24 h line. An issue
    // that was already stale in the previous snapshot stays silent.
    if (before && isStale(work.lastActivityAt, next.at) && !isStale(before.lastActivityAt, prev.at)) {
      events.push({ type: "stuck", issueId: work.issueId, title: work.title });
    }
  }

  const idleBefore = prev.runningRuns === 0 && prev.queued > 0;
  const idleNow = next.runningRuns === 0 && next.queued > 0;
  if (idleNow && !idleBefore) events.push({ type: "no_agents", queued: next.queued });

  return events;
}
