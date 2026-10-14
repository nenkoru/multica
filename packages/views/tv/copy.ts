// The TV screen is read from across the room by the workspace owner, so its
// copy is chosen per screen (?lang=) rather than per signed-in user: there is
// no signed-in user on a kiosk. Russian is the default; the app's own locale
// set has no Russian, which is why this copy lives beside the screen.

export type TvLang = "ru" | "en";

export interface TvCopy {
  agentsWorking: string;
  waitingOwner: string;
  stale: string;
  noSprint: string;
  sprintDay: (day: number, total: number) => string;
  sprintLeft: (days: number) => string;
  sprintLastDay: string;
  sprintOver: (days: number) => string;
  sprintUpcoming: (days: number) => string;
  done: (done: number, total: number) => string;
  inProgress: string;
  inReview: string;
  blocked: string;
  doneInSprint: string;
  doneInWeek: string;
  moreInProgress: (n: number) => string;
  noEpic: (n: number) => string;
  moreEpics: (n: number) => string;
  noEpics: string;
  needsYou: string;
  needsYouMore: (n: number) => string;
  needsYouEmpty: string;
  needsYouNoLabel: string;
  waitingFor: (duration: string) => string;
  feedTitle: string;
  feedEmpty: string;
  feedDone: string;
  feedRunFailed: string;
  unassigned: string;
  offline: string;
  pairTitle: string;
  pairHint: string;
  pairPlaceholder: string;
  pairSubmit: string;
  pairRejected: string;
  loading: string;
  timeZone: string;
  duration: (ms: number) => string;
}

function plural(n: number, one: string, few: string, many: string): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod10 === 1 && mod100 !== 11) return one;
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return few;
  return many;
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

function splitDuration(ms: number): { d: number; h: number; m: number } {
  const safe = Math.max(0, ms);
  return { d: Math.floor(safe / DAY), h: Math.floor((safe % DAY) / HOUR), m: Math.floor((safe % HOUR) / MINUTE) };
}

const ru: TvCopy = {
  agentsWorking: "Агенты работают",
  waitingOwner: "Ждут Руслана",
  stale: "Застряли > 24 ч",
  noSprint: "Спринт не идёт",
  sprintDay: (day, total) => `день ${day} из ${total}`,
  sprintLeft: (days) => `осталось ${days} ${plural(days, "день", "дня", "дней")}`,
  sprintLastDay: "последний день",
  sprintOver: (days) => `закончился ${days} ${plural(days, "день", "дня", "дней")} назад`,
  sprintUpcoming: (days) => `начнётся через ${days} ${plural(days, "день", "дня", "дней")}`,
  done: (done, total) => `готово ${done} из ${total}`,
  inProgress: "в работе",
  inReview: "на ревью",
  blocked: "blocked",
  doneInSprint: "готово за спринт",
  doneInWeek: "готово за неделю",
  moreInProgress: (n) => `+ ещё ${n} в работе`,
  noEpic: (n) => `Без эпика: ${n}`,
  moreEpics: (n) => `ещё ${n} ${plural(n, "эпик", "эпика", "эпиков")} с активностью`,
  noEpics: "Активных эпиков нет",
  needsYou: "Нужен ты",
  needsYouMore: (n) => `и ещё ${n}`,
  needsYouEmpty: "Ничего не ждёт",
  needsYouNoLabel: "Метка «нужен владелец» не задана у этого экрана",
  waitingFor: (duration) => `ждёт ${duration}`,
  feedTitle: "За 24 ч",
  feedEmpty: "Событий за сутки нет",
  feedDone: "готово",
  feedRunFailed: "запуск упал",
  unassigned: "без исполнителя",
  offline: "нет связи — показаны последние данные",
  pairTitle: "Подключение экрана",
  pairHint: "Отсканируйте QR-код или откройте одноразовую ссылку из «multica kiosk-token pair». Можно вставить код из ссылки или kiosk-токен.",
  pairPlaceholder: "код или mkt_…",
  pairSubmit: "Подключить",
  pairRejected: "Не принято: ссылка уже использована или устарела, либо токен отозван",
  loading: "Загрузка…",
  timeZone: "МСК",
  duration: (ms) => {
    const { d, h, m } = splitDuration(ms);
    if (d > 0) return h > 0 ? `${d} д ${h} ч` : `${d} д`;
    if (h > 0) return m > 0 ? `${h} ч ${m} мин` : `${h} ч`;
    return `${m} мин`;
  },
};

const en: TvCopy = {
  agentsWorking: "Agents working",
  waitingOwner: "Waiting for owner",
  stale: "Stuck > 24 h",
  noSprint: "No sprint running",
  sprintDay: (day, total) => `day ${day} of ${total}`,
  sprintLeft: (days) => `${days} ${days === 1 ? "day" : "days"} left`,
  sprintLastDay: "last day",
  sprintOver: (days) => `ended ${days} ${days === 1 ? "day" : "days"} ago`,
  sprintUpcoming: (days) => `starts in ${days} ${days === 1 ? "day" : "days"}`,
  done: (done, total) => `${done} of ${total} done`,
  inProgress: "in progress",
  inReview: "in review",
  blocked: "blocked",
  doneInSprint: "done this sprint",
  doneInWeek: "done this week",
  moreInProgress: (n) => `+ ${n} more in progress`,
  noEpic: (n) => `No epic: ${n}`,
  moreEpics: (n) => `${n} more active ${n === 1 ? "epic" : "epics"}`,
  noEpics: "No active epics",
  needsYou: "Needs you",
  needsYouMore: (n) => `and ${n} more`,
  needsYouEmpty: "Nothing is waiting",
  needsYouNoLabel: "This screen has no owner label configured",
  waitingFor: (duration) => `waiting ${duration}`,
  feedTitle: "Last 24 h",
  feedEmpty: "No events in the last day",
  feedDone: "done",
  feedRunFailed: "run failed",
  unassigned: "unassigned",
  offline: "offline — showing the last data",
  pairTitle: "Pair this screen",
  pairHint: "Scan the QR code or open the one-time link from \"multica kiosk-token pair\". You can also paste the code from the link or a kiosk token.",
  pairPlaceholder: "code or mkt_…",
  pairSubmit: "Pair",
  pairRejected: "Rejected: the link was already used or expired, or the token was revoked",
  loading: "Loading…",
  timeZone: "MSK",
  duration: (ms) => {
    const { d, h, m } = splitDuration(ms);
    if (d > 0) return h > 0 ? `${d}d ${h}h` : `${d}d`;
    if (h > 0) return m > 0 ? `${h}h ${m}m` : `${h}h`;
    return `${m}m`;
  },
};

export const TV_COPY: Record<TvLang, TvCopy> = { ru, en };

export function tvLang(value: string | null | undefined): TvLang {
  return value === "en" ? "en" : "ru";
}
