import { useEffect, useMemo, useState } from "react";
import { useT } from "../i18n";
import { DEMO_LOOP_PAUSE_MS, DEMO_STEPS, DemoClock, mskNoon, type DemoContext } from "./demo";
import { emptySnapshot } from "./model";
import { TvGameLayer, useTvGameController } from "./tv-game-layer";

/**
 * The game layer driven by the demo script instead of live data, for
 * `?demo=1` on the TV screen. Loops until the page is closed.
 */
export function TvGameDemo() {
  const { t } = useT("tv-game");
  const clock = useMemo(() => new DemoClock(mskNoon(Date.now())), []);
  const controller = useTvGameController({ now: clock.now });
  const [step, setStep] = useState(DEMO_STEPS[0]!.id);

  useEffect(() => {
    const ctx: DemoContext = { controller, clock, state: emptySnapshot(clock.now()) };
    let timer: ReturnType<typeof setTimeout>;
    const run = (index: number) => {
      if (index >= DEMO_STEPS.length) {
        timer = setTimeout(() => {
          clock.set(mskNoon(Date.now()));
          run(0);
        }, DEMO_LOOP_PAUSE_MS);
        return;
      }
      const current = DEMO_STEPS[index]!;
      timer = setTimeout(() => {
        current.run(ctx);
        setStep(current.id);
        run(index + 1);
      }, current.delayMs);
    };
    run(0);
    return () => clearTimeout(timer);
  }, [controller, clock]);

  return (
    <TvGameLayer
      controller={controller}
      extra={
        <span className="rounded bg-white/10 px-[1vh] py-[0.4vh] font-mono text-[1.8vh] text-white/70">
          {`${t(($) => $.demo)} · ${step}`}
        </span>
      }
    />
  );
}
