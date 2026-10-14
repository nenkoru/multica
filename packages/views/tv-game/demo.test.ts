// @vitest-environment node
import { describe, expect, it } from "vitest";
import { TvGameController, silentPlayer } from "./controller";
import { DEMO_STEPS, DemoClock, mskNoon, type DemoContext } from "./demo";
import { emptySnapshot } from "./model";

// Runs the demo script on a fake clock: the same steps `?demo=1` plays, so
// the rate limit and the quiet hours are checked with substituted time.
function runDemo() {
  let real = Date.UTC(2026, 9, 14, 6, 0, 0);
  const clock = new DemoClock(mskNoon(real), () => real);
  const controller = new TvGameController({ player: silentPlayer(), now: clock.now });
  const ctx: DemoContext = { controller, clock, state: emptySnapshot(clock.now()) };
  const log: { step: string; cues: string[]; quiet: boolean; plates: string[] }[] = [];
  for (const step of DEMO_STEPS) {
    // Advance real time second by second so warning repeats tick as on screen.
    for (let waited = 0; waited < step.delayMs; waited += 1000) {
      real += 1000;
      controller.tick();
    }
    const before = controller.played.length;
    step.run(ctx);
    const view = controller.getView();
    log.push({
      step: step.id,
      cues: controller.played.slice(before).map((p) => p.cue.kind + ("voice" in p.cue && p.cue.voice ? `:${p.cue.voice}` : "")),
      quiet: view.quietHours,
      plates: view.plates.map((p) => `${p.id}${p.flashing ? "!" : ""}`),
    });
  }
  return { log, controller };
}

describe("demo script", () => {
  it("plays every tier and respects the limits", () => {
    const { log, controller } = runDemo();
    const at = (id: string) => log.find((l) => l.step === id)!;

    expect(at("start").cues).toEqual([]);
    expect(at("deliver").cues).toEqual(["ding"]);
    expect(at("crash").cues).toEqual(["caution:Внимание: запуск упал — Ops"]);
    // Two more crashes and a blocked worker inside 2 minutes: plates only.
    expect(at("more-trouble").cues).toEqual([]);
    expect([...at("more-trouble").plates].sort()).toEqual(["caution:need_you!", "caution:run_failed!"]);
    expect(at("rate-limit-passes").cues).toEqual(["caution:Внимание: 2 запуска упали. Нужен ты — новая задача"]);
    expect(at("warning").cues).toEqual(["warning:Тревога: модель недоступна"]);
    expect(at("acknowledge").plates[0]).toBe("warning:llm");
    expect(at("recover").cues).toEqual(["ding"]);
    expect(at("night").quiet).toBe(true);
    expect(at("night").cues).toEqual([]);
    expect(at("night").plates).toContain("caution:run_failed!");
    expect(at("night-warning").cues).toEqual(["warning:Тревога: демон Multica не на связи"]);
    // The recovery ding is info: silent at night.
    expect(at("night-recover").cues).toEqual([]);

    // The warning chimed again between being raised and acknowledged, and
    // stopped after the acknowledgement.
    const kinds = controller.played.map((p) => p.cue.kind);
    expect(kinds.filter((k) => k === "warning").length).toBeGreaterThanOrEqual(4);
  });
});
