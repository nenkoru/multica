// @vitest-environment node
import { describe, expect, it } from "vitest";
import { EMPTY_TV_GAME, parseTVGame, parseTVHealth } from "./tv-game-schemas";

describe("TV game schemas", () => {
  it("parses a full game response", () => {
    const game = parseTVGame({
      generated_at: "2026-10-14T13:00:00Z",
      host_ceiling: 12,
      running: 3,
      queued: 1,
      done_in_sprint: 9,
      agents: [{ id: "a", name: "Ops", slots: 2 }],
      work: [{ issue_id: "i", title: "T", agent_id: "a", epic_id: null, state: "in_review", last_activity_at: null }],
      epics: [{ id: "e", title: "E", open: 4, done_in_sprint: 1, last_activity_at: "2026-10-14T12:00:00Z" }],
      recent_done: [{ issue_id: "d", title: "D", epic_id: "e", agent_id: null, at: "2026-10-14T12:30:00Z" }],
      recent_epics_closed: [],
      recent_failed: [{ task_id: "t", issue_id: "i", title: "T", agent_id: "a", agent_name: "Ops", epic_id: null, at: "x" }],
    });
    expect(game.running).toBe(3);
    expect(game.work[0]?.state).toBe("in_review");
    expect(game.recent_failed[0]?.agent_name).toBe("Ops");
  });

  it("falls back on malformed or unknown values", () => {
    expect(parseTVGame("nope")).toEqual(EMPTY_TV_GAME);
    const game = parseTVGame({ work: [{ issue_id: "i", title: "T", agent_id: "a", state: "dancing" }], agents: "x" });
    expect(game.work[0]?.state).toBe("running");
    expect(game.agents).toEqual([]);
    expect(game.host_ceiling).toBe(12);
  });

  it("never turns an unknown health status into a warning", () => {
    const health = parseTVHealth({
      signals: [
        { key: "llm", status: "warning", reason: "http_5xx", count: 2 },
        { key: "site", status: "degraded", count: 1 },
        { key: "gpu", status: "warning", count: 1 },
      ],
    });
    expect(health.signals).toEqual([
      { key: "llm", status: "warning", reason: "http_5xx", count: 2 },
      { key: "site", status: "unknown", count: 1 },
    ]);
    expect(parseTVHealth(null).signals).toEqual([]);
  });
});
