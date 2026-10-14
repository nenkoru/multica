// @vitest-environment node
import { describe, expect, it } from "vitest";
import { deriveEvents, emptySnapshot, type TvGameSnapshot, type TvGameWork } from "./model";

const H = 60 * 60_000;
const T0 = Date.UTC(2026, 9, 14, 9, 0, 0);

const work = (over: Partial<TvGameWork> = {}): TvGameWork => ({
  issueId: "i-1",
  title: "Task",
  agentId: "a-1",
  epicId: "e-1",
  state: "running",
  lastActivityAt: new Date(T0 - H).toISOString(),
  ...over,
});
const snap = (over: Partial<TvGameSnapshot>): TvGameSnapshot => ({ ...emptySnapshot(T0), ...over });

describe("deriveEvents", () => {
  it("is silent on the first snapshot", () => {
    expect(deriveEvents(null, snap({ work: [work({ needsYou: true })], queued: 5 }))).toEqual([]);
  });

  it("reports new completions and closed epics once", () => {
    const done = { issueId: "d-1", title: "Done", epicId: "e-1", agentId: "a-1" };
    const a = snap({ recentDone: [] });
    const b = snap({ recentDone: [done], recentEpicsClosed: [{ epicId: "e-1", title: "Epic" }] });
    expect(deriveEvents(a, b)).toEqual([
      { type: "issue_done", ...done },
      { type: "epic_closed", epicId: "e-1", title: "Epic" },
    ]);
    expect(deriveEvents(b, b)).toEqual([]);
  });

  it("reports a new needs-you issue on the transition only", () => {
    const a = snap({ work: [work()] });
    const b = snap({ work: [work({ state: "blocked", needsYou: true })] });
    expect(deriveEvents(a, b)).toEqual([{ type: "need_you", issueId: "i-1", title: "Task" }]);
    expect(deriveEvents(b, b)).toEqual([]);
  });

  it("reports an issue crossing 24 h without activity", () => {
    const item = work({ lastActivityAt: new Date(T0 - 24 * H + 30_000).toISOString() });
    const a = snap({ at: T0, work: [item] });
    const b = snap({ at: T0 + 60_000, work: [item] });
    expect(deriveEvents(a, b)).toEqual([{ type: "stuck", issueId: "i-1", title: "Task" }]);
    const c = snap({ at: T0 + 120_000, work: [item] });
    expect(deriveEvents(b, c)).toEqual([]);
  });
});
