// @vitest-environment node
import { describe, expect, it } from "vitest";
import { emptySnapshot, type TvGameSnapshot } from "./model";
import { MAX_PLANETS, RING_PERIOD_MS, buildOrbit } from "./orbit-scene";

const NOW = Date.UTC(2026, 9, 14, 9, 0, 0);
const fresh = new Date(NOW - 60_000).toISOString();
const stale = new Date(NOW - 30 * 3600_000).toISOString();
const size = { width: 390, height: 330, t: 0, now: NOW };

function snapshot(over: Partial<TvGameSnapshot> = {}): TvGameSnapshot {
  return {
    ...emptySnapshot(NOW),
    epics: [
      { id: "busy", title: "Busy epic", open: 15, doneInSprint: 0, lastActivityAt: fresh },
      { id: "quiet", title: "Quiet epic", open: 4, doneInSprint: 0, lastActivityAt: fresh },
      { id: "old", title: "Old epic", open: 3, doneInSprint: 0, lastActivityAt: stale },
      { id: "empty", title: "Empty", open: 0, doneInSprint: 2, lastActivityAt: fresh },
    ],
    work: [
      { issueId: "i1", title: "a", agentId: "a1", epicId: "busy", state: "running", lastActivityAt: fresh },
      { issueId: "i2", title: "b", agentId: "a2", epicId: "busy", state: "running", lastActivityAt: fresh },
      { issueId: "i3", title: "c", agentId: "a3", epicId: "quiet", state: "in_review", lastActivityAt: fresh },
    ],
    ...over,
  };
}

describe("buildOrbit", () => {
  it("puts the busiest epic closest, dims stale ones on the far orbit, skips finished ones", () => {
    const scene = buildOrbit(snapshot(), size);
    const byId = new Map(scene.planets.map((p) => [p.id, p]));
    expect([...byId.keys()].sort()).toEqual(["busy", "old", "quiet"]);
    expect(byId.get("busy")).toMatchObject({ ring: 0, dim: false });
    expect(byId.get("old")).toMatchObject({ ring: 2, dim: true });
    expect(byId.get("busy")!.r).toBeGreaterThan(byId.get("quiet")!.r);
    expect(byId.get("busy")!.label).toBe("Busy epic · 15");
  });

  it("orbits agents that run work as satellites of their planet", () => {
    const scene = buildOrbit(snapshot(), size);
    const byId = new Map(scene.planets.map((p) => [p.id, p]));
    expect(byId.get("busy")!.satellites.map((s) => s.agentId)).toEqual(["a1", "a2"]);
    // In review is not a running run.
    expect(byId.get("quiet")!.satellites).toEqual([]);
  });

  it("moves planets with time, a full lap per ring period", () => {
    const a = buildOrbit(snapshot(), size).planets.find((p) => p.id === "busy")!;
    const b = buildOrbit(snapshot(), { ...size, t: RING_PERIOD_MS[0] / 4 }).planets.find((p) => p.id === "busy")!;
    const c = buildOrbit(snapshot(), { ...size, t: RING_PERIOD_MS[0] }).planets.find((p) => p.id === "busy")!;
    expect(Math.hypot(a.x - b.x, a.y - b.y)).toBeGreaterThan(10);
    expect(c.x).toBeCloseTo(a.x, 5);
  });

  it("caps planets and flies sparks into the orb", () => {
    const epics = Array.from({ length: 20 }, (_, i) => ({ id: `e${i}`, title: `E${i}`, open: i + 1, doneInSprint: 0, lastActivityAt: fresh }));
    const scene = buildOrbit(snapshot({ epics, work: [] }), size);
    expect(scene.planets).toHaveLength(MAX_PLANETS);

    const sparks = [{ id: "s", epicId: "busy", at: NOW - 800 }];
    const mid = buildOrbit(snapshot(), { ...size, sparks, sparkLifetimeMs: 1600 }).sparks[0]!;
    expect(mid.progress).toBeCloseTo(0.5, 5);
    const end = buildOrbit(snapshot(), { ...size, now: NOW + 799, sparks, sparkLifetimeMs: 1600 }).sparks[0]!;
    expect(Math.hypot(end.x - 195, end.y - 165)).toBeLessThan(5);
    expect(buildOrbit(snapshot(), { ...size, now: NOW + 900, sparks, sparkLifetimeMs: 1600 }).sparks).toEqual([]);
  });
});
