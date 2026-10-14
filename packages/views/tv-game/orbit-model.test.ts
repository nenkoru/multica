// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { TvIssue } from "@multica/core/tv";
import { TV_COPY } from "../tv/copy";
import type { AlertPlate } from "./alerts";
import { orbMood, type TvGameView } from "./controller";
import { DEFAULT_ORBIT_LABELS, ORBIT_COPY } from "./copy";
import { emptySnapshot } from "./model";
import { orbitText, workingAgents } from "./orbit-model";

const NOW = Date.UTC(2026, 9, 14, 13, 40, 0);
const view = (over: Partial<TvGameView> = {}): TvGameView => ({
  snapshot: {
    ...emptySnapshot(NOW),
    agents: [{ id: "lab", name: "Лаборант", slots: 2 }],
    work: [
      { issueId: "kv", title: "KVarN-доводка", agentId: "lab", epicId: null, state: "running", lastActivityAt: null, since: new Date(NOW - 3 * 3600_000).toISOString() },
      { issueId: "x", title: "Short", agentId: "lab", epicId: null, state: "running", lastActivityAt: null, since: new Date(NOW - 600_000).toISOString() },
    ],
  },
  plates: [],
  sparks: [],
  mutedUntil: null,
  quietHours: false,
  soundUnlocked: true,
  talking: false,
  version: 1,
  ...over,
});
const plate = (over: Partial<AlertPlate>): AlertPlate => ({
  id: "warning:llm", signal: "llm", level: "warning", group: null, count: 1, detail: null, flashing: true, since: NOW - 4 * 60_000, updatedAt: NOW, ...over,
});
const text = (v: TvGameView, needsYou: TvIssue[] = []) =>
  orbitText({
    view: v,
    mood: orbMood(v, needsYou.length),
    needsYou,
    needsYouTotal: needsYou.length,
    copy: ORBIT_COPY.ru,
    labels: DEFAULT_ORBIT_LABELS,
    duration: TV_COPY.ru.duration,
    now: NOW,
  });

describe("orbitText", () => {
  it("calm: agents at work and the longest run as the main thing", () => {
    const t = text(view());
    expect(workingAgents(view())).toBe(1);
    expect(t.say).toBe("Всё идёт. 1 агент в работе.");
    expect(t.hint).toBe("Тебе ничего делать не нужно");
    expect(t.card).toMatch(/^KVarN-доводка — Лаборант, 3 ч/);
  });

  it("alarm: Mika calls with the top warning and how long it lasts", () => {
    const t = text(view({ plates: [plate({})] }));
    expect(t.say).toMatch(/^Mika зовёт: 27B недоступна 4 мин/);
    expect(t.hint).toBe("Коснись экрана — сигнал стихнет");
    expect(text(view({ plates: [plate({ flashing: false })] })).hint).toBe("Сигнал принят, ждём исправления");
  });

  it("call: one decision from the oldest needs-you issue", () => {
    const issue = { id: "n", identifier: "R-1", title: "Выбрать шаблон", status: "blocked", assignee_type: null, assignee_name: null, since: "", last_activity_at: "", stale: false };
    const t = text(view(), [issue]);
    expect(t.say).toBe("Mika зовёт: нужен ты — 1 задача");
    expect(t.cardLead).toBe("Одно решение:");
    expect(t.card).toBe("Выбрать шаблон");
  });

  it("talk wins over everything", () => {
    const t = text(view({ talking: true, plates: [plate({})] }));
    expect(t.say).toBe("Слушаю…");
  });
});
