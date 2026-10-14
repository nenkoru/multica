// @vitest-environment node
import { describe, expect, it } from "vitest";
import { emptySnapshot, type TvGameSnapshot } from "./model";
import { CARRY_LOOP_MS, MAX_FIELDS, NO_EPIC_FIELD, buildScene } from "./scene";

const NOW = Date.UTC(2026, 9, 14, 9, 0, 0);
const fresh = new Date(NOW - 60_000).toISOString();
const size = { width: 1920, height: 400, t: 0, now: NOW };

function snapshot(over: Partial<TvGameSnapshot> = {}): TvGameSnapshot {
  return {
    ...emptySnapshot(NOW),
    epics: [
      { id: "e-1", title: "Epic one", open: 9, doneInSprint: 3, lastActivityAt: fresh },
      { id: "e-2", title: "Epic two", open: 2, doneInSprint: 0, lastActivityAt: new Date(NOW - 30 * 3600_000).toISOString() },
      { id: "e-done", title: "Finished", open: 0, doneInSprint: 4, lastActivityAt: fresh },
    ],
    agents: [
      { id: "a-1", name: "Dev", slots: 2 },
      { id: "a-2", name: "Ops", slots: 1 },
      { id: "a-3", name: "Idle", slots: 3 },
    ],
    work: [
      { issueId: "i-1", title: "Dig", agentId: "a-1", epicId: "e-1", state: "running", lastActivityAt: fresh },
      { issueId: "i-2", title: "Carry", agentId: "a-1", epicId: "e-2", state: "in_review", lastActivityAt: fresh },
      { issueId: "i-3", title: "Stuck", agentId: "a-2", epicId: null, state: "blocked", lastActivityAt: fresh },
    ],
    ...over,
  };
}

describe("buildScene", () => {
  it("maps work to worker modes at the right fields", () => {
    const scene = buildScene(snapshot(), size);
    expect(scene.fields.map((f) => f.id)).toEqual(["e-1", "e-2", NO_EPIC_FIELD]);
    const byIssue = new Map(scene.units.map((u) => [u.issueId, u]));
    expect(byIssue.get("i-1")?.mode).toBe("mining");
    expect(byIssue.get("i-2")?.mode).toBe("carrying");
    expect(byIssue.get("i-3")?.mode).toBe("blocked");
    // An agent without work stands at the base with one worker, not three.
    const idle = scene.units.filter((u) => u.agentId === "a-3");
    expect(idle).toHaveLength(1);
    expect(idle[0]?.mode).toBe("idle");
    expect(idle[0]!.x).toBeLessThan(scene.fields[0]!.x);
    // Fields stay clear of the plates column on the right.
    expect(Math.max(...scene.fields.map((f) => f.x))).toBeLessThan(1920 * 0.7);
  });

  it("marks stale epics as overgrown and sizes fields by open issues", () => {
    const scene = buildScene(snapshot(), size);
    const [one, two] = scene.fields;
    expect(one).toMatchObject({ overgrown: false, crystals: 3, depletion: 0.25 });
    expect(two).toMatchObject({ overgrown: true, crystals: 2, depletion: 0 });
  });

  it("caps workers at the agent's slots and counts the rest", () => {
    const extra = snapshot({
      work: [
        { issueId: "x-1", title: "a", agentId: "a-2", epicId: "e-1", state: "running", lastActivityAt: fresh },
        { issueId: "x-2", title: "b", agentId: "a-2", epicId: "e-1", state: "running", lastActivityAt: fresh },
      ],
    });
    const scene = buildScene(extra, size);
    expect(scene.units.filter((u) => u.agentId === "a-2")).toHaveLength(1);
    expect(scene.hiddenWork).toBe(1);
  });

  it("caps the number of fields", () => {
    const epics = Array.from({ length: 10 }, (_, i) => ({ id: `e-${i}`, title: `E${i}`, open: i + 1, doneInSprint: 0, lastActivityAt: fresh }));
    expect(buildScene(snapshot({ epics, work: [] }), size).fields).toHaveLength(MAX_FIELDS);
  });

  it("walks carrying workers to the base and back", () => {
    const s = snapshot();
    const xs = [0, CARRY_LOOP_MS / 4, CARRY_LOOP_MS / 2].map(
      (t) => buildScene(s, { ...size, t }).units.find((u) => u.issueId === "i-2")!.x,
    );
    expect(new Set(xs).size).toBeGreaterThan(1);
  });

  it("keeps a wreck for a minute", () => {
    const wreck = { issueId: "w-1", title: "Crashed run", epicId: "e-1", agentId: "a-1", at: NOW - 30_000 };
    expect(buildScene(snapshot(), { ...size, wrecks: [wreck] }).wrecks).toMatchObject([{ issueId: "w-1", age: 0.5 }]);
    expect(buildScene(snapshot(), { ...size, now: NOW + 31_000, wrecks: [wreck] }).wrecks).toEqual([]);
  });
});
