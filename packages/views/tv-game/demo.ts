import type { TvGameController } from "./controller";
import { emptySnapshot, type TvGameSnapshot, type TvGameWork, type TvHealthKey } from "./model";

// Demo mode (`?demo=1`): a scripted run through every event the layer knows —
// digging, carrying, a delivered load, crashes, a blocked worker, grouped
// cautions after the 2-minute limit, a master warning with acknowledgement,
// then night mode where only a warning sounds. Time is virtual: the script
// jumps the clock to show the rate limit and the quiet hours without waiting
// for them. The same steps run in demo.test.ts with a fake clock.

const MINUTE = 60_000;

/** A clock that runs in real time from `start` and can jump forward. */
export class DemoClock {
  private offset: number;
  constructor(
    start: number,
    private readonly real: () => number = Date.now,
  ) {
    this.offset = start - real();
  }
  now = (): number => this.real() + this.offset;
  advance(ms: number): void {
    this.offset += ms;
  }
  set(at: number): void {
    this.offset = at - this.real();
  }
}

/** 12:00 MSK of the given day — daytime, so the demo starts with sound. */
export function mskNoon(day: number): number {
  const d = new Date(day + 3 * 60 * MINUTE);
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate(), 9, 0, 0);
}

export interface DemoContext {
  controller: TvGameController;
  clock: DemoClock;
  state: TvGameSnapshot;
}

export interface DemoStep {
  /** Real time after the previous step. */
  delayMs: number;
  id: string;
  run(ctx: DemoContext): void;
}

const HOUR = 60 * MINUTE;

function iso(at: number): string {
  return new Date(at).toISOString();
}

function initialState(now: number): TvGameSnapshot {
  const fresh = iso(now - 10 * MINUTE);
  return {
    ...emptySnapshot(now),
    epics: [
      { id: "e-tv", title: "ТВ-режим", open: 6, doneInSprint: 2, lastActivityAt: fresh },
      { id: "e-site", title: "Сайт и SEO", open: 14, doneInSprint: 5, lastActivityAt: fresh },
      { id: "e-model", title: "Модель 27B", open: 9, doneInSprint: 1, lastActivityAt: fresh },
      { id: "e-catalog", title: "Каталог", open: 4, doneInSprint: 0, lastActivityAt: iso(now - 30 * HOUR) },
    ],
    agents: [
      { id: "a-dev", name: "Разработчик", slots: 2 },
      { id: "a-ops", name: "Ops", slots: 1 },
      { id: "a-site", name: "Сайтодел", slots: 1 },
      { id: "a-data", name: "Инженер данных", slots: 1 },
      { id: "a-review", name: "Ревьюер", slots: 1 },
    ],
    work: [
      { issueId: "i-1", title: "Игровой слой", agentId: "a-dev", epicId: "e-tv", state: "running", lastActivityAt: fresh },
    ],
    doneInSprint: 8,
    runningRuns: 1,
    hostCeiling: 12,
    queued: 2,
    health: [
      { key: "daemon", status: "ok", count: 0 },
      { key: "llm", status: "ok", count: 0 },
      { key: "oom", status: "ok", count: 0 },
      { key: "site", status: "ok", count: 0 },
    ],
  };
}

function publish(ctx: DemoContext, patch: Partial<TvGameSnapshot>): void {
  ctx.state = { ...ctx.state, ...patch, at: ctx.clock.now() };
  ctx.state.runningRuns = ctx.state.work.filter((w) => w.state === "running").length;
  ctx.controller.update(ctx.state);
}

function setWork(ctx: DemoContext, issueId: string, patch: Partial<TvGameWork> | null): TvGameWork[] {
  const fresh = iso(ctx.clock.now());
  const rest = ctx.state.work.filter((w) => w.issueId !== issueId);
  if (patch === null) return rest;
  const before = ctx.state.work.find((w) => w.issueId === issueId);
  return [...rest, { ...(before as TvGameWork), ...patch, issueId, lastActivityAt: fresh }];
}

function health(ctx: DemoContext, key: TvHealthKey, status: "ok" | "warning"): TvGameSnapshot["health"] {
  return ctx.state.health.map((h) => (h.key === key ? { ...h, status, count: status === "ok" ? 0 : 2 } : h));
}

