import type { TvLang } from "../tv/copy";
import type { AlertPlate } from "./alerts";
import type { SignalId } from "./sound-table";

// Copy of the Orbit screen and its alarm plates. Like the base TV screen it
// is chosen per screen (?lang=), Russian by default: a kiosk has no
// signed-in user whose locale could apply.

export interface OrbitLabels {
  /** How the self-hosted model is called on screen, e.g. "27B". */
  llm: string;
}

export const DEFAULT_ORBIT_LABELS: OrbitLabels = { llm: "27B" };

export interface OrbitCopy {
  calm: (working: number) => string;
  calmHint: string;
  calling: (what: string) => string;
  callHint: string;
  alarmHint: string;
  alarmAcked: string;
  talking: string;
  talkingHint: string;
  mainNow: string;
  oneDecision: string;
  workingOn: (title: string, agent: string, duration: string) => string;
  nothingRunning: string;
  needsYouCount: (n: number) => string;
  closedToday: (n: number) => string;
  streak: (days: number) => string;
  enableSound: string;
  mute: string;
  unmute: (until: string) => string;
  quietHours: string;
  focus: string;
  unfocus: string;
  plate: (plate: AlertPlate, labels: OrbitLabels) => string;
  level: (level: "warning" | "caution") => string;
  orbitLabel: (epics: number, working: number) => string;
}

function plural(n: number, one: string, few: string, many: string): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod10 === 1 && mod100 !== 11) return one;
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return few;
  return many;
}

const ruSignal: Record<Exclude<SignalId, "master_caution" | "cleared" | "issue_done" | "epic_closed">, (p: AlertPlate, l: OrbitLabels) => string> = {
  daemon: () => "Демон Multica не на связи",
  llm: (_, l) => `${l.llm} недоступна`,
  site: () => "Сайт не отвечает",
  stall: () => "Работа встала: очередь есть, агенты стоят",
  budget: () => "Деньги или лимит подписки у резерва",
  oom: () => "Хост перегружен: запуски падают по памяти",
  autopilot: () => "Автопилот упал",
  failed_burst: (p) => `${p.count} ${plural(p.count, "запуск упал", "запуска упали", "запусков упали")} за 10 мин`,
  duplicate_run: () => "Два запуска на одной задаче",
  llm_slow: (_, l) => `${l.llm} медленнее нормы`,
  minimums: (p) => `До конца спринта меньше суток, готово ${p.count}%`,
};

const enSignal: typeof ruSignal = {
  daemon: () => "Multica daemon offline",
  llm: (_, l) => `${l.llm} unavailable`,
  site: () => "Site not responding",
  stall: () => "Work stalled: queue waiting, agents idle",
  budget: () => "Budget or subscription limit at reserve",
  oom: () => "Host overloaded: runs killed for memory",
  autopilot: () => "Autopilot failed",
  failed_burst: (p) => `${p.count} runs failed in 10 min`,
  duplicate_run: () => "Two runs on one issue",
  llm_slow: (_, l) => `${l.llm} slower than normal`,
  minimums: (p) => `Sprint ends within a day, ${p.count}% done`,
};

const ru: OrbitCopy = {
  calm: (n) => (n === 0 ? "Всё спокойно." : `Всё идёт. ${n} ${plural(n, "агент", "агента", "агентов")} в работе.`),
  calmHint: "Тебе ничего делать не нужно",
  calling: (what) => `Mika зовёт: ${what}`,
  callHint: "Нужно твоё решение",
  alarmHint: "Коснись экрана — сигнал стихнет",
  alarmAcked: "Сигнал принят, ждём исправления",
  talking: "Слушаю…",
  talkingHint: "Можно перебивать",
  mainNow: "Главное сейчас:",
  oneDecision: "Одно решение:",
  // Agent names do not decline, so the name follows a dash.
  workingOn: (title, agent, duration) => `${title} — ${agent}, ${duration}`,
  nothingRunning: "Сейчас ничего не запущено",
  needsYouCount: (n) => `нужен ты — ${n} ${plural(n, "задача", "задачи", "задач")}`,
  closedToday: (n) => `Сегодня закрыто: ${n}`,
  streak: (d) => `серия ${d} ${plural(d, "день", "дня", "дней")}`,
  enableSound: "Включить звук",
  mute: "Тихо на 1 ч",
  unmute: (until) => `Тихо до ${until} · вернуть звук`,
  quietHours: "Ночь: звучат только тревоги",
  focus: "Фокус",
  unfocus: "Показать всё",
  plate: (p, l) => {
    if (p.group === "run_failed") {
      return p.count === 1 && p.detail ? `Запуск упал — ${p.detail}` : `${p.count} ${plural(p.count, "запуск упал", "запуска упали", "запусков упали")}`;
    }
    if (p.group === "need_you") return p.count === 1 ? "Нужен ты — новая задача" : `Нужен ты — ${p.count} ${plural(p.count, "задача", "задачи", "задач")}`;
    if (p.group === "stuck") return p.count === 1 ? "Задача висит больше суток" : `${p.count} ${plural(p.count, "задача висит", "задачи висят", "задач висят")} больше суток`;
    const text = ruSignal[p.signal as keyof typeof ruSignal];
    return text ? text(p, l) : p.signal;
  },
  level: (level) => (level === "warning" ? "Тревога" : "Внимание"),
  orbitLabel: (epics, n) => `Орбита: ${epics} ${plural(epics, "эпик", "эпика", "эпиков")}, ${n} ${plural(n, "агент", "агента", "агентов")} в работе`,
};

const en: OrbitCopy = {
  calm: (n) => (n === 0 ? "All quiet." : `All running. ${n} ${n === 1 ? "agent" : "agents"} at work.`),
  calmHint: "Nothing for you to do",
  calling: (what) => `Mika is calling: ${what}`,
  callHint: "Your decision is needed",
  alarmHint: "Touch the screen to silence",
  alarmAcked: "Acknowledged, waiting for the fix",
  talking: "Listening…",
  talkingHint: "You can interrupt",
  mainNow: "Main thing now:",
  oneDecision: "One decision:",
  workingOn: (title, agent, duration) => `${title} with ${agent}, ${duration}`,
  nothingRunning: "Nothing is running",
  needsYouCount: (n) => `needs you — ${n} ${n === 1 ? "issue" : "issues"}`,
  closedToday: (n) => `Closed today: ${n}`,
  streak: (d) => `${d}-day streak`,
  enableSound: "Enable sound",
  mute: "Quiet for 1 h",
  unmute: (until) => `Quiet until ${until} · sound on`,
  quietHours: "Night: warnings only",
  focus: "Focus",
  unfocus: "Show all",
  plate: (p, l) => {
    if (p.group === "run_failed") return p.count === 1 && p.detail ? `Run failed — ${p.detail}` : `${p.count} runs failed`;
    if (p.group === "need_you") return p.count === 1 ? "Needs you — new issue" : `Needs you — ${p.count} issues`;
    if (p.group === "stuck") return p.count === 1 ? "Issue stuck for a day" : `${p.count} issues stuck for a day`;
    const text = enSignal[p.signal as keyof typeof enSignal];
    return text ? text(p, l) : p.signal;
  },
  level: (level) => (level === "warning" ? "Warning" : "Caution"),
  orbitLabel: (epics, n) => `Orbit: ${epics} epics, ${n} agents at work`,
};

export const ORBIT_COPY: Record<TvLang, OrbitCopy> = { ru, en };
