// @vitest-environment node
import { describe, expect, it } from "vitest";
import { TV_COPY, tvLang } from "./copy";
import { elapsedMs, epicSegments, percent, truncateTitle, tvDate, tvTime } from "./model";

describe("TV time is Moscow time", () => {
  it("formats the clock and the date in Europe/Moscow", () => {
    // 21:30 UTC is 00:30 the next day in Moscow.
    const at = new Date("2026-10-14T21:30:00Z");
    expect(tvTime(at)).toBe("00:30");
    expect(tvDate(at)).toBe("2026-10-15");
    expect(tvTime("not a date")).toBe("");
  });
});

describe("helpers", () => {
  it("truncates titles to 60 characters with an ellipsis", () => {
    const long = "Очень длинный заголовок задачи, который точно не помещается в одну строку экрана";
    const cut = truncateTitle(long);
    expect(Array.from(cut)).toHaveLength(60);
    expect(cut.endsWith("…")).toBe(true);
    expect(truncateTitle("Короткий")).toBe("Короткий");
  });

  it("measures elapsed time and ignores bad or future stamps", () => {
    const now = new Date("2026-10-14T12:00:00Z");
    expect(elapsedMs("2026-10-14T10:00:00Z", now)).toBe(2 * 3_600_000);
    expect(elapsedMs("garbage", now)).toBe(0);
    expect(elapsedMs("2026-10-15T10:00:00Z", now)).toBe(0);
  });

  it("orders epic segments as work in flight, then done", () => {
    const segments = epicSegments({
      id: "e", title: "E", in_progress: 2, in_review: 1, blocked: 3, done_in_window: 4, last_activity_at: null, issues: [],
    });
    expect(segments.map((s) => [s.key, s.count, s.tone])).toEqual([
      ["in_progress", 2, "neutral"],
      ["in_review", 1, "warning"],
      ["blocked", 3, "serious"],
      ["done", 4, "good"],
    ]);
    expect(segments.some((s) => s.tone === "critical")).toBe(false);
  });

  it("computes percent without dividing by zero", () => {
    expect(percent(1, 3)).toBe(33);
    expect(percent(0, 0)).toBe(0);
  });
});

describe("copy", () => {
  it("defaults to Russian and declines Russian plurals", () => {
    expect(tvLang(undefined)).toBe("ru");
    expect(tvLang("en")).toBe("en");
    const ru = TV_COPY.ru;
    expect(ru.sprintLeft(1)).toBe("осталось 1 день");
    expect(ru.sprintLeft(3)).toBe("осталось 3 дня");
    expect(ru.sprintLeft(5)).toBe("осталось 5 дней");
    expect(ru.sprintLeft(11)).toBe("осталось 11 дней");
    expect(ru.sprintLeft(22)).toBe("осталось 22 дня");
    expect(ru.duration(2 * 3_600_000 + 15 * 60_000)).toBe("2 ч 15 мин");
    expect(ru.duration(26 * 3_600_000)).toBe("1 д 2 ч");
    expect(TV_COPY.en.duration(5 * 60_000)).toBe("5m");
  });
});
