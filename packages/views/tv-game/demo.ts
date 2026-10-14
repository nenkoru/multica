import type { TvIssue, TvOverview } from "@multica/core/tv";
import { EMPTY_TV_OVERVIEW } from "@multica/core/tv";
import type { TvGameController } from "./controller";
import { emptySnapshot, type TvGameSnapshot, type TvGameWork, type TvHealthKey } from "./model";

// Demo mode (`?demo=1`): a scripted run through the Orbit's moods — calm,
// a finished issue, Mika calling, a voice conversation, an alarm — and
// through every row of the sound table, checking the priority, the
// acknowledgement and the night mode. Time is virtual: the script jumps the
// clock past the 2-minute caution limit and into the night instead of
// waiting. demo.test.ts runs the same steps on a fake clock.

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;

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
  const d = new Date(day + 3 * HOUR);
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate(), 9, 0, 0);
}

export interface DemoContext {
  controller: TvGameController;
  clock: DemoClock;
  state: TvGameSnapshot;
  overview: TvOverview;
  /** Set by the screen to re-render when the overview changes. */
  onOverview?: (overview: TvOverview) => void;
}

export interface DemoStep {
  /** Real time after the previous step. */
  delayMs: number;
  id: string;
  run(ctx: DemoContext): void;
}

const iso = (at: number) => new Date(at).toISOString();

function initialState(now: number): TvGameSnapshot {
  const fresh = iso(now - 10 * MINUTE);
  return {
    ...emptySnapshot(now),
    epics: [
      { id: "e-model", title: "Qwen V100", open: 15, doneInSprint: 2, lastActivityAt: fresh },
      { id: "e-parts", title: "Запчасти", open: 6, doneInSprint: 3, lastActivityAt: fresh },
      { id: "e-tv", title: "ТВ-режим", open: 4, doneInSprint: 1, lastActivityAt: fresh },
      { id: "e-seo", title: "SEO", open: 9, doneInSprint: 0, lastActivityAt: fresh },
      { id: "e-old", title: "Каталог", open: 3, doneInSprint: 0, lastActivityAt: iso(now - 30 * HOUR) },
    ],
    agents: [
      { id: "a-lab", name: "Лаборант", slots: 2 },
      { id: "a-dev", name: "Разработчик", slots: 2 },
      { id: "a-ops", name: "Ops", slots: 1 },
      { id: "a-site", name: "Сайтодел", slots: 1 },
      { id: "a-data", name: "Инженер данных", slots: 1 },
      { id: "a-mika", name: "Mika", slots: 1 },
    ],
    work: [
      { issueId: "i-kv", title: "KVarN-доводка", agentId: "a-lab", epicId: "e-model", state: "running", lastActivityAt: fresh, since: iso(now - 3 * HOUR) },
      { issueId: "i-orb", title: "Орбита", agentId: "a-dev", epicId: "e-tv", state: "running", lastActivityAt: fresh, since: iso(now - 40 * MINUTE) },
      { issueId: "i-bench", title: "Замер tok/s", agentId: "a-ops", epicId: "e-model", state: "running", lastActivityAt: fresh, since: iso(now - 20 * MINUTE) },
      { issueId: "i-cards", title: "Карточки", agentId: "a-site", epicId: "e-parts", state: "running", lastActivityAt: fresh, since: iso(now - HOUR) },
    ],
    doneInSprint: 11,
    doneToday: 4,
    streakDays: 3,
    runningRuns: 4,
    queued: 1,
    health: (
      ["daemon", "llm", "site", "stall", "budget", "oom", "autopilot", "failed_burst", "duplicate_run", "llm_slow", "minimums"] as TvHealthKey[]
    ).map((key) => ({ key, status: "ok" as const, count: 0 })),
  };
}

