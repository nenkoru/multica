import type { TvGameEvent, TvHealthKey, TvHealthSignal } from "./model";
import {
  CAUTION_PREFIX,
  DEFAULT_VOICE_LABELS,
  WARNING_PREFIX,
  cautionGroupOf,
  cautionPhrase,
  warningPhrase,
  type CautionGroup,
  type VoiceLabels,
} from "./voice";

// Cockpit alerting for the TV screen (RUSL-1786). Three tiers, like advisory /
// caution / warning:
//   info    — one soft ding: an issue or an epic is done.
//   caution — double gong and a voice line, once: a run failed, a new "needs
//             you" issue, an issue stuck for a day, agents idle over a queue.
//   warning — master warning repeating until acknowledged: infrastructure
//             health from /api/tv/health.
// Only a change sounds, never a steady state. The engine is pure: it gets the
// clock from the caller, returns the cues to play, and keeps the plates the
// screen shows. Playing a cue is sound.ts's job.

export type AlertLevel = "info" | "caution" | "warning";

export type SoundCue =
  | { kind: "ding" }
  | { kind: "caution"; voice: string }
  | { kind: "warning"; voice: string | null };

export interface AlertPlate {
  id: string;
  level: "caution" | "warning";
  text: string;
  count: number;
  /** Flashing until acknowledged. */
  flashing: boolean;
  since: number;
  updatedAt: number;
}

export interface AlertEngineOptions {
  /** At most one caution sound per this interval; the rest are grouped. */
  cautionCooldownMs?: number;
  /** Master warning chime repeats this often until acknowledged. */
  warningRepeatMs?: number;
  /** The voice line of an unacknowledged warning repeats this often. */
  warningVoiceRepeatMs?: number;
  /** A caution plate leaves the screen after this long without updates. */
  cautionPlateMs?: number;
  labels?: VoiceLabels;
}

const MINUTE = 60_000;
const MSK_OFFSET_MS = 3 * 60 * MINUTE;

/** 23:00–08:00 Moscow time: only warnings make a sound. */
export function isQuietHours(now: number): boolean {
  const hour = new Date(now + MSK_OFFSET_MS).getUTCHours();
  return hour >= 23 || hour < 8;
}

interface PendingCaution {
  count: number;
  last: TvGameEvent;
}

interface ActiveWarning {
  key: TvHealthKey;
  since: number;
  acked: boolean;
  lastChimeAt: number;
  lastVoiceAt: number;
}

export class AlertEngine {
  private readonly cautionCooldownMs: number;
  private readonly warningRepeatMs: number;
  private readonly warningVoiceRepeatMs: number;
  private readonly cautionPlateMs: number;
  private readonly labels: VoiceLabels;

  private lastCautionSoundAt = Number.NEGATIVE_INFINITY;
  private pending = new Map<CautionGroup, PendingCaution>();
  private cautionPlates = new Map<CautionGroup, AlertPlate>();
  private warnings = new Map<TvHealthKey, ActiveWarning>();
  private mutedUntil = 0;

  constructor(options: AlertEngineOptions = {}) {
    this.cautionCooldownMs = options.cautionCooldownMs ?? 2 * MINUTE;
    this.warningRepeatMs = options.warningRepeatMs ?? 8_000;
    this.warningVoiceRepeatMs = options.warningVoiceRepeatMs ?? MINUTE;
    this.cautionPlateMs = options.cautionPlateMs ?? 10 * MINUTE;
    this.labels = options.labels ?? DEFAULT_VOICE_LABELS;
  }

  /** Feeds info and caution events. Returns the cues to play now. */
  push(events: TvGameEvent[], now: number): SoundCue[] {
    const cues: SoundCue[] = [];
    let ding = false;
    for (const event of events) {
      const group = cautionGroupOf(event);
      if (!group) {
        ding = true;
        continue;
      }
      const pending = this.pending.get(group);
      this.pending.set(group, { count: (pending?.count ?? 0) + 1, last: event });
      const plate = this.cautionPlates.get(group);
      const count = plate && plate.flashing ? plate.count + 1 : 1;
      this.cautionPlates.set(group, {
        id: `caution:${group}`,
        level: "caution",
        text: cautionPhrase(group, count, event),
        count,
        flashing: true,
        since: plate && plate.flashing ? plate.since : now,
        updatedAt: now,
      });
    }
    if (ding) cues.push({ kind: "ding" });
    cues.push(...this.flushCautions(now));
    return this.audible(cues, now);
  }

