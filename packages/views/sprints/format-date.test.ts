import { afterEach, describe, expect, it, vi } from "vitest";
import { formatSprintDate } from "./format-date";

afterEach(() => vi.unstubAllEnvs());

describe("sprint calendar dates", () => {
  it.each(["UTC", "America/Los_Angeles", "Europe/Moscow"])(
    "keeps October 9–16 in %s", (zone) => {
      vi.stubEnv("TZ", zone);
      expect(formatSprintDate("2026-10-09")).toBe("Oct 9");
      expect(formatSprintDate("2026-10-16")).toBe("Oct 16");
    },
  );
});
