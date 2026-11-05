import type { TvEvent, TvIssue, TvOverview } from "./schema";

// One stream of discrete changes between two TV snapshots, for anything that
// reacts to change rather than state (animations, sounds). The first snapshot
// is a baseline and yields nothing.

export type TvStreamEvent =
  | { type: "issue_done"; at: string; identifier: string | null; title: string | null; actor: string | null }
  | { type: "run_failed"; at: string; identifier: string | null; title: string | null; agent: string | null; reason: string | null }
  | { type: "needs_owner_added"; issue: TvIssue }
  | { type: "needs_owner_cleared"; issue: TvIssue }
  | {
      type: "counters_changed";
      from: TvCounters;
      to: TvCounters;
    };

export interface TvCounters {
  agents_working: number;
  needs_owner_total: number;
  stale_total: number;
}

function counters(o: TvOverview): TvCounters {
  return { agents_working: o.agents_working, needs_owner_total: o.needs_owner_total, stale_total: o.stale_total };
}

const feedKey = (e: TvEvent) => `${e.kind}|${e.at}|${e.issue_identifier ?? ""}`;

export function diffTvOverview(prev: TvOverview | null | undefined, next: TvOverview): TvStreamEvent[] {
  if (!prev) return [];
  const out: TvStreamEvent[] = [];

  const seen = new Set(prev.feed.map(feedKey));
  const newest = prev.feed.reduce((max, e) => (e.at > max ? e.at : max), "");
  // Oldest first, so consumers can replay in order. Events older than the
  // previous newest are ones that merely re-entered the capped feed.
  for (const e of [...next.feed].reverse()) {
    if (seen.has(feedKey(e)) || e.at < newest) continue;
    if (e.kind === "done") {
      out.push({ type: "issue_done", at: e.at, identifier: e.issue_identifier, title: e.issue_title, actor: e.actor_name });
    } else {
      out.push({ type: "run_failed", at: e.at, identifier: e.issue_identifier, title: e.issue_title, agent: e.actor_name, reason: e.failure_reason });
    }
  }

  const before = new Map(prev.needs_owner.map((i) => [i.id, i]));
  const after = new Map(next.needs_owner.map((i) => [i.id, i]));
  for (const [id, issue] of after) if (!before.has(id)) out.push({ type: "needs_owner_added", issue });
  for (const [id, issue] of before) if (!after.has(id)) out.push({ type: "needs_owner_cleared", issue });

  const from = counters(prev);
  const to = counters(next);
  if (from.agents_working !== to.agents_working || from.needs_owner_total !== to.needs_owner_total || from.stale_total !== to.stale_total) {
    out.push({ type: "counters_changed", from, to });
  }
  return out;
}
