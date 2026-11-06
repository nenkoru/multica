import { AlertEngine, isQuietHours, type AlertEngineOptions, type AlertPlate, type SoundCue } from "./alerts";
import { deriveEvents, type TvGameEvent, type TvGameSnapshot } from "./model";
import type { SoundPlayer } from "./sound";
import { priorityOf } from "./sound-table";

// Glue between the screen's data and the alarm layer: snapshots and pushed
// events in, cues to the speaker and view state out. A plain object held in a
// ref by the screen, not a store — nothing here is server state, and nothing
// outlives the screen.

export const SPARK_LIFETIME_MS = 1_600;
export const MUTE_MS = 60 * 60_000;

/** A finished issue flying from its epic's planet into the orb. */
export interface TvGameSpark {
  id: string;
  epicId: string | null;
  at: number;
}

export type OrbMood = "calm" | "call" | "alarm" | "talk";

export interface TvGameView {
  snapshot: TvGameSnapshot | null;
  plates: AlertPlate[];
  sparks: TvGameSpark[];
  mutedUntil: number | null;
  quietHours: boolean;
  soundUnlocked: boolean;
  /** A voice conversation is going (driven by the voice feature). */
  talking: boolean;
  /** Bumped on every change; lets React skip identical renders. */
  version: number;
}

export interface TvGameControllerOptions extends AlertEngineOptions {
  player: SoundPlayer;
  now?: () => number;
}

export class TvGameController {
  private readonly engine: AlertEngine;
  private readonly player: SoundPlayer;
  readonly now: () => number;
  private snapshot: TvGameSnapshot | null = null;
  private sparks: TvGameSpark[] = [];
  private talking = false;
  private listeners = new Set<() => void>();
  private view: TvGameView;
  /** The last cues the controller played, for the demo and tests. */
  readonly played: { at: number; cue: SoundCue }[] = [];

  constructor(options: TvGameControllerOptions) {
    this.engine = new AlertEngine(options);
    this.player = options.player;
    this.now = options.now ?? Date.now;
    this.view = this.buildView(0);
  }

  /** A fresh snapshot from the screen's aggregates. */
  update(snapshot: TvGameSnapshot): void {
    const now = this.now();
    const events = deriveEvents(this.snapshot, snapshot);
    this.snapshot = snapshot;
    this.trackEvents(events, now);
    // Health first: a raised signal joins the pending cautions before they
    // are flushed, so the grouped sound is the most important of them.
    const health = this.engine.syncHealth(snapshot.health, now);
    this.play(pickOne([...health, ...this.engine.push(events, now)]), now);
    this.changed();
  }

  /** Events a snapshot cannot carry, such as a run that just failed. */
  pushEvents(events: TvGameEvent[]): void {
    if (events.length === 0) return;
    const now = this.now();
    this.trackEvents(events, now);
    this.play(this.engine.push(events, now), now);
    this.changed();
  }

  /** Call about once a second. */
  tick(): void {
    const now = this.now();
    const sparks = this.sparks.length;
    this.sparks = this.sparks.filter((s) => now - s.at < SPARK_LIFETIME_MS);
    const plates = this.engine.plates().length;
    const cues = this.engine.tick(now);
    this.play(cues, now);
    if (
      cues.length > 0 ||
      sparks !== this.sparks.length ||
      plates !== this.engine.plates().length ||
      isQuietHours(now) !== this.view.quietHours ||
      (this.view.mutedUntil !== null && this.engine.mutedUntilAt(now) === null)
    ) {
      this.changed();
    }
  }

  acknowledge(): void {
    this.engine.acknowledge();
    this.player.stop();
    this.changed();
  }

  toggleMute(): void {
    const now = this.now();
    if (this.engine.mutedUntilAt(now)) this.engine.unmute();
    else {
      this.engine.muteFor(MUTE_MS, now);
      this.player.stop();
    }
    this.changed();
  }

  setTalking(talking: boolean): void {
    if (this.talking === talking) return;
    this.talking = talking;
    this.changed();
  }

  async unlockSound(): Promise<boolean> {
    const ok = await this.player.unlock();
    this.changed();
    return ok;
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getView = (): TvGameView => this.view;

  private trackEvents(events: TvGameEvent[], now: number): void {
    for (const event of events) {
      if (event.type === "issue_done") {
        this.sparks = [...this.sparks, { id: `${event.issueId}:${now}`, epicId: event.epicId, at: now }];
      }
    }
  }

  private play(cues: SoundCue[], now: number): void {
    for (const cue of cues) {
      this.played.push({ at: now, cue });
      if (this.played.length > 200) this.played.shift();
      this.player.play(cue);
    }
  }

  private buildView(version: number): TvGameView {
    const now = this.now();
    return {
      snapshot: this.snapshot,
      plates: this.engine.plates(),
      sparks: this.sparks,
      mutedUntil: this.engine.mutedUntilAt(now),
      quietHours: isQuietHours(now),
      soundUnlocked: this.player.unlocked,
      talking: this.talking,
      version,
    };
  }

  private changed(): void {
    this.view = this.buildView(this.view.version + 1);
    for (const listener of this.listeners) listener();
  }
}

/** Two engine calls in one update still give the speaker a single cue. */
function pickOne(cues: SoundCue[]): SoundCue[] {
  if (cues.length <= 1) return cues;
  return [cues.reduce((a, b) => (priorityOf(b.signal) < priorityOf(a.signal) ? b : a))];
}

/** The orb's state: talk while talking, red on a warning, amber when Ruslan is needed. */
export function orbMood(view: TvGameView, needsYou: number): OrbMood {
  if (view.talking) return "talk";
  if (view.plates.some((p) => p.level === "warning")) return "alarm";
  if (needsYou > 0 || view.plates.some((p) => p.flashing)) return "call";
  return "calm";
}

/** A player that plays nothing (tests, SSR). */
export function silentPlayer(): SoundPlayer {
  let unlocked = false;
  return {
    get unlocked() {
      return unlocked;
    },
    async unlock() {
      unlocked = true;
      return true;
    },
    play() {},
    stop() {},
  };
}
