import type { TvGameEvent, TvHealthKey, TvHealthSignal } from "./model";
import { priorityOf, soundEntry, type AlarmLevel, type SignalId } from "./sound-table";

// Cockpit alerting for the TV screen (RUSL-1786), driven by SOUND_TABLE:
//   warning — repeats until acknowledged; the plate stays until the signal
//             is "ok" again, then "cleared" sounds.
//   caution — sounds once; the plate stays while the signal is raised (or
//             10 minutes for event cautions).
//   info    — sounds once, no plate.
// Only a change sounds, never a steady state. At most one caution per
// 2 minutes; cautions inside the interval are grouped into the next one.
// 23:00–08:00 MSK only warnings sound. One sound at a time: of everything
// that wants the speaker, the signal highest in the table plays, and nothing
// below an unacknowledged warning sounds while it is active.
// The engine is pure: the caller passes the clock and plays the cues.

export interface SoundCue {
  signal: SignalId;
  level: AlarmLevel;
  /** A repeat of a warning that is still unacknowledged. */
  repeat: boolean;
}

/** What a master-caution plate counts. */
export type CautionGroup = "run_failed" | "need_you" | "stuck";

export interface AlertPlate {
  id: string;
  signal: SignalId;
  level: "warning" | "caution";
  group: CautionGroup | null;
  count: number;
  /** Reason code from the server, or the agent of a single failed run. */
  detail: string | null;
  /** Flashing until acknowledged. */
  flashing: boolean;
  since: number;
  updatedAt: number;
}

export interface AlertEngineOptions {
  cautionCooldownMs?: number;
  warningRepeatMs?: number;
  /** An event caution plate leaves after this long without updates. */
  cautionPlateMs?: number;
}

const MINUTE = 60_000;
const MSK_OFFSET_MS = 3 * 60 * MINUTE;

/** 23:00–08:00 Moscow time: only warnings make a sound. */
export function isQuietHours(now: number): boolean {
  const hour = new Date(now + MSK_OFFSET_MS).getUTCHours();
  return hour >= 23 || hour < 8;
}

function cautionGroupOf(event: TvGameEvent): CautionGroup | null {
  return event.type === "run_failed" || event.type === "need_you" || event.type === "stuck" ? event.type : null;
}

interface RaisedSignal {
  plate: AlertPlate;
  acked: boolean;
  lastChimeAt: number;
}

export class AlertEngine {
  private readonly cautionCooldownMs: number;
  private readonly warningRepeatMs: number;
  private readonly cautionPlateMs: number;

  private lastCautionSoundAt = Number.NEGATIVE_INFINITY;
  /** Cautions waiting for the rate limit; one sound covers all of them. */
  private pending = new Set<SignalId>();
  /** Health signals currently raised (warning and caution level). */
  private raised = new Map<TvHealthKey, RaisedSignal>();
  /** Event cautions: failed runs, "needs you", stuck issues. */
  private eventPlates = new Map<CautionGroup, AlertPlate>();
  private mutedUntil = 0;

  constructor(options: AlertEngineOptions = {}) {
    this.cautionCooldownMs = options.cautionCooldownMs ?? 2 * MINUTE;
    this.warningRepeatMs = options.warningRepeatMs ?? 8_000;
    this.cautionPlateMs = options.cautionPlateMs ?? 10 * MINUTE;
  }

  /** Feeds info and caution events. Returns the cue to play now, if any. */
  push(events: TvGameEvent[], now: number): SoundCue[] {
    const cues: SoundCue[] = [];
    for (const event of events) {
      if (event.type === "issue_done") cues.push(this.cue("issue_done"));
      else if (event.type === "epic_closed") cues.push(this.cue("epic_closed"));
      const group = cautionGroupOf(event);
      if (!group) continue;
      this.pending.add("master_caution");
      const plate = this.eventPlates.get(group);
      const fresh = !plate || !plate.flashing;
      const count = fresh ? 1 : plate.count + 1;
      this.eventPlates.set(group, {
        id: `caution:${group}`,
        signal: "master_caution",
        level: "caution",
        group,
        count,
        detail: event.type === "run_failed" && count === 1 ? event.agentName : null,
        flashing: true,
        since: fresh ? now : plate.since,
        updatedAt: now,
      });
    }
    cues.push(...this.flushCautions(now));
    return this.audible(cues, now);
  }

