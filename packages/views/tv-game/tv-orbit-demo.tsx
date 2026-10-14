"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import type { TvOverview } from "@multica/core/tv";
import type { TvLang } from "../tv/copy";
import { useWakeLock } from "../tv/use-device";
import { DEMO_LOOP_PAUSE_MS, DEMO_STEPS, DemoClock, demoContext, mskNoon } from "./demo";
import type { SoundPlayer } from "./sound";
import { TvOrbit, useTvGameController } from "./tv-orbit";

export interface TvOrbitDemoProps {
  lang?: TvLang;
  apiBaseUrl?: string;
  /** Overrides where the sound files come from (UI Lab, recordings). */
  player?: SoundPlayer;
  /** Called with the step id as each step runs (recordings, tests). */
  onStep?: (id: string) => void;
}

/** The Orbit driven by the demo script instead of live data; loops. */
export function TvOrbitDemo({ lang = "ru", apiBaseUrl, player, onStep }: TvOrbitDemoProps) {
  const clock = useMemo(() => new DemoClock(mskNoon(Date.now())), []);
  const controller = useTvGameController({ now: clock.now, apiBaseUrl, player });
  const [overview, setOverview] = useState<TvOverview | null>(null);
  const [now, setNow] = useState(() => new Date(clock.now()));
  useWakeLock(true);
  const onStepRef = useRef(onStep);
  onStepRef.current = onStep;

  useEffect(() => {
    const ctx = demoContext(controller, clock);
    ctx.onOverview = setOverview;
    let timer: ReturnType<typeof setTimeout>;
    const run = (index: number) => {
      if (index >= DEMO_STEPS.length) {
        timer = setTimeout(() => {
          clock.set(mskNoon(Date.now()));
          run(0);
        }, DEMO_LOOP_PAUSE_MS);
        return;
      }
      const step = DEMO_STEPS[index]!;
      timer = setTimeout(() => {
        step.run(ctx);
        onStepRef.current?.(step.id);
        run(index + 1);
      }, step.delayMs);
    };
    run(0);
    const tick = setInterval(() => setNow(new Date(clock.now())), 1000);
    return () => {
      clearTimeout(timer);
      clearInterval(tick);
    };
  }, [controller, clock]);

  return (
    <div className="dark fixed inset-0 bg-background">
      <TvOrbit controller={controller} overview={overview} lang={lang} now={now} onOrbTap={() => controller.setTalking(!controller.getView().talking)} />
    </div>
  );
}
