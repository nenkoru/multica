import type { TVGame, TVHealth } from "@multica/core/api/tv-game-schemas";
import type { TvGameEvent, TvGameSnapshot } from "./model";

// Maps the parsed /api/tv/game and /api/tv/health responses into the game
// layer's snapshot, and turns newly failed runs into events. `needsYou` is
// the set of issue ids the base screen already shows under "needs you".

export function toTvGameSnapshot(game: TVGame, health: TVHealth, needsYou: ReadonlySet<string>, at: number): TvGameSnapshot {
  return {
    at,
    epics: game.epics.map((e) => ({
      id: e.id,
      title: e.title,
      open: e.open,
      doneInSprint: e.done_in_sprint,
      lastActivityAt: e.last_activity_at,
    })),
    agents: game.agents.map((a) => ({ id: a.id, name: a.name, slots: a.slots })),
    work: game.work.map((w) => ({
      issueId: w.issue_id,
      title: w.title,
      agentId: w.agent_id,
      epicId: w.epic_id,
      state: w.state,
      needsYou: w.state === "blocked" && needsYou.has(w.issue_id),
      lastActivityAt: w.last_activity_at,
      since: w.since,
    })),
    doneInSprint: game.done_in_sprint,
    doneToday: game.done_today,
    streakDays: game.streak_days,
    runningRuns: game.running,
    hostCeiling: game.host_ceiling,
    queued: game.queued,
    recentDone: game.recent_done.map((d) => ({ issueId: d.issue_id, title: d.title, epicId: d.epic_id, agentId: d.agent_id })),
    recentEpicsClosed: game.recent_epics_closed.map((e) => ({ epicId: e.epic_id, title: e.title })),
    health: health.signals.map((s) => ({ key: s.key, status: s.status, reason: s.reason, count: s.count })),
  };
}

/**
 * Failed runs from consecutive /api/tv/game responses. The first response is
 * a baseline: runs that failed before the screen opened stay silent.
 */
export class FailedRunTracker {
  private seen: Set<string> | null = null;

  next(game: TVGame): TvGameEvent[] {
    const ids = new Set(game.recent_failed.map((f) => f.task_id));
    if (!this.seen) {
      this.seen = ids;
      return [];
    }
    const fresh = game.recent_failed.filter((f) => !this.seen!.has(f.task_id));
    // Keep ids that left the window too, so a late page never replays them.
    for (const id of ids) this.seen.add(id);
    if (this.seen.size > 500) this.seen = ids;
    return fresh.map((f) => ({
      type: "run_failed",
      issueId: f.issue_id,
      title: f.title,
      agentId: f.agent_id,
      agentName: f.agent_name,
      epicId: f.epic_id,
    }));
  }
}
