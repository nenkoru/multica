// @vitest-environment node
import { describe, expect, it } from "vitest";
import { AlertEngine, isQuietHours, type SoundCue } from "./alerts";
import type { TvGameEvent, TvHealthKey, TvHealthSignal } from "./model";
import { SOUND_TABLE } from "./sound-table";

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
const sig = (key: TvHealthKey, status: TvHealthSignal["status"], count = 1): TvHealthSignal => ({ key, status, count });
const ids = (cues: SoundCue[]) => cues.map((c) => (c.repeat ? `${c.signal}*` : c.signal));

describe("sound table", () => {
  it("has the 15 rows Ruslan chose, in priority order, Airbus first", () => {
    expect(SOUND_TABLE.map((e) => `${e.id}:${e.level}:${e.file}`)).toEqual([
      "daemon:warning:airbus_master_warning_crc",
      "llm:warning:airbus_master_warning_crc",
      "site:warning:boeing_fire_bell_737",
      "stall:warning:airbus_stall_cricket",
      "budget:warning:gpws_pull_up_x3",
      "oom:caution:overspeed_clacker",
      "autopilot:caution:airbus_ap_disconnect_cavalry",
      "failed_burst:caution:boeing_stick_shaker",
      "duplicate_run:caution:tcas_traffic_traffic",
      "llm_slow:caution:gpws_sink_rate",
      "master_caution:caution:airbus_master_caution_chime",
      "minimums:caution:gpws_minimums",
      "cleared:info:tcas_clear",
      "issue_done:info:airbus_cabin_chime",
      "epic_closed:info:airbus_altitude_c_chord",
    ]);
  });
});

describe("isQuietHours", () => {
  it("is 23:00–08:00 Moscow time", () => {
    expect(isQuietHours(Date.UTC(2026, 9, 14, 19, 59))).toBe(false);
    expect(isQuietHours(Date.UTC(2026, 9, 14, 20, 0))).toBe(true);
    expect(isQuietHours(Date.UTC(2026, 9, 15, 4, 59))).toBe(true);
    expect(isQuietHours(Date.UTC(2026, 9, 15, 5, 0))).toBe(false);
  });
});

