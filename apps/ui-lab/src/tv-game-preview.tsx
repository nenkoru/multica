import { TvOrbitDemo, createFilePlayer, type SoundPlayer } from "@multica/views/tv-game";

// Standalone Orbit demo (`?tv-game`), no backend: the same script the TV
// screen plays with `?demo=1`. The alarm sounds are not in the repository;
// put the files in apps/ui-lab/public/tv-sounds/ to hear them here.
// Played cues are kept in window.__tvCues so a recording can add the sound.
const files = createFilePlayer({ resolve: (file) => `/tv-sounds/${file}.mp3`, credentials: "same-origin" });
const log = window as unknown as { __tvCues?: { at: number; signal: string }[]; __tvStep?: string };
log.__tvCues = [];
const player: SoundPlayer = {
  get unlocked() {
    return files.unlocked;
  },
  unlock: () => files.unlock(),
  play(cue) {
    log.__tvCues?.push({ at: Date.now(), signal: cue.signal });
    files.play(cue);
  },
  stop: () => files.stop(),
};

export function TvGamePreview() {
  return (
    <div className="dark fixed inset-0 bg-black">
      <TvOrbitDemo player={player} onStep={(id) => (log.__tvStep = id)} />
    </div>
  );
}
