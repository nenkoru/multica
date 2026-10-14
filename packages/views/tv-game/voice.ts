import type { TvGameEvent, TvHealthKey } from "./model";

// Spoken phrases and alarm plate text. They are Russian on purpose, whatever
// the UI locale: the TV speaks to one person in one room (RUSL-1786), and a
// phrase is short enough to be understood from across it.

export type CautionGroup = "run_failed" | "need_you" | "stuck" | "no_agents";

export interface VoiceLabels {
  /** How the self-hosted model is called out loud, e.g. "27B". */
  llm: string;
}

export const DEFAULT_VOICE_LABELS: VoiceLabels = { llm: "Модель" };

/** 1 запуск, 2 запуска, 5 запусков. */
export function pluralRu(n: number, one: string, few: string, many: string): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod10 === 1 && mod100 !== 11) return one;
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return few;
  return many;
}

export function cautionGroupOf(event: TvGameEvent): CautionGroup | null {
  switch (event.type) {
    case "run_failed":
    case "need_you":
    case "stuck":
    case "no_agents":
      return event.type;
    default:
      return null;
  }
}

/** One caution group, `count` events, `last` the newest of them. */
export function cautionPhrase(group: CautionGroup, count: number, last: TvGameEvent): string {
  switch (group) {
    case "run_failed":
      // Agent names do not decline in Russian, so the name goes after a dash.
      if (count === 1 && last.type === "run_failed") return `Запуск упал — ${last.agentName}`;
      return `${count} ${pluralRu(count, "запуск упал", "запуска упали", "запусков упали")}`;
    case "need_you":
      if (count === 1) return "Нужен ты — новая задача";
      return `Нужен ты — ${count} ${pluralRu(count, "задача", "задачи", "задач")}`;
    case "stuck":
      if (count === 1) return "Задача застряла больше суток";
      return `${count} ${pluralRu(count, "задача застряла", "задачи застряли", "задач застряли")} больше суток`;
    case "no_agents":
      return "Агенты стоят, а очередь не пуста";
  }
}

export function warningPhrase(key: TvHealthKey, labels: VoiceLabels): string {
  switch (key) {
    case "daemon":
      return "Демон Multica не на связи";
    case "llm":
      return `${labels.llm} недоступна`;
    case "oom":
      return "Запуски падают по памяти";
    case "site":
      return "Сайт недоступен";
  }
}

export const CAUTION_PREFIX = "Внимание: ";
export const WARNING_PREFIX = "Тревога: ";

/** "Внимание: " + "Сайт недоступен" reads as "Внимание: сайт недоступен". */
export function spoken(prefix: string, phrase: string): string {
  return prefix + phrase.charAt(0).toLocaleLowerCase("ru-RU") + phrase.slice(1);
}
