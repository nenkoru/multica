import type { SoundCue } from "./alerts";
import { SOUND_FILES, soundEntry } from "./sound-table";

// Plays the sound table's mp3 files through Web Audio. Mobile browsers allow
// audio only after a gesture: unlock() must run inside the first tap, which
// also fetches and decodes every file once, so later alarms start at once.
// mp3, not Ogg: iPhone Safari does not play Ogg. Safari's audio session is
// set to "playback" where supported, so the ringer switch does not mute an
// alarm the owner asked for.

export interface SoundPlayer {
  readonly unlocked: boolean;
  /** Must run inside a user gesture. Resolves to whether audio is allowed. */
  unlock(): Promise<boolean>;
  play(cue: SoundCue): void;
  /** Silences anything playing (the acknowledge button). */
  stop(): void;
}

type AudioContextCtor = typeof AudioContext;

function audioContextCtor(): AudioContextCtor | null {
  if (typeof window === "undefined") return null;
  const w = window as unknown as { AudioContext?: AudioContextCtor; webkitAudioContext?: AudioContextCtor };
  return w.AudioContext ?? w.webkitAudioContext ?? null;
}

export interface FilePlayerOptions {
  /** URL of a table file, given its name without extension. */
  resolve: (file: string) => string;
  /** Sent with the fetch; the kiosk cookie authenticates /api/tv/sounds. */
  credentials?: RequestCredentials;
  volume?: number;
}

export function tvSoundUrl(apiBaseUrl: string): (file: string) => string {
  const base = apiBaseUrl.replace(/\/+$/, "");
  return (file) => `${base}/api/tv/sounds/${file}.mp3`;
}

export function createFilePlayer(options: FilePlayerOptions): SoundPlayer {
  let ctx: AudioContext | null = null;
  let gain: GainNode | null = null;
  const buffers = new Map<string, AudioBuffer>();
  let current: AudioBufferSourceNode | null = null;
  let loading: Promise<void> | null = null;

  const load = (c: AudioContext) =>
    Promise.all(
      SOUND_FILES.map(async (file) => {
        try {
          const res = await fetch(options.resolve(file), { credentials: options.credentials ?? "include" });
          if (!res.ok) return;
          buffers.set(file, await c.decodeAudioData(await res.arrayBuffer()));
        } catch {
          // A missing file only silences its signal; the plate still shows.
        }
      }),
    ).then(() => undefined);

  return {
    get unlocked() {
      return ctx?.state === "running";
    },
    async unlock() {
      const Ctor = audioContextCtor();
      if (!Ctor) return false;
      const session = (navigator as unknown as { audioSession?: { type: string } }).audioSession;
      if (session) session.type = "playback";
      if (!ctx) {
        ctx = new Ctor();
        gain = ctx.createGain();
        gain.gain.value = options.volume ?? 0.9;
        gain.connect(ctx.destination);
      }
      try {
        await ctx.resume();
      } catch {
        return false;
      }
      loading ??= load(ctx);
      await loading;
      return ctx.state === "running";
    },
    play(cue) {
      if (!ctx || !gain || ctx.state !== "running") return;
      const buffer = buffers.get(soundEntry(cue.signal).file);
      if (!buffer) return;
      // One sound at a time: a new cue cuts the previous one, as the engine
      // only emits the most important cue.
      current?.stop();
      const source = ctx.createBufferSource();
      source.buffer = buffer;
      source.connect(gain);
      source.start();
      current = source;
      source.onended = () => {
        if (current === source) current = null;
      };
    },
    stop() {
      try {
        current?.stop();
      } catch {
        // Already stopped.
      }
      current = null;
    },
  };
}