  /** Reads the current infrastructure health. Returns the cues to play now. */
  syncHealth(signals: TvHealthSignal[], now: number): SoundCue[] {
    const cues: SoundCue[] = [];
    const warningKeys = new Set(signals.filter((s) => s.status === "warning").map((s) => s.key));
    for (const key of warningKeys) {
      if (this.warnings.has(key)) continue;
      this.warnings.set(key, { key, since: now, acked: false, lastChimeAt: now, lastVoiceAt: now });
      cues.push({ kind: "warning", voice: WARNING_PREFIX + warningPhrase(key, this.labels) });
    }
    for (const key of [...this.warnings.keys()]) {
      if (warningKeys.has(key)) continue;
      // "unknown" is not a recovery: only an explicit "ok" clears a warning,
      // so a probe that goes quiet does not silence a real outage.
      const signal = signals.find((s) => s.key === key);
      if (signal && signal.status === "unknown") continue;
      this.warnings.delete(key);
      cues.push({ kind: "ding" });
    }
    return this.audible(cues, now);
  }

  /** Advances timers: grouped cautions and warning repeats. */
  tick(now: number): SoundCue[] {
    const cues = this.flushCautions(now);
    for (const [group, plate] of this.cautionPlates) {
      if (now - plate.updatedAt >= this.cautionPlateMs) this.cautionPlates.delete(group);
    }
    for (const warning of this.warnings.values()) {
      if (warning.acked || now - warning.lastChimeAt < this.warningRepeatMs) continue;
      warning.lastChimeAt = now;
      let voice: string | null = null;
      if (now - warning.lastVoiceAt >= this.warningVoiceRepeatMs) {
        warning.lastVoiceAt = now;
        voice = WARNING_PREFIX + warningPhrase(warning.key, this.labels);
      }
      cues.push({ kind: "warning", voice });
    }
    return this.audible(cues, now);
  }

  /**
   * Master caution button: silences what is sounding and stops the flashing.
   * Warning plates stay until the signal recovers.
   */
  acknowledge(): void {
    for (const warning of this.warnings.values()) warning.acked = true;
    for (const plate of this.cautionPlates.values()) plate.flashing = false;
  }

  muteFor(ms: number, now: number): void {
    this.mutedUntil = now + ms;
  }

  unmute(): void {
    this.mutedUntil = 0;
  }

  mutedUntilAt(now: number): number | null {
    return this.mutedUntil > now ? this.mutedUntil : null;
  }

  /** Plates to show, warnings first, newest first inside a tier. */
  plates(): AlertPlate[] {
    const warnings: AlertPlate[] = [...this.warnings.values()].map((w) => ({
      id: `warning:${w.key}`,
      level: "warning",
      text: warningPhrase(w.key, this.labels),
      count: 1,
      flashing: !w.acked,
      since: w.since,
      updatedAt: w.since,
    }));
    const cautions = [...this.cautionPlates.values()];
    const newest = (a: AlertPlate, b: AlertPlate) => b.updatedAt - a.updatedAt;
    return [...warnings.sort(newest), ...cautions.sort(newest)];
  }

  hasUnacknowledged(): boolean {
    return this.plates().some((p) => p.flashing);
  }

  private flushCautions(now: number): SoundCue[] {
    if (this.pending.size === 0 || now - this.lastCautionSoundAt < this.cautionCooldownMs) return [];
    const parts = [...this.pending].map(([group, p]) => cautionPhrase(group, p.count, p.last));
    this.pending.clear();
    this.lastCautionSoundAt = now;
    return [{ kind: "caution", voice: CAUTION_PREFIX + parts.join(". ") }];
  }

  private audible(cues: SoundCue[], now: number): SoundCue[] {
    if (this.mutedUntil > now) return [];
    if (!isQuietHours(now)) return cues;
    return cues.filter((cue) => cue.kind === "warning");
  }
}