function initialOverview(now: number): TvOverview {
  const day = new Date(now + 3 * HOUR).toISOString().slice(0, 10);
  const end = new Date(now + 3 * HOUR + 2 * 24 * HOUR).toISOString().slice(0, 10);
  return {
    ...EMPTY_TV_OVERVIEW,
    sprint: { id: "s-2", name: "Спринт 2", status: "active", start_date: day, end_date: end, total: 30, done: 11 },
    feed: [
      { kind: "done", at: iso(now - 30 * MINUTE), issue_identifier: "RUSL-1", issue_title: "Выгрузка остатков", actor_name: "Инженер данных", failure_reason: null },
      { kind: "run_failed", at: iso(now - 2 * HOUR), issue_identifier: "RUSL-2", issue_title: "Перевод описаний", actor_name: "Сайтодел", failure_reason: "timeout" },
    ],
  };
}

const needsYouIssue = (now: number): TvIssue => ({
  id: "i-cards",
  identifier: "RUSL-3",
  title: "Карточки: выбрать шаблон фото",
  status: "blocked",
  assignee_type: "agent",
  assignee_name: "Сайтодел",
  since: iso(now),
  last_activity_at: iso(now),
  stale: false,
});

function publish(ctx: DemoContext, patch: Partial<TvGameSnapshot>): void {
  ctx.state = { ...ctx.state, ...patch, at: ctx.clock.now() };
  ctx.state.runningRuns = ctx.state.work.filter((w) => w.state === "running").length;
  ctx.controller.update(ctx.state);
}

function setWork(ctx: DemoContext, issueId: string, patch: Partial<TvGameWork> | null): TvGameWork[] {
  const rest = ctx.state.work.filter((w) => w.issueId !== issueId);
  if (patch === null) return rest;
  const before = ctx.state.work.find((w) => w.issueId === issueId);
  return [...rest, { ...(before as TvGameWork), ...patch, issueId, lastActivityAt: iso(ctx.clock.now()) }];
}

function setHealth(ctx: DemoContext, statuses: Partial<Record<TvHealthKey, "ok" | "warning">>, count = 1): void {
  publish(ctx, {
    health: ctx.state.health.map((h) => {
      const status = statuses[h.key];
      return status ? { ...h, status, count: status === "ok" ? 0 : h.key === "minimums" ? 55 : h.key === "failed_burst" ? 6 : count } : h;
    }),
  });
}

/** Raise one caution signal after the 2-minute limit has passed. */
const caution = (id: string, key: TvHealthKey): DemoStep => ({
  delayMs: 2500,
  id,
  run: (ctx) => {
    ctx.clock.advance(2 * MINUTE);
    setHealth(ctx, { [key]: "warning" });
  },
});