export const DEMO_STEPS: DemoStep[] = [
  {
    delayMs: 0,
    id: "start",
    run: (ctx) => {
      ctx.state = initialState(ctx.clock.now());
      ctx.controller.update(ctx.state);
    },
  },
  {
    delayMs: 3000,
    id: "dig",
    run: (ctx) => {
      let work = setWork(ctx, "i-2", { title: "Карточки товаров", agentId: "a-site", epicId: "e-site", state: "running" });
      ctx.state = { ...ctx.state, work };
      work = setWork(ctx, "i-3", { title: "Замер KV-кэша", agentId: "a-ops", epicId: "e-model", state: "running" });
      ctx.state = { ...ctx.state, work };
      work = setWork(ctx, "i-4", { title: "Выгрузка остатков", agentId: "a-data", epicId: "e-catalog", state: "running" });
      publish(ctx, { work, queued: 0 });
    },
  },
  {
    delayMs: 3000,
    id: "carry",
    run: (ctx) => publish(ctx, { work: setWork(ctx, "i-1", { state: "in_review" }) }),
  },
  {
    delayMs: 4000,
    id: "deliver",
    run: (ctx) => {
      const epics = ctx.state.epics.map((e) => (e.id === "e-tv" ? { ...e, open: e.open - 1, doneInSprint: e.doneInSprint + 1 } : e));
      publish(ctx, {
        work: setWork(ctx, "i-1", null),
        epics,
        doneInSprint: ctx.state.doneInSprint + 1,
        recentDone: [{ issueId: "i-1", title: "Игровой слой", epicId: "e-tv", agentId: "a-dev" }, ...ctx.state.recentDone],
      });
    },
  },
  {
    delayMs: 3000,
    id: "crash",
    run: (ctx) =>
      ctx.controller.pushEvents([
        { type: "run_failed", issueId: "i-3", title: "Замер KV-кэша", agentId: "a-ops", agentName: "Ops", epicId: "e-model" },
      ]),
  },
  {
    delayMs: 3000,
    id: "more-trouble",
    run: (ctx) => {
      // Inside the 2-minute limit: the plates update, the speaker stays quiet.
      ctx.controller.pushEvents([
        { type: "run_failed", issueId: "i-4", title: "Выгрузка остатков", agentId: "a-data", agentName: "Инженер данных", epicId: "e-catalog" },
        { type: "run_failed", issueId: "i-5", title: "Перевод описаний", agentId: "a-site", agentName: "Сайтодел", epicId: "e-site" },
      ]);
      publish(ctx, { work: setWork(ctx, "i-2", { state: "blocked", needsYou: true }) });
    },
  },
  {
    delayMs: 4000,
    id: "rate-limit-passes",
    run: (ctx) => {
      // Jump past the limit: the grouped caution sounds once.
      ctx.clock.advance(2 * MINUTE);
      ctx.controller.tick();
    },
  },
  {
    delayMs: 5000,
    id: "warning",
    run: (ctx) => publish(ctx, { health: health(ctx, "llm", "warning") }),
  },
  {
    delayMs: 10000,
    id: "acknowledge",
    run: (ctx) => ctx.controller.acknowledge(),
  },
  {
    delayMs: 4000,
    id: "recover",
    run: (ctx) => publish(ctx, { health: health(ctx, "llm", "ok") }),
  },
  {
    delayMs: 3000,
    id: "night",
    run: (ctx) => {
      // 23:30 MSK: cautions only show, warnings still sound.
      ctx.clock.set(mskNoon(ctx.clock.now()) + 11 * HOUR + 30 * MINUTE);
      ctx.controller.pushEvents([
        { type: "run_failed", issueId: "i-6", title: "Ночная сборка", agentId: "a-dev", agentName: "Разработчик", epicId: "e-tv" },
      ]);
      ctx.controller.tick();
    },
  },
  {
    delayMs: 4000,
    id: "night-warning",
    run: (ctx) => publish(ctx, { health: health(ctx, "daemon", "warning") }),
  },
  {
    delayMs: 9000,
    id: "night-acknowledge",
    run: (ctx) => ctx.controller.acknowledge(),
  },
  {
    delayMs: 3000,
    id: "night-recover",
    run: (ctx) => publish(ctx, { health: health(ctx, "daemon", "ok") }),
  },
];

export const DEMO_LOOP_PAUSE_MS = 5000;
