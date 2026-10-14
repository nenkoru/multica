// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseTVGame, parseTVHealth } from "@multica/core/api/tv-game-schemas";
import { FailedRunTracker, toTvGameSnapshot } from "./adapter";

const failed = (taskId: string) => ({
  task_id: taskId,
  issue_id: `i-${taskId}`,
  title: "Run",
  agent_id: "a",
  agent_name: "Ops",
  epic_id: null,
  at: "2026-10-14T12:00:00Z",
});

describe("toTvGameSnapshot", () => {
  it("maps the API shape and marks needs-you only on blocked work", () => {
    const game = parseTVGame({
      running: 2,
      queued: 1,
      host_ceiling: 12,
      done_in_sprint: 5,
      agents: [{ id: "a", name: "Ops", slots: 2 }],
      work: [
        { issue_id: "b", title: "B", agent_id: "a", epic_id: "e", state: "blocked", last_activity_at: null },
        { issue_id: "r", title: "R", agent_id: "a", epic_id: "e", state: "running", last_activity_at: null },
      ],
    });
    const health = parseTVHealth({ signals: [{ key: "daemon", status: "warning", count: 1 }] });
    const snapshot = toTvGameSnapshot(game, health, new Set(["b", "r"]), 1000);
    expect(snapshot).toMatchObject({ at: 1000, runningRuns: 2, queued: 1, hostCeiling: 12, doneInSprint: 5 });
    expect(snapshot.work.map((w) => [w.issueId, w.needsYou])).toEqual([
      ["b", true],
      ["r", false],
    ]);
    expect(snapshot.health).toEqual([{ key: "daemon", status: "warning", reason: undefined, count: 1 }]);
  });
});

describe("FailedRunTracker", () => {
  it("treats the first response as a baseline and reports each new failure once", () => {
    const tracker = new FailedRunTracker();
    expect(tracker.next(parseTVGame({ recent_failed: [failed("1")] }))).toEqual([]);
    expect(tracker.next(parseTVGame({ recent_failed: [failed("2"), failed("1")] }))).toEqual([
      { type: "run_failed", issueId: "i-2", title: "Run", agentId: "a", agentName: "Ops", epicId: null },
    ]);
    expect(tracker.next(parseTVGame({ recent_failed: [failed("2")] }))).toEqual([]);
    // A failure that left the window does not come back as new.
    expect(tracker.next(parseTVGame({ recent_failed: [failed("1")] }))).toEqual([]);
  });
});
