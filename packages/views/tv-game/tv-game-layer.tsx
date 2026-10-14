import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { Moon, OctagonAlert, TriangleAlert, Volume2, VolumeX } from "lucide-react";
import { useT } from "../i18n";
import type { AlertPlate } from "./alerts";
import { TvGameController, type TvGameControllerOptions } from "./controller";
import { GameMap } from "./game-map";
import { createWebAudioPlayer } from "./sound";

const MAX_PLATES = 3;

/**
 * Owns one controller for the lifetime of the screen. Feed it with
 * `controller.update(snapshot)` on every aggregate refresh and
 * `controller.pushEvents([...])` from the realtime stream.
 */
export function useTvGameController(options: Partial<TvGameControllerOptions> = {}): TvGameController {
  const ref = useRef<TvGameController | null>(null);
  if (!ref.current) {
    ref.current = new TvGameController({ ...options, player: options.player ?? createWebAudioPlayer() });
  }
  const controller = ref.current;
  useEffect(() => {
    const timer = setInterval(() => controller.tick(), 1000);
    return () => clearInterval(timer);
  }, [controller]);
  return controller;
}

function formatMsk(at: number): string {
  return new Intl.DateTimeFormat("ru-RU", { hour: "2-digit", minute: "2-digit", timeZone: "Europe/Moscow" }).format(at);
}

function Plate({ plate, onAcknowledge }: { plate: AlertPlate; onAcknowledge: () => void }) {
  const { t } = useT("tv-game");
  const warning = plate.level === "warning";
  const Icon = warning ? OctagonAlert : TriangleAlert;
  return (
    <button
      type="button"
      onClick={onAcknowledge}
      aria-label={`${warning ? t(($) => $.level_warning) : t(($) => $.level_caution)}: ${plate.text}`}
      className={[
        "flex items-center gap-[1vh] rounded-md border-[0.4vh] px-[1.6vh] py-[0.8vh] text-left font-semibold text-[2.6vh] leading-tight",
        warning ? "border-destructive bg-destructive/25 text-white" : "border-warning bg-warning/20 text-white",
        plate.flashing ? "animate-pulse motion-reduce:animate-none" : "opacity-80",
      ].join(" ")}
    >
      <Icon aria-hidden className={warning ? "size-[3.2vh] shrink-0 text-destructive" : "size-[3.2vh] shrink-0 text-warning"} />
      <span className="uppercase tracking-wide text-[1.8vh] opacity-80">
        {warning ? t(($) => $.level_warning) : t(($) => $.level_caution)}
      </span>
      <span className="truncate">{plate.text}</span>
    </button>
  );
}

export interface TvGameLayerProps {
  controller: TvGameController;
  /** Optional slot rendered at the right of the top bar (demo controls). */
  extra?: ReactNode;
}

/**
 * The map strip of the TV screen: Supply and Done counters, the animated
 * base-and-workers map, and the alarm plates. Fills its container; the base
 * screen gives it 35–40 % of the height.
 */
export function TvGameLayer({ controller, extra }: TvGameLayerProps) {
  const { t } = useT("tv-game");
  const view = useSyncExternalStore(controller.subscribe, controller.getView, controller.getView);
  const [unlocking, setUnlocking] = useState(false);

  // Any key of the remote acknowledges, like the master caution button; the
  // first one also unlocks audio, which TV browsers allow only after a gesture.
  useEffect(() => {
    const onKey = () => {
      if (!controller.getView().soundUnlocked) void controller.unlockSound();
      if (controller.getView().plates.some((p) => p.flashing)) controller.acknowledge();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [controller]);

  const snapshot = view.snapshot;
  const plates = view.plates.slice(0, MAX_PLATES);
  const hiddenPlates = view.plates.length - plates.length;

  return (
    <section className="relative flex h-full w-full flex-col overflow-hidden rounded-lg bg-[#15191f] text-white">
      <div className="flex items-center gap-[3vh] px-[2vh] py-[1vh]">
        <div className="flex items-baseline gap-[1vh]">
          <span className="text-[2vh] uppercase tracking-wide text-white/60">{t(($) => $.supply)}</span>
          <span className="font-bold tabular-nums text-[4.5vh] leading-none">
            {snapshot ? `${snapshot.runningRuns}/${snapshot.hostCeiling}` : "–"}
          </span>
        </div>
        <div className="flex items-baseline gap-[1vh]">
          <span className="text-[2vh] uppercase tracking-wide text-white/60">{t(($) => $.resources)}</span>
          <span className="font-bold tabular-nums text-[4.5vh] leading-none">{snapshot ? snapshot.doneInSprint : "–"}</span>
        </div>
        <div className="ml-auto flex items-center gap-[1.5vh] text-[2vh]">
          {view.quietHours && (
            <span className="flex items-center gap-[0.6vh] text-white/70">
              <Moon aria-hidden className="size-[2.4vh]" />
              {t(($) => $.quiet_hours)}
            </span>
          )}
          {!view.soundUnlocked ? (
            <button
              type="button"
              disabled={unlocking}
              onClick={() => {
                setUnlocking(true);
                void controller.unlockSound().finally(() => setUnlocking(false));
              }}
              className="flex items-center gap-[0.6vh] rounded-md bg-white/15 px-[1.4vh] py-[0.6vh] font-semibold hover:bg-white/25"
            >
              <Volume2 aria-hidden className="size-[2.4vh]" />
              {t(($) => $.enable_sound)}
            </button>
          ) : (
            <button
              type="button"
              onClick={() => controller.toggleMute()}
              className="flex items-center gap-[0.6vh] rounded-md bg-white/10 px-[1.4vh] py-[0.6vh] hover:bg-white/20"
            >
              {view.mutedUntil ? <VolumeX aria-hidden className="size-[2.4vh]" /> : <Volume2 aria-hidden className="size-[2.4vh]" />}
              {view.mutedUntil
                ? `${t(($) => $.muted_until, { time: formatMsk(view.mutedUntil) })} · ${t(($) => $.unmute)}`
                : t(($) => $.mute_hour)}
            </button>
          )}
          {extra}
        </div>
      </div>

      <div className="relative min-h-0 flex-1">
        <GameMap
          view={view}
          now={controller.now}
          noEpicLabel={t(($) => $.no_epic)}
          ariaLabel={t(($) => $.map_label, {
            running: snapshot?.runningRuns ?? 0,
            done: snapshot?.doneInSprint ?? 0,
          })}
        />
        {plates.length > 0 && (
          <div className="absolute right-[2vh] top-[1vh] flex max-w-[29%] flex-col items-end gap-[0.8vh]">
            {plates.map((plate) => (
              <Plate key={plate.id} plate={plate} onAcknowledge={() => controller.acknowledge()} />
            ))}
            {hiddenPlates > 0 && (
              <span className="text-[1.8vh] text-white/70">{t(($) => $.more_plates, { count: hiddenPlates })}</span>
            )}
          </div>
        )}
      </div>
    </section>
  );
}
