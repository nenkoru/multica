// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  EMPTY_TV_OVERVIEW,
  TvOverviewSchema,
  TvUnauthorizedError,
  createTvSession,
  diffTvOverview,
  fetchTvOverview,
  tvCredential,
  tvWsUrl,
} from ".";

const issue = {
  id: "i1",
  identifier: "RUSL-1",
  title: "Ship it",
  status: "in_progress",
  assignee_type: "agent",
  assignee_name: "Dev",
  since: "2026-10-14T10:00:00Z",
  last_activity_at: "2026-10-14T11:00:00Z",
  stale: false,
};

describe("TvOverviewSchema", () => {
  it("parses a full response", () => {
    const parsed = TvOverviewSchema.parse({
      ...EMPTY_TV_OVERVIEW,
      workspace_name: "Ruslan",
      sprint: { id: "s", name: "Sprint 2", status: "active", start_date: "2026-10-15", end_date: "2026-10-17", total: 9, done: 3 },
      agents_working: 2,
      epics: [{ id: "e", title: "Epic", in_progress: 1, in_review: 0, blocked: 0, done_in_window: 4, last_activity_at: null, issues: [issue] }],
      needs_owner: [issue],
      feed: [{ kind: "done", at: "2026-10-14T11:00:00Z", issue_identifier: "RUSL-1", issue_title: "Ship it", actor_name: "Dev", failure_reason: null }],
    });
    expect(parsed.sprint?.done).toBe(3);
    expect(parsed.epics[0]?.issues[0]?.assignee_name).toBe("Dev");
    expect(parsed.feed).toHaveLength(1);
  });

  it("defaults missing and malformed fields instead of failing", () => {
    const parsed = TvOverviewSchema.parse({ agents_working: "many", epics: "nope", sprint: { id: 1 } });
    expect(parsed.agents_working).toBe(0);
    expect(parsed.epics).toEqual([]);
    expect(parsed.sprint).toBeNull();
    expect(parsed.needs_owner).toEqual([]);
  });

  it("parses the prompt-cache block and hides a malformed one", () => {
    const parsed = TvOverviewSchema.parse({
      llm_cache: {
        hours: 24,
        requests: 713,
        hit_reusable: 97.08,
        hit_all: 94.92,
        target: 99.5,
        cloud_rub: 245,
        spent_rub: 233,
        saved_rub: 11,
        hourly: [{ at: "2026-10-15T12:00:00Z", requests: 6, hit_reusable: null, hit_all: 97.9 }],
        computed_at: "2026-10-15T12:47:48Z",
      },
    });
    expect(parsed.llm_cache?.hit_reusable).toBe(97.08);
    expect(parsed.llm_cache?.hourly[0]?.hit_reusable).toBeNull();
    expect(TvOverviewSchema.parse({}).llm_cache).toBeNull();
    expect(TvOverviewSchema.parse({ llm_cache: "broken" }).llm_cache).toBeNull();
  });

  it("drops feed events of an unknown kind and keeps the rest", () => {
    const parsed = TvOverviewSchema.parse({
      feed: [
        { kind: "deployed", at: "x" },
        { kind: "run_failed", at: "2026-10-14T11:00:00Z", failure_reason: "timeout" },
      ],
    });
    expect(parsed.feed.map((e) => e.kind)).toEqual(["run_failed"]);
    expect(parsed.feed[0]?.issue_identifier).toBeNull();
  });
});

describe("tvWsUrl", () => {
  it("derives from the page origin when the API is same-origin", () => {
    expect(tvWsUrl({ pageOrigin: "https://tasks.example.com" })).toBe("wss://tasks.example.com/api/tv/ws");
    expect(tvWsUrl({ pageOrigin: "http://localhost:3000" })).toBe("ws://localhost:3000/api/tv/ws");
  });
  it("follows a split API origin", () => {
    expect(tvWsUrl({ apiBaseUrl: "https://api.example.com", pageOrigin: "https://app.example.com" })).toBe(
      "wss://api.example.com/api/tv/ws",
    );
  });
  it("sits next to an explicit realtime URL", () => {
    expect(tvWsUrl({ wsUrl: "wss://api.example.com/ws", pageOrigin: "https://app.example.com" })).toBe(
      "wss://api.example.com/api/tv/ws",
    );
  });
});

describe("kiosk requests", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reports a rejected kiosk credential as TvUnauthorizedError", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 401 })));
    await expect(fetchTvOverview("")).rejects.toBeInstanceOf(TvUnauthorizedError);
  });

  it("sends the overview request with credentials", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ workspace_name: "W" }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const overview = await fetchTvOverview("https://api.example.com/");
    expect(overview.workspace_name).toBe("W");
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.com/api/tv/overview",
      expect.objectContaining({ credentials: "include" }),
    );
  });

  it("exchanges a token or a pairing code in the request body", async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(createTvSession("", tvCredential("mkt_abc"))).resolves.toBe(true);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/tv/session");
    expect(init.body).toBe(JSON.stringify({ token: "mkt_abc" }));
    await createTvSession("", tvCredential(" 0f1e2d "));
    expect((fetchMock.mock.calls[1] as unknown as [string, RequestInit])[1].body).toBe(JSON.stringify({ code: "0f1e2d" }));
    fetchMock.mockResolvedValueOnce(new Response("{}", { status: 401 }));
    await expect(createTvSession("", { code: "used" })).resolves.toBe(false);
  });
});

describe("diffTvOverview", () => {
  const base = { ...EMPTY_TV_OVERVIEW, agents_working: 1, needs_owner_total: 1, needs_owner: [issue] };
  const done = { kind: "done" as const, at: "2026-10-14T11:00:00Z", issue_identifier: "RUSL-2", issue_title: "Done", actor_name: "Dev", failure_reason: null };
  const failed = { kind: "run_failed" as const, at: "2026-10-14T11:05:00Z", issue_identifier: "RUSL-3", issue_title: "Run", actor_name: "Dev", failure_reason: "timeout" };

  it("treats the first snapshot as a baseline", () => {
    expect(diffTvOverview(null, base)).toEqual([]);
  });

  it("emits new feed events oldest first, needs-you changes and counter changes", () => {
    const next = { ...base, agents_working: 2, needs_owner_total: 0, needs_owner: [], feed: [failed, done] };
    const events = diffTvOverview(base, next);
    expect(events.map((e) => e.type)).toEqual(["issue_done", "run_failed", "needs_owner_cleared", "counters_changed"]);
  });

  it("does not repeat events already seen", () => {
    const a = { ...base, feed: [done] };
    const b = { ...base, feed: [failed, done] };
    expect(diffTvOverview(a, b).map((e) => e.type)).toEqual(["run_failed"]);
    expect(diffTvOverview(b, b)).toEqual([]);
  });
});
