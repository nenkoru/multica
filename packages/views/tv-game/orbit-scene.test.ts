// @vitest-environment node
import { describe, expect, it } from "vitest";
import { emptySnapshot, type TvGameSnapshot } from "./model";
import { MAX_PLANETS, RING_PERIOD_MS, SATELLITE_R, SETTLE_MS, buildOrbit, layoutLabels, type OrbitLabelPlace } from "./orbit-scene";

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

describe("layoutLabels", () => {
  // The live epics of 14.10 (RUSL-1818) on a 390 px wide phone stage.
  const titles = [
    "Qwen на V100: выжать максимум",
    "Покерная GTO decision-модель",
    "Личные проекты и инструменты",
    "Инфраструктура агентов",
    "Автоматический дежурный обход",
    "Эпики и спринты в Multica",
    "Переезд на self-hosted Multica",
    "Семейные сервисы",
    "ТВ-экран",
  ];
  const crowded = snapshot({
    epics: titles.map((title, i) => ({ id: `e${i}`, title, open: 12 - i, doneInSprint: 0, lastActivityAt: fresh })),
    work: ["a1", "a2", "a3"].map((agentId, i) => ({
      issueId: `w${i}`, title: "w", agentId, epicId: `e${i}`, state: "running" as const, lastActivityAt: fresh,
    })),
  });
  const px = 12;
  const measure = (text: string) => text.length * px * 0.6;
  const stage = { width: 390, height: 540, now: NOW };

  it("keeps every label on screen and clear of the orb, planets, satellites and other labels", () => {
    let labelled = 0;
    let planets = 0;
    for (let t = 0; t < RING_PERIOD_MS[2]; t += 1_000) {
      const scene = buildOrbit(crowded, { ...stage, t });
      const labels = layoutLabels(scene, measure, px);
      planets += scene.planets.length;
      labelled += labels.length;
      const boxes = labels.map((l) => ({ x0: l.x - 2, x1: l.x - 2 + l.width, y0: l.y - 2, y1: l.y - 2 + l.height }));
      const clear = (b: (typeof boxes)[number], x: number, y: number, r: number) => {
        const dx = x - Math.max(b.x0, Math.min(x, b.x1));
        const dy = y - Math.max(b.y0, Math.min(y, b.y1));
        return dx * dx + dy * dy >= r * r;
      };
      boxes.forEach((b, i) => {
        expect(b.x0).toBeGreaterThanOrEqual(0);
        expect(b.x1).toBeLessThanOrEqual(stage.width);
        expect(b.y0).toBeGreaterThanOrEqual(0);
        expect(b.y1).toBeLessThanOrEqual(stage.height);
        expect(clear(b, scene.cx, scene.cy, scene.orbR)).toBe(true);
        for (const p of scene.planets) {
          expect(clear(b, p.x, p.y, p.r)).toBe(true);
          for (const s of p.satellites) expect(clear(b, s.x, s.y, 390 * SATELLITE_R)).toBe(true);
        }
        boxes.slice(i + 1).forEach((o) => expect(b.x0 < o.x1 && b.x1 > o.x0 && b.y0 < o.y1 && b.y1 > o.y0).toBe(false));
      });
    }
    // A label is dropped only while planets crowd each other.
    expect(labelled / planets).toBeGreaterThan(0.9);
  });

  // One planet placed by hand, well away from the orb.
  function alone(x: number, y: number) {
    const scene = buildOrbit(snapshot({ epics: [crowded.epics[4]!], work: [] }), { ...stage, t: 0 });
    return { ...scene, planets: [{ ...scene.planets[0]!, x, y }] };
  }

  it("slides a label at the edge back on screen instead of cutting it", () => {
    const scene = alone(380, 100);
    const [label] = layoutLabels(scene, measure, px);
    expect(label!.text).toBe(scene.planets[0]!.label);
    expect(label!.side).toBe("below");
    expect(label!.x - 2 + label!.width).toBeLessThanOrEqual(386);
  });

  it("keeps a label on the side it had while that side stays free", () => {
    const scene = alone(195, 100);
    expect(layoutLabels(scene, measure, px)[0]!.side).toBe("below");
    const memory = new Map<string, OrbitLabelPlace>([["e4", { side: "above", variant: 0, since: 0 }]]);
    expect(layoutLabels(scene, measure, px, memory)[0]!.side).toBe("above");
    // Close to the top edge "above" no longer fits: the label moves and the
    // memory follows it.
    expect(layoutLabels(alone(195, 20), measure, px, memory)[0]!.side).toBe("below");
    expect(memory.get("e4")).toEqual({ side: "below", variant: 0, since: 0 });
  });

  it("grows a shortened label back only after it has settled", () => {
    const scene = alone(195, 100);
    const memory = new Map<string, OrbitLabelPlace>([["e4", { side: "below", variant: 1, since: 0 }]]);
    expect(layoutLabels(scene, measure, px, memory, SETTLE_MS - 1)[0]!.text).toBe("Автоматич… · 8");
    expect(layoutLabels(scene, measure, px, memory, SETTLE_MS)[0]!.text).toBe(scene.planets[0]!.label);
    expect(memory.get("e4")).toEqual({ side: "below", variant: 0, since: SETTLE_MS });
  });
});
