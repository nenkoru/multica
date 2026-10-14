// @vitest-environment node
import { describe, expect, it } from "vitest";
import { TvGameController, orbMood, silentPlayer } from "./controller";
import { DEMO_STEPS, DemoClock, demoContext, mskNoon } from "./demo";
import { SOUND_TABLE } from "./sound-table";

// Runs the demo script on a fake clock: the same steps `?demo=1` plays, so
// priority, acknowledgement, the rate limit and the quiet hours are checked
// with substituted time.
function runDemo() {
  let real = Date.UTC(2026, 9, 14, 6, 0, 0);
  const clock = new DemoClock(mskNoon(real), () => real);
  const controller = new TvGameController({ player: silentPlayer(), now: clock.now });
  const ctx = demoContext(controller, clock);
  const log: { step: string; cues: string[]; mood: string; quiet: boolean }[] = [];
  for (const step of DEMO_STEPS) {
    const before = controller.played.length;
    // Advance real time second by second so warning repeats tick as on screen.
    for (let waited = 0; waited < step.delayMs; waited += 1000) {
      real += 1000;
      controller.tick();
    }
    const repeats = controller.played.slice(before).map((p) => `${p.cue.signal}*`);
    const mid = controller.played.length;
    step.run(ctx);
    const view = controller.getView();
    log.push({
      step: step.id,
      cues: [...repeats, ...controller.played.slice(mid).map((p) => p.cue.signal)],
      mood: orbMood(view, ctx.overview.needs_owner_total),
      quiet: view.quietHours,
    });
  }
  return { log, controller };
}

describe("demo script", () => {
  const { log, controller } = runDemo();
  const at = (id: string) => log.find((l) => l.step === id)!;

  it("plays the Orbit moods", () => {
    expect(at("calm").mood).toBe("calm");
    expect(at("call").mood).toBe("call");
    expect(at("talk").mood).toBe("talk");
    expect(at("talk-end").mood).toBe("calm");
    expect(at("daemon").mood).toBe("alarm");
  });

  it("sounds every row of the sound table at least once", () => {
    const played = new Set(controller.played.map((p) => p.cue.signal));
    expect(SOUND_TABLE.filter((e) => !played.has(e.id)).map((e) => e.id)).toEqual([]);
  });

  it("follows priority, acknowledgement, rate limit and night rules", () => {
    expect(at("calm").cues).toEqual([]);
    expect(at("issue-done").cues).toEqual(["issue_done"]);
    expect(at("epic-closed").cues).toEqual(["epic_closed"]);
    expect(at("call").cues).toEqual(["master_caution"]);
    // Each caution waits out the 2-minute limit; the grouped "needs you"
    // goes with the more important overspeed.
    expect(at("oom").cues).toEqual(["oom"]);
    expect(at("autopilot").cues).toEqual(["autopilot"]);
    expect(at("failed-burst").cues).toEqual(["failed_burst"]);
    expect(at("duplicate-run").cues).toEqual(["duplicate_run"]);
    expect(at("llm-slow").cues).toEqual(["llm_slow"]);
    expect(at("minimums").cues).toEqual(["minimums"]);
    expect(at("cautions-clear").cues).toEqual([]);
    expect(at("daemon").cues).toEqual(["daemon"]);
    // The daemon warning repeated; the model failing under it stayed silent.
    expect(at("llm-under-daemon").cues).toEqual(["daemon*"]);
    expect(at("acknowledge-daemon").cues).toEqual([]);
    expect(at("daemon-cleared").cues).toEqual(["cleared"]);
    expect(at("llm").cues).toEqual(["llm"]);
    expect(at("site-and-stall").cues).toEqual(["site"]);
    expect(at("site-cleared").cues).toEqual(["cleared"]);
    expect(at("stall").cues).toEqual(["stall"]);
    expect(at("budget").cues).toEqual(["budget"]);
    expect(at("night-caution").quiet).toBe(true);
    expect(at("night-caution").cues).toEqual([]);
    expect(at("night-warning").cues).toEqual(["daemon"]);
    expect(at("night-cleared").cues).toEqual([]);
  });
});
