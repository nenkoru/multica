// @vitest-environment node
import { describe, expect, it } from "vitest";
import { AlertEngine, isQuietHours } from "./alerts";
import type { TvGameEvent, TvHealthSignal } from "./model";

const MIN = 60_000;
// 12:00 and 23:30 MSK on 14.10.2026.
const NOON = Date.UTC(2026, 9, 14, 9, 0, 0);
const NIGHT = Date.UTC(2026, 9, 14, 20, 30, 0);

const failed = (n: number, agentName = "Ops"): TvGameEvent => ({
  type: "run_failed",
  issueId: `i-${n}`,
  title: `Task ${n}`,
  agentId: "a-ops",
  agentName,
  epicId: null,
});
const done: TvGameEvent = { type: "issue_done", issueId: "d-1", title: "Done", epicId: null, agentId: null };
const health = (key: TvHealthSignal["key"], status: TvHealthSignal["status"]): TvHealthSignal => ({ key, status, count: 1 });

describe("isQuietHours", () => {
  it("is 23:00–08:00 Moscow time", () => {
    expect(isQuietHours(Date.UTC(2026, 9, 14, 19, 59))).toBe(false); // 22:59 MSK
    expect(isQuietHours(Date.UTC(2026, 9, 14, 20, 0))).toBe(true); // 23:00 MSK
    expect(isQuietHours(Date.UTC(2026, 9, 15, 4, 59))).toBe(true); // 07:59 MSK
    expect(isQuietHours(Date.UTC(2026, 9, 15, 5, 0))).toBe(false); // 08:00 MSK
  });
});

describe("AlertEngine", () => {
  it("dings once for any number of completions", () => {
    const engine = new AlertEngine();
    expect(engine.push([done, { ...done, issueId: "d-2" }], NOON)).toEqual([{ kind: "ding" }]);
    expect(engine.plates()).toEqual([]);
  });

  it("sounds a caution at most once per 2 minutes and groups the rest", () => {
    const engine = new AlertEngine();
    expect(engine.push([failed(1)], NOON)).toEqual([{ kind: "caution", voice: "Внимание: Упал запуск у Ops" }]);
    expect(engine.push([failed(2), failed(3)], NOON + 30_000)).toEqual([]);
    expect(engine.push([failed(4)], NOON + 60_000)).toEqual([]);
    expect(engine.tick(NOON + 2 * MIN - 1)).toEqual([]);
    expect(engine.tick(NOON + 2 * MIN)).toEqual([{ kind: "caution", voice: "Внимание: 3 запуска упали" }]);
    // Nothing pending: the next tick is silent.
    expect(engine.tick(NOON + 5 * MIN)).toEqual([]);
    const [plate] = engine.plates();
    expect(plate).toMatchObject({ level: "caution", count: 4, text: "4 запуска упали", flashing: true });
  });

  it("joins different caution groups into one line", () => {
    const engine = new AlertEngine();
    const cues = engine.push(
      [failed(1), failed(2), { type: "need_you", issueId: "n-1", title: "x" }, { type: "no_agents", queued: 3 }],
      NOON,
    );
    expect(cues).toEqual([
      { kind: "caution", voice: "Внимание: 2 запуска упали. Нужен ты: новая задача. Агенты стоят, а очередь не пуста" },
    ]);
  });

  it("keeps quiet at night except for warnings", () => {
    const engine = new AlertEngine();
    expect(engine.push([done, failed(1)], NIGHT)).toEqual([]);
    expect(engine.plates()).toHaveLength(1);
    expect(engine.syncHealth([health("llm", "warning")], NIGHT)).toEqual([
      { kind: "warning", voice: "Тревога: Модель недоступна" },
    ]);
  });

  it("repeats a warning until acknowledged and keeps the plate until recovery", () => {
    const engine = new AlertEngine({ labels: { llm: "27B" } });
    expect(engine.syncHealth([health("llm", "warning")], NOON)).toEqual([{ kind: "warning", voice: "Тревога: 27B недоступна" }]);
    // Steady state is silent.
    expect(engine.syncHealth([health("llm", "warning")], NOON + 1000)).toEqual([]);
    expect(engine.tick(NOON + 8000)).toEqual([{ kind: "warning", voice: null }]);
    expect(engine.tick(NOON + 60_000)).toEqual([{ kind: "warning", voice: "Тревога: 27B недоступна" }]);
    engine.acknowledge();
    expect(engine.tick(NOON + 120_000)).toEqual([]);
    expect(engine.plates()).toMatchObject([{ id: "warning:llm", level: "warning", flashing: false, text: "27B недоступна" }]);
    // "unknown" is not a recovery.
    expect(engine.syncHealth([health("llm", "unknown")], NOON + 130_000)).toEqual([]);
    expect(engine.plates()).toHaveLength(1);
    expect(engine.syncHealth([health("llm", "ok")], NOON + 140_000)).toEqual([{ kind: "ding" }]);
    expect(engine.plates()).toEqual([]);
  });

  it("a new warning flashes again after an earlier one was acknowledged", () => {
    const engine = new AlertEngine();
    engine.syncHealth([health("daemon", "warning")], NOON);
    engine.acknowledge();
    const cues = engine.syncHealth([health("daemon", "warning"), health("site", "warning")], NOON + 1000);
    expect(cues).toEqual([{ kind: "warning", voice: "Тревога: Сайт недоступен" }]);
    expect(engine.hasUnacknowledged()).toBe(true);
    expect(engine.plates().map((p) => p.id)).toEqual(["warning:site", "warning:daemon"]);
  });

  it("mutes everything for an hour, then sounds again", () => {
    const engine = new AlertEngine();
    engine.muteFor(60 * MIN, NOON);
    expect(engine.syncHealth([health("oom", "warning")], NOON + MIN)).toEqual([]);
    expect(engine.mutedUntilAt(NOON + MIN)).toBe(NOON + 60 * MIN);
    expect(engine.tick(NOON + 61 * MIN)).toEqual([{ kind: "warning", voice: "Тревога: Запуски падают по памяти" }]);
  });

  it("drops caution plates after 10 minutes without updates", () => {
    const engine = new AlertEngine();
    engine.push([failed(1)], NOON);
    engine.tick(NOON + 9 * MIN);
    expect(engine.plates()).toHaveLength(1);
    engine.tick(NOON + 10 * MIN);
    expect(engine.plates()).toEqual([]);
  });
});
