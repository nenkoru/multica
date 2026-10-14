"use client";

import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { Focus, Moon, OctagonAlert, TriangleAlert, Volume2, VolumeX } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { sprintTimeline } from "@multica/core/planning";
import { tvGameOptions, tvHealthOptions, type TvOverview } from "@multica/core/tv";
import { TV_COPY, type TvLang } from "../tv/copy";
import { TvScreen } from "../tv/tv-screen";
import { tvDate, tvTime } from "../tv/model";
import { FailedRunTracker, toTvGameSnapshot } from "./adapter";
import type { AlertPlate } from "./alerts";
import { orbMood, TvGameController, type TvGameControllerOptions } from "./controller";
import { DEFAULT_ORBIT_LABELS, ORBIT_COPY, type OrbitLabels } from "./copy";
import { OrbitCanvas } from "./orbit-canvas";
import { orbitText } from "./orbit-model";
import { createFilePlayer, tvSoundUrl } from "./sound";

const MAX_PLATES = 3;
const MAX_NEEDS_YOU = 5;
const MAX_FEED = 8;

/** One controller for the lifetime of the screen, ticking once a second. */
export function useTvGameController(options: Partial<TvGameControllerOptions> & { apiBaseUrl?: string }): TvGameController {
  const ref = useRef<TvGameController | null>(null);
  if (!ref.current) {
    ref.current = new TvGameController({
      ...options,
      player: options.player ?? createFilePlayer({ resolve: tvSoundUrl(options.apiBaseUrl ?? "") }),
    });
  }
  const controller = ref.current;
  useEffect(() => {
    const timer = setInterval(() => controller.tick(), 1000);
    return () => clearInterval(timer);
  }, [controller]);
  return controller;
}

function Plate({ plate, text, level }: { plate: AlertPlate; text: string; level: string }) {
  const warning = plate.level === "warning";
  const Icon = warning ? OctagonAlert : TriangleAlert;
  return (
    <div
      className={[
        "flex items-center gap-2 rounded-lg border px-3 py-1.5 text-[clamp(13px,3.4vw,20px)] font-medium",
        warning ? "border-[#e5483a] bg-[#e5483a]/20" : "border-[#f2b84b] bg-[#f2b84b]/15",
        plate.flashing ? "animate-pulse motion-reduce:animate-none" : "opacity-80",
      ].join(" ")}
    >
      <Icon aria-hidden className={warning ? "size-4 shrink-0 text-[#ff8a7a]" : "size-4 shrink-0 text-[#f2b84b]"} />
      <span className="sr-only">{level}</span>
      <span className="truncate">{text}</span>
    </div>
  );
}

export interface TvOrbitProps {
  controller: TvGameController;
  overview: TvOverview | null;
  lang?: TvLang;
  labels?: OrbitLabels;
  now: Date;
  offline?: boolean;
  /** Tapping the orb; the voice conversation hooks in here. */
  onOrbTap?: () => void;
}

/**
 * The Orbit screen (RUSL-1786): one orb to look at, one line, one card.
 * Portrait first; in landscape a right column adds "needs you" and the
 * last 24 hours.
 */
