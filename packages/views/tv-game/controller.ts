import { AlertEngine, isQuietHours, type AlertEngineOptions, type AlertPlate, type SoundCue } from "./alerts";
import { deriveEvents, type TvGameEvent, type TvGameSnapshot } from "./model";
import type { SceneWreckInput } from "./scene";
import type { SoundPlayer } from "./sound";

// Glue between the data the base TV screen already has and the game layer:
// snapshots and pushed events in, cues to the speaker and view state out.
// It is a plain object held in a ref by the component, not a store — nothing
// here is server state, and nothing outlives the screen.

export const WRECK_LIFETIME_MS = 60_000;
export const POPUP_LIFETIME_MS = 2_500;
export const MUTE_MS = 60 * 60_000;

export interface TvGamePopup {
  id: string;
  epicId: string | null;
  at: number;
}

export interface TvGameView {
  snapshot: TvGameSnapshot | null;
  plates: AlertPlate[];
  wrecks: SceneWreckInput[];
  popups: TvGamePopup[];
  mutedUntil: number | null;
  quietHours: boolean;
  soundUnlocked: boolean;
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
  private wrecks: SceneWreckInput[] = [];
  private popups: TvGamePopup[] = [];
  private listeners = new Set<() => void>();
  private view: TvGameView;
  /** Every cue the controller decided to play, for the demo and tests. */
  readonly played: { at: number; cue: SoundCue }[] = [];

  constructor(options: TvGameControllerOptions) {
    this.engine = new AlertEngine(options);
    this.player = options.player;
    this.now = options.now ?? Date.now;
    this.view = this.buildView(0);
  }

  /** A fresh snapshot from the base screen's aggregate. */
  update(snapshot: TvGameSnapshot): void {
    const now = this.now();
    const events = deriveEvents(this.snapshot, snapshot);
    this.snapshot = snapshot;
    this.trackEvents(events, now);
    this.play([...this.engine.push(events, now), ...this.engine.syncHealth(snapshot.health, now)], now);
    this.changed();
  }

  /** Events the snapshot cannot carry, such as a run that just failed. */
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
    const before = this.wrecks.length + this.popups.length;
    this.wrecks = this.wrecks.filter((w) => now - w.at < WRECK_LIFETIME_MS);
    this.popups = this.popups.filter((p) => now - p.at < POPUP_LIFETIME_MS);
    const platesBefore = this.engine.plates().length;
    const cues = this.engine.tick(now);
    this.play(cues, now);
    const quiet = isQuietHours(now);
    if (
      cues.length > 0 ||
      before !== this.wrecks.length + this.popups.length ||
      platesBefore !== this.engine.plates().length ||
      quiet !== this.view.quietHours ||
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
      if (event.type === "run_failed") {
        this.wrecks = [
          ...this.wrecks.filter((w) => w.issueId !== event.issueId),
          { issueId: event.issueId, title: event.title, epicId: event.epicId, agentId: event.agentId, at: now },
        ];
      } else if (event.type === "issue_done") {
        this.popups = [...this.popups, { id: `${event.issueId}:${now}`, epicId: event.epicId, at: now }];
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
      wrecks: this.wrecks,
      popups: this.popups,
      mutedUntil: this.engine.mutedUntilAt(now),
      quietHours: isQuietHours(now),
      soundUnlocked: this.player.unlocked,
      version,
    };
  }

  private changed(): void {
    this.view = this.buildView(this.view.version + 1);
    for (const listener of this.listeners) listener();
  }
}

/** A player that records nothing and plays nothing (tests, SSR). */
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