describe("AlertEngine", () => {
  it("plays one info sound for a batch of completions", () => {
    const engine = new AlertEngine();
    expect(ids(engine.push([done, { ...done, issueId: "d-2" }, { type: "epic_closed", epicId: "e", title: "E" }], NOON))).toEqual(["issue_done"]);
    expect(engine.plates()).toEqual([]);
  });

  it("sounds a caution at most once per 2 minutes and groups the rest", () => {
    const engine = new AlertEngine();
    expect(ids(engine.push([failed(1)], NOON))).toEqual(["master_caution"]);
    expect(ids(engine.push([failed(2), failed(3)], NOON + 30_000))).toEqual([]);
    expect(ids(engine.syncHealth([sig("oom", "warning")], NOON + 60_000))).toEqual([]);
    expect(ids(engine.tick(NOON + 2 * MIN - 1))).toEqual([]);
    // One sound for the group, the most important of it.
    expect(ids(engine.tick(NOON + 2 * MIN))).toEqual(["oom"]);
    expect(ids(engine.tick(NOON + 5 * MIN))).toEqual([]);
    expect(engine.plates().map((p) => [p.id, p.count])).toEqual([
      ["caution:oom", 1],
      ["caution:run_failed", 3],
    ]);
  });

  it("keeps quiet at night except for warnings", () => {
    const engine = new AlertEngine();
    expect(ids(engine.push([done, failed(1)], NIGHT))).toEqual([]);
    expect(ids(engine.syncHealth([sig("oom", "warning")], NIGHT))).toEqual([]);
    expect(ids(engine.syncHealth([sig("site", "warning")], NIGHT + 1000))).toEqual(["site"]);
    // The recovery chime is info: silent at night.
    expect(ids(engine.syncHealth([sig("site", "ok")], NIGHT + 2000))).toEqual([]);
  });

  it("repeats only the top warning until acknowledged, then clears on ok", () => {
    const engine = new AlertEngine();
    expect(ids(engine.syncHealth([sig("llm", "warning")], NOON))).toEqual(["llm"]);
    expect(ids(engine.syncHealth([sig("llm", "warning")], NOON + 1000))).toEqual([]);
    // A higher warning takes over the speaker.
    expect(ids(engine.syncHealth([sig("daemon", "warning")], NOON + 2000))).toEqual(["daemon"]);
    expect(ids(engine.tick(NOON + 9000))).toEqual([]);
    expect(ids(engine.tick(NOON + 10_000))).toEqual(["daemon*"]);
    // Anything lower stays silent while it is unacknowledged.
    expect(ids(engine.push([done], NOON + 11_000))).toEqual([]);
    expect(ids(engine.syncHealth([sig("site", "warning")], NOON + 12_000))).toEqual([]);
    engine.acknowledge();
    expect(ids(engine.tick(NOON + 60_000))).toEqual([]);
    expect(engine.plates().map((p) => `${p.id}${p.flashing ? "!" : ""}`)).toEqual(["warning:daemon", "warning:llm", "warning:site"]);
    // "unknown" is not a recovery.
    expect(ids(engine.syncHealth([sig("daemon", "unknown")], NOON + 61_000))).toEqual([]);
    expect(engine.plates()).toHaveLength(3);
    expect(ids(engine.syncHealth([sig("daemon", "ok")], NOON + 62_000))).toEqual(["cleared"]);
    expect(engine.plates().map((p) => p.id)).toEqual(["warning:llm", "warning:site"]);
  });

  it("treats a signal missing from the response as unknown, not as a recovery", () => {
    const engine = new AlertEngine();
    engine.syncHealth([sig("daemon", "warning")], NOON);
    engine.acknowledge();
    // A malformed response parses to no signals; another drops the key.
    expect(ids(engine.syncHealth([], NOON + 30_000))).toEqual([]);
    expect(ids(engine.syncHealth([sig("site", "ok")], NOON + 60_000))).toEqual([]);
    expect(engine.plates().map((p) => `${p.id}${p.flashing ? "!" : ""}`)).toEqual(["warning:daemon"]);
    // When the signal comes back still raised, it neither re-sounds nor flashes.
    expect(ids(engine.syncHealth([sig("daemon", "warning")], NOON + 90_000))).toEqual([]);
    expect(engine.hasUnacknowledged()).toBe(false);
  });

  it("a new warning after an acknowledgement flashes and repeats again", () => {
    const engine = new AlertEngine();
    engine.syncHealth([sig("site", "warning")], NOON);
    engine.acknowledge();
    expect(ids(engine.syncHealth([sig("stall", "warning")], NOON + 1000))).toEqual(["stall"]);
    expect(engine.hasUnacknowledged()).toBe(true);
    expect(ids(engine.tick(NOON + 9000))).toEqual(["stall*"]);
  });

  it("removes a caution signal's plate silently when it recovers", () => {
    const engine = new AlertEngine();
    expect(ids(engine.syncHealth([sig("duplicate_run", "warning")], NOON))).toEqual(["duplicate_run"]);
    expect(ids(engine.syncHealth([sig("duplicate_run", "ok")], NOON + 1000))).toEqual([]);
    expect(engine.plates()).toEqual([]);
  });

  it("mutes everything for an hour, then sounds again", () => {
    const engine = new AlertEngine();
    engine.muteFor(60 * MIN, NOON);
    expect(ids(engine.syncHealth([sig("budget", "warning")], NOON + MIN))).toEqual([]);
    expect(engine.mutedUntilAt(NOON + MIN)).toBe(NOON + 60 * MIN);
    expect(ids(engine.tick(NOON + 61 * MIN))).toEqual(["budget*"]);
  });

  it("drops event caution plates after 10 minutes without updates", () => {
    const engine = new AlertEngine();
    engine.push([failed(1)], NOON);
    engine.tick(NOON + 9 * MIN);
    expect(engine.plates()).toHaveLength(1);
    engine.tick(NOON + 10 * MIN);
    expect(engine.plates()).toEqual([]);
  });
});