  /** Reads the current health signals. Returns the cue to play now, if any. */
  syncHealth(signals: TvHealthSignal[], now: number): SoundCue[] {
    const cues: SoundCue[] = [];
    for (const signal of signals) {
      const entry = soundEntry(signal.key);
      const current = this.raised.get(signal.key);
      if (signal.status === "warning") {
        if (current) {
          current.plate.count = signal.count;
          current.plate.detail = signal.reason ?? null;
          continue;
        }
        this.raised.set(signal.key, {
          acked: false,
          lastChimeAt: now,
          plate: {
            id: `${entry.level}:${signal.key}`,
            signal: signal.key,
            level: entry.level === "warning" ? "warning" : "caution",
            group: null,
            count: signal.count,
            detail: signal.reason ?? null,
            flashing: true,
            since: now,
            updatedAt: now,
          },
        });
        if (entry.level === "warning") cues.push(this.cue(signal.key));
        else this.pending.add(signal.key);
      } else if (signal.status === "ok" && current) {
        // "unknown" is not a recovery: only an explicit "ok" clears a signal,
        // so a probe that goes quiet does not silence a real outage.
        this.raised.delete(signal.key);
        this.pending.delete(signal.key);
        if (entry.level === "warning") cues.push(this.cue("cleared"));
      }
    }
    cues.push(...this.flushCautions(now));
    return this.audible(cues, now);
  }

  /** Advances timers: grouped cautions and the warning repeat. */
  tick(now: number): SoundCue[] {
    const cues = this.flushCautions(now);
    for (const [group, plate] of this.eventPlates) {
      if (now - plate.updatedAt >= this.cautionPlateMs) this.eventPlates.delete(group);
    }
    const top = this.topUnackedWarning();
    if (top && now - top.lastChimeAt >= this.warningRepeatMs) {
      top.lastChimeAt = now;
      cues.push({ ...this.cue(top.plate.signal), repeat: true });
    }
    return this.audible(cues, now);
  }

  /** Master warning/caution button: silence and stop the flashing. */
  acknowledge(): void {
    for (const raised of this.raised.values()) {
      raised.acked = true;
      raised.plate.flashing = false;
    }
    for (const plate of this.eventPlates.values()) plate.flashing = false;
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

  /** Plates in table order, newest first within a signal. */
  plates(): AlertPlate[] {
    const all = [...[...this.raised.values()].map((r) => r.plate), ...this.eventPlates.values()];
    return all
      .map((p) => ({ ...p }))
      .sort((a, b) => priorityOf(a.signal) - priorityOf(b.signal) || b.updatedAt - a.updatedAt);
  }

  hasUnacknowledged(): boolean {
    return this.plates().some((p) => p.flashing);
  }

  private cue(signal: SignalId): SoundCue {
    return { signal, level: soundEntry(signal).level, repeat: false };
  }

  private topUnackedWarning(): RaisedSignal | null {
    let top: RaisedSignal | null = null;
    for (const raised of this.raised.values()) {
      if (raised.acked || raised.plate.level !== "warning") continue;
      if (!top || priorityOf(raised.plate.signal) < priorityOf(top.plate.signal)) top = raised;
    }
    return top;
  }

  private flushCautions(now: number): SoundCue[] {
    if (this.pending.size === 0 || now - this.lastCautionSoundAt < this.cautionCooldownMs) return [];
    // One sound for the whole group: the most important pending caution.
    const top = [...this.pending].sort((a, b) => priorityOf(a) - priorityOf(b))[0]!;
    this.pending.clear();
    this.lastCautionSoundAt = now;
    return [this.cue(top)];
  }

  private audible(cues: SoundCue[], now: number): SoundCue[] {
    if (cues.length === 0 || this.mutedUntil > now) return [];
    const quiet = isQuietHours(now);
    const allowed = cues.filter((cue) => !quiet || cue.level === "warning");
    if (allowed.length === 0) return [];
    const best = allowed.reduce((a, b) => (priorityOf(b.signal) < priorityOf(a.signal) ? b : a));
    // Nothing below an active, unacknowledged warning gets the speaker.
    const blocking = this.topUnackedWarning();
    if (blocking && best.signal !== blocking.plate.signal && priorityOf(best.signal) > priorityOf(blocking.plate.signal)) {
      return [];
    }
    return [best];
  }
}