export function TvOrbit({ controller, overview, lang = "ru", labels = DEFAULT_ORBIT_LABELS, now, offline, onOrbTap }: TvOrbitProps) {
  const copy = ORBIT_COPY[lang];
  const tvCopy = TV_COPY[lang];
  const view = useSyncExternalStore(controller.subscribe, controller.getView, controller.getView);
  const [focus, setFocus] = useState(false);

  const needsYou = overview?.needs_owner ?? [];
  const needsYouTotal = overview?.needs_owner_total ?? 0;
  const mood = orbMood(view, needsYouTotal);
  const clock = controller.now();
  const text = orbitText({ view, mood, needsYou, needsYouTotal, copy, labels, duration: tvCopy.duration, now: clock });
  const snapshot = view.snapshot;
  // In an alarm the line under the orb already names the top warning.
  const plates = (mood === "alarm" ? view.plates.filter((p) => p !== view.plates.find((q) => q.level === "warning")) : view.plates).slice(0, MAX_PLATES);

  // Any tap or key is the master warning/caution button; the first one also
  // unlocks audio, which mobile browsers allow only after a gesture.
  useEffect(() => {
    const onGesture = () => {
      if (!controller.getView().soundUnlocked) void controller.unlockSound();
      if (controller.getView().plates.some((p) => p.flashing)) controller.acknowledge();
    };
    window.addEventListener("keydown", onGesture);
    window.addEventListener("pointerdown", onGesture);
    return () => {
      window.removeEventListener("keydown", onGesture);
      window.removeEventListener("pointerdown", onGesture);
    };
  }, [controller]);

  let sprintLine = tvCopy.noSprint;
  if (overview?.sprint) {
    const timeline = sprintTimeline(overview.sprint.start_date, overview.sprint.end_date, tvDate(now));
    sprintLine = timeline?.phase === "running" ? `${overview.sprint.name} · ${tvCopy.sprintDay(timeline.day, timeline.totalDays)}` : overview.sprint.name;
  }

  const wins = snapshot?.doneToday ?? 0;
  const dots = "●".repeat(Math.min(wins, 10));

  return (
    <div
      className="flex h-full w-full flex-col overflow-hidden bg-[radial-gradient(ellipse_at_50%_40%,#141a24_0%,#07090c_70%)] text-[#dfe6ee] landscape:md:flex-row"
      data-testid="tv-orbit"
      data-mood={mood}
    >
      <main className="flex min-h-0 flex-1 flex-col px-4 pb-[max(1rem,env(safe-area-inset-bottom))] pt-[max(0.75rem,env(safe-area-inset-top))]">
        <div className="flex items-center justify-between text-[clamp(12px,3.2vw,18px)] text-[#7d8a99]">
          <span className="truncate">{sprintLine}</span>
          <span className="flex shrink-0 items-center gap-2 tabular-nums">
            {view.quietHours && <Moon aria-label={copy.quietHours} className="size-4" />}
            {`${tvTime(now)} ${tvCopy.timeZone}`}
          </span>
        </div>

        <div className="relative min-h-[34vh] flex-1">
          <OrbitCanvas
            view={view}
            mood={mood}
            now={controller.now}
            orbLabel="Mika"
            focus={focus}
            onOrbTap={onOrbTap}
            ariaLabel={copy.orbitLabel(snapshot?.epics.length ?? 0, view.snapshot ? new Set(view.snapshot.work.filter((w) => w.state === "running").map((w) => w.agentId)).size : 0)}
          />
        </div>

        <p className="text-center text-[clamp(18px,5.2vw,40px)] font-semibold leading-tight" data-testid="orbit-say">
          {text.say}
        </p>
        <p className="mt-1 text-center text-[clamp(13px,3.4vw,22px)] text-[#8fa0b3]">{text.hint}</p>
        {offline && <p className="mt-1 text-center text-[clamp(12px,3vw,16px)] text-[#f2b84b]">{tvCopy.offline}</p>}

        {plates.length > 0 && (
          <div className="mt-3 flex flex-col gap-1.5">
            {plates.map((plate) => (
              <Plate key={plate.id} plate={plate} text={copy.plate(plate, labels)} level={copy.level(plate.level)} />
            ))}
          </div>
        )}

        {focus ? (
          needsYou.length > 0 && (
            <ul className="mt-3 space-y-1.5 text-[clamp(14px,3.6vw,22px)]">
              {needsYou.slice(0, MAX_NEEDS_YOU).map((issue) => (
                <li key={issue.id} className="truncate rounded-lg border border-[#4f3416] bg-[#1c1610] px-3 py-1.5">
                  {issue.title}
                </li>
              ))}
            </ul>
          )
        ) : (
          <>
            <div className="mx-1 mt-3 rounded-xl border border-[#223042] bg-[#121821] px-3 py-2.5 text-[clamp(13px,3.6vw,22px)]">
              <b className="text-[#f2b84b]">{text.cardLead}</b> {text.card}
            </div>
            <p className="mt-2 text-center text-[clamp(12px,3.2vw,18px)] text-[#7d8a99]">
              {`${copy.closedToday(wins)} ${dots}`}
              {snapshot && snapshot.streakDays > 0 ? ` · ${copy.streak(snapshot.streakDays)}` : ""}
            </p>
          </>
        )}

        <div className="mt-3 flex flex-wrap justify-center gap-2 text-[clamp(12px,3.2vw,16px)]">
          {!view.soundUnlocked ? (
            <button
              type="button"
              onClick={() => void controller.unlockSound()}
              className="flex items-center gap-1.5 rounded-full border border-[#2b3b4f] bg-[#18212c] px-3 py-1.5 font-semibold text-[#cfe0f2]"
            >
              <Volume2 aria-hidden className="size-4" />
              {copy.enableSound}
            </button>
          ) : (
            <button
              type="button"
              onClick={() => controller.toggleMute()}
              className="flex items-center gap-1.5 rounded-full border border-[#2b3b4f] bg-[#18212c] px-3 py-1.5 text-[#cfe0f2]"
            >
              {view.mutedUntil ? <VolumeX aria-hidden className="size-4" /> : <Volume2 aria-hidden className="size-4" />}
              {view.mutedUntil ? copy.unmute(tvTime(new Date(view.mutedUntil))) : copy.mute}
            </button>
          )}
          <button
            type="button"
            onClick={() => setFocus((f) => !f)}
            aria-pressed={focus}
            className="flex items-center gap-1.5 rounded-full border border-[#2b3b4f] bg-[#18212c] px-3 py-1.5 text-[#cfe0f2]"
          >
            <Focus aria-hidden className="size-4" />
            {focus ? copy.unfocus : copy.focus}
          </button>
        </div>
      </main>

      <aside className="hidden w-[32%] min-w-[300px] flex-col gap-4 border-l border-[#1d2a3a] p-4 landscape:md:flex">
        <section>
          <h2 className="mb-2 text-[clamp(14px,1.4vw,26px)] font-semibold text-[#ff8a7a]">{tvCopy.needsYou}</h2>
          {needsYou.length === 0 ? (
            <p className="text-[clamp(13px,1.1vw,20px)] text-[#7d8a99]">{tvCopy.needsYouEmpty}</p>
          ) : (
            <ul className="space-y-1.5 text-[clamp(13px,1.1vw,20px)]">
              {needsYou.slice(0, MAX_NEEDS_YOU).map((issue) => (
                <li key={issue.id} className="rounded-lg border border-[#4f3416] bg-[#1c1610] px-3 py-1.5">
                  <span className="line-clamp-2">{issue.title}</span>
                  <span className="text-[#8fa0b3]">{tvCopy.waitingFor(tvCopy.duration(now.getTime() - Date.parse(issue.since)))}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
        {!focus && (
          <section className="min-h-0 flex-1 overflow-hidden">
            <h2 className="mb-2 text-[clamp(14px,1.4vw,26px)] font-semibold text-[#9fb4c8]">{tvCopy.feedTitle}</h2>
            {(overview?.feed ?? []).length === 0 ? (
              <p className="text-[clamp(13px,1.1vw,20px)] text-[#7d8a99]">{tvCopy.feedEmpty}</p>
            ) : (
              <ul className="space-y-1 text-[clamp(12px,1vw,18px)]">
                {(overview?.feed ?? []).slice(0, MAX_FEED).map((event, i) => (
                  <li key={`${event.at}:${i}`} className="flex gap-2">
                    <span className="shrink-0 tabular-nums text-[#7d8a99]">{tvTime(event.at)}</span>
                    <span className={event.kind === "run_failed" ? "text-[#ff8a7a]" : "text-[#8fd6a0]"}>
                      {event.kind === "run_failed" ? tvCopy.feedRunFailed : tvCopy.feedDone}
                    </span>
                    <span className="truncate">{event.issue_title}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}
      </aside>
    </div>
  );
}

export interface TvOrbitLiveProps {
  apiBaseUrl: string;
  overview: TvOverview | null;
  lang?: TvLang;
  labels?: OrbitLabels;
  now: Date;
  offline?: boolean;
}

/** Orbit on live data: polls /api/tv/game and /api/tv/health in the foreground. */
export function TvOrbitLive({ apiBaseUrl, overview, lang, labels, now, offline }: TvOrbitLiveProps) {
  const controller = useTvGameController({ apiBaseUrl });
  const game = useQuery(tvGameOptions(apiBaseUrl));
  const health = useQuery(tvHealthOptions(apiBaseUrl));
  const failed = useMemo(() => new FailedRunTracker(), []);
  const needsYouIds = useMemo(() => new Set((overview?.needs_owner ?? []).map((i) => i.id)), [overview]);

  useEffect(() => {
    if (!game.data || !health.data) return;
    controller.update(toTvGameSnapshot(game.data, health.data, needsYouIds, controller.now()));
    controller.pushEvents(failed.next(game.data));
  }, [controller, failed, game.data, health.data, needsYouIds]);

  return (
    <TvOrbit
      controller={controller}
      overview={overview}
      lang={lang}
      labels={labels}
      now={now}
      offline={offline || game.isError || health.isError}
    />
  );
}

export interface TvOrbitPageProps {
  apiBaseUrl: string;
  wsUrl?: string;
  lang?: TvLang;
  labels?: OrbitLabels;
  /** "board" keeps the RUSL-1784 dashboard. */
  view?: "orbit" | "board";
}

/**
 * The /tv page: the base screen's pairing, realtime and wake lock, with the
 * Orbit in place of the dashboard unless the board was asked for.
 */
export function TvOrbitPage({ apiBaseUrl, wsUrl, lang = "ru", labels, view = "orbit" }: TvOrbitPageProps) {
  return (
    <TvScreen
      apiBaseUrl={apiBaseUrl}
      wsUrl={wsUrl}
      lang={lang}
      screen={
        view === "orbit"
          ? (overview, state) => (
              <TvOrbitLive apiBaseUrl={apiBaseUrl} overview={overview} lang={lang} labels={labels} now={state.now} offline={state.offline} />
            )
          : undefined
      }
    />
  );
}
