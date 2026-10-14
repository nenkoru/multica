import type { SoundCue } from "./alerts";

// Every sound is synthesized here with the Web Audio API: no recorded samples,
// so there is nothing to license (RUSL-1786 forbids game and real-cockpit
// recordings). Voice lines use the browser's speech synthesis in Russian.
//
// TV browsers block audio until the first user gesture; call unlock() from the
// "enable sound" button's click handler. Chromium kiosks can skip that with
// --autoplay-policy=no-user-gesture-required.

export interface SoundPlayer {
  readonly unlocked: boolean;
  /** Must run inside a user gesture. Resolves to whether audio is allowed. */
  unlock(): Promise<boolean>;
  play(cue: SoundCue): void;
  /** Silences anything playing or queued (the acknowledge button). */
  stop(): void;
}

type AudioContextCtor = typeof AudioContext;

function audioContextCtor(): AudioContextCtor | null {
  if (typeof window === "undefined") return null;
  const w = window as unknown as { AudioContext?: AudioContextCtor; webkitAudioContext?: AudioContextCtor };
  return w.AudioContext ?? w.webkitAudioContext ?? null;
}

interface Strike {
  freq: number;
  gain: number;
  decay: number;
  type?: OscillatorType;
}

export function createWebAudioPlayer(): SoundPlayer {
  let ctx: AudioContext | null = null;
  let master: GainNode | null = null;
  let live: AudioScheduledSourceNode[] = [];
  let voiceTimer: ReturnType<typeof setTimeout> | null = null;

  const ensure = (): AudioContext | null => {
    if (ctx) return ctx;
    const Ctor = audioContextCtor();
    if (!Ctor) return null;
    ctx = new Ctor();
    master = ctx.createGain();
    master.gain.value = 0.6;
    master.connect(ctx.destination);
    return ctx;
  };

  const strike = (at: number, partials: Strike[], attack = 0.005) => {
    const c = ctx;
    if (!c || !master) return;
    for (const p of partials) {
      const osc = c.createOscillator();
      const env = c.createGain();
      osc.type = p.type ?? "sine";
      osc.frequency.value = p.freq;
      env.gain.setValueAtTime(0.0001, at);
      env.gain.exponentialRampToValueAtTime(p.gain, at + attack);
      env.gain.exponentialRampToValueAtTime(0.0001, at + p.decay);
      osc.connect(env).connect(master);
      osc.start(at);
      osc.stop(at + p.decay + 0.05);
      live.push(osc);
      osc.onended = () => {
        live = live.filter((n) => n !== osc);
      };
    }
  };

  // Advisory: a single soft bell.
  const ding = (t: number) => {
    strike(t, [
      { freq: 1318.5, gain: 0.35, decay: 0.9 },
      { freq: 2637, gain: 0.08, decay: 0.4 },
    ]);
    return 0.9;
  };

  // Caution: two gong strikes, the second a fourth lower. Inharmonic partials
  // make it a gong rather than a beep.
  const gong = (t: number, base: number) =>
    strike(t, [
      { freq: base, gain: 0.45, decay: 1.4 },
      { freq: base * 2.76, gain: 0.12, decay: 0.8 },
      { freq: base * 5.4, gain: 0.05, decay: 0.4 },
    ], 0.01);
  const caution = (t: number) => {
    gong(t, 392);
    gong(t + 0.45, 294);
    return 1.9;
  };

  // Master warning: a fast two-tone triple pulse, softened square waves.
  const warning = (t: number) => {
    for (let i = 0; i < 3; i++) {
      const at = t + i * 0.36;
      strike(at, [{ freq: 988, gain: 0.22, decay: 0.15, type: "square" }], 0.004);
      strike(at + 0.17, [{ freq: 740, gain: 0.22, decay: 0.15, type: "square" }], 0.004);
    }
    return 1.1;
  };

  const speak = (text: string, afterSec: number) => {
    if (typeof window === "undefined" || !("speechSynthesis" in window)) return;
    if (voiceTimer) clearTimeout(voiceTimer);
    voiceTimer = setTimeout(() => {
      voiceTimer = null;
      const utterance = new SpeechSynthesisUtterance(text);
      utterance.lang = "ru-RU";
      utterance.rate = 1.05;
      const voice = window.speechSynthesis.getVoices().find((v) => v.lang.toLowerCase().startsWith("ru"));
      if (voice) utterance.voice = voice;
      window.speechSynthesis.speak(utterance);
    }, afterSec * 1000);
  };

  return {
    get unlocked() {
      return ctx?.state === "running";
    },
    async unlock() {
      const c = ensure();
      if (!c) return false;
      try {
        await c.resume();
      } catch {
        return false;
      }
      return c.state === "running";
    },
    play(cue) {
      const c = ensure();
      if (!c || c.state !== "running") return;
      const t = c.currentTime + 0.02;
      switch (cue.kind) {
        case "ding":
          ding(t);
          break;
        case "caution":
          speak(cue.voice, caution(t));
          break;
        case "warning": {
          const length = warning(t);
          if (cue.voice) speak(cue.voice, length);
          break;
        }
      }
    },
    stop() {
      for (const node of live) {
        try {
          node.stop();
        } catch {
          // Already stopped.
        }
      }
      live = [];
      if (voiceTimer) clearTimeout(voiceTimer);
      voiceTimer = null;
      if (typeof window !== "undefined" && "speechSynthesis" in window) window.speechSynthesis.cancel();
    },
  };
}