export const DEMO_STEPS: DemoStep[] = [
  {
    delayMs: 0,
    id: "calm",
    run: (ctx) => {
      ctx.state = initialState(ctx.clock.now());
      ctx.overview = initialOverview(ctx.clock.now());
      ctx.onOverview?.(ctx.overview);
      ctx.controller.setTalking(false);
      ctx.controller.update(ctx.state);
    },
  },
  {
    delayMs: 4000,
    id: "issue-done",
    run: (ctx) =>
      publish(ctx, {
        work: setWork(ctx, "i-bench", null),
        doneToday: ctx.state.doneToday + 1,
        recentDone: [{ issueId: "i-bench", title: "Замер tok/s", epicId: "e-model", agentId: "a-ops" }, ...ctx.state.recentDone],
      }),
  },
  {
    delayMs: 3000,
    id: "epic-closed",
    run: (ctx) =>
      publish(ctx, {
        epics: ctx.state.epics.filter((e) => e.id !== "e-old"),
        recentEpicsClosed: [{ epicId: "e-old", title: "Каталог" }],
      }),
  },
  {
    delayMs: 3000,
    id: "call",
    run: (ctx) => {
      // A run fails (master caution), and an issue now needs Ruslan.
      ctx.controller.pushEvents([
        { type: "run_failed", issueId: "i-orb", title: "Орбита", agentId: "a-dev", agentName: "Разработчик", epicId: "e-tv" },
      ]);
      ctx.overview = { ...ctx.overview, needs_owner: [needsYouIssue(ctx.clock.now())], needs_owner_total: 1 };
      ctx.onOverview?.(ctx.overview);
      publish(ctx, { work: setWork(ctx, "i-cards", { state: "blocked", needsYou: true }) });
    },
  },
  {
    delayMs: 4000,
    id: "acknowledge-call",
    run: (ctx) => {
      ctx.controller.acknowledge();
      ctx.overview = { ...ctx.overview, needs_owner: [], needs_owner_total: 0 };
      ctx.onOverview?.(ctx.overview);
      publish(ctx, { work: setWork(ctx, "i-cards", { state: "running", needsYou: false }) });
    },
  },
  { delayMs: 3000, id: "talk", run: (ctx) => ctx.controller.setTalking(true) },
  { delayMs: 4000, id: "talk-end", run: (ctx) => ctx.controller.setTalking(false) },
  caution("oom", "oom"),
  caution("autopilot", "autopilot"),
  caution("failed-burst", "failed_burst"),
  caution("duplicate-run", "duplicate_run"),
  caution("llm-slow", "llm_slow"),
  caution("minimums", "minimums"),
  {
    delayMs: 2500,
    id: "cautions-clear",
    run: (ctx) => {
      ctx.controller.acknowledge();
      setHealth(ctx, { oom: "ok", autopilot: "ok", failed_burst: "ok", duplicate_run: "ok", llm_slow: "ok", minimums: "ok" });
    },
  },
  // Warnings in table order. The daemon outage repeats until acknowledged;
  // the model failing meanwhile is lower in the table and stays silent.
  { delayMs: 2500, id: "daemon", run: (ctx) => setHealth(ctx, { daemon: "warning" }) },
  { delayMs: 9000, id: "llm-under-daemon", run: (ctx) => setHealth(ctx, { llm: "warning" }) },
  { delayMs: 3000, id: "acknowledge-daemon", run: (ctx) => ctx.controller.acknowledge() },
  { delayMs: 2500, id: "daemon-cleared", run: (ctx) => setHealth(ctx, { daemon: "ok", llm: "ok" }) },
  { delayMs: 3000, id: "llm", run: (ctx) => setHealth(ctx, { llm: "warning" }) },
  { delayMs: 3500, id: "llm-acknowledge", run: (ctx) => ctx.controller.acknowledge() },
  {
    delayMs: 1500,
    id: "site-and-stall",
    // Two warnings at once: only the higher one, the site, sounds.
    run: (ctx) => setHealth(ctx, { llm: "ok", site: "warning", stall: "warning" }),
  },
  { delayMs: 4000, id: "site-acknowledge", run: (ctx) => ctx.controller.acknowledge() },
  { delayMs: 1500, id: "site-cleared", run: (ctx) => setHealth(ctx, { site: "ok" }) },
  { delayMs: 2500, id: "stall-cleared", run: (ctx) => setHealth(ctx, { stall: "ok" }) },
  { delayMs: 2500, id: "stall", run: (ctx) => setHealth(ctx, { stall: "warning" }) },
  { delayMs: 3500, id: "stall-acknowledge", run: (ctx) => { ctx.controller.acknowledge(); setHealth(ctx, { stall: "ok" }); } },
  { delayMs: 2500, id: "budget", run: (ctx) => setHealth(ctx, { budget: "warning" }) },
  { delayMs: 4000, id: "budget-cleared", run: (ctx) => { ctx.controller.acknowledge(); setHealth(ctx, { budget: "ok" }); } },
  {
    delayMs: 3000,
    id: "night-caution",
    run: (ctx) => {
      // 23:30 MSK: a caution only shows, a warning still sounds.
      ctx.clock.set(mskNoon(ctx.clock.now()) + 11 * HOUR + 30 * MINUTE);
      setHealth(ctx, { oom: "warning" });
    },
  },
  { delayMs: 3000, id: "night-warning", run: (ctx) => setHealth(ctx, { daemon: "warning" }) },
  {
    delayMs: 5000,
    id: "night-cleared",
    run: (ctx) => {
      ctx.controller.acknowledge();
      setHealth(ctx, { daemon: "ok", oom: "ok" });
    },
  },
];

export const DEMO_LOOP_PAUSE_MS = 5000;

export function demoContext(controller: TvGameController, clock: DemoClock): DemoContext {
  return { controller, clock, state: emptySnapshot(clock.now()), overview: EMPTY_TV_OVERVIEW };
}
