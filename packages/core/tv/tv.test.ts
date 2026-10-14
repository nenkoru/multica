// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  EMPTY_TV_OVERVIEW,
  TvOverviewSchema,
  TvUnauthorizedError,
  createTvSession,
  fetchTvOverview,
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

  it("exchanges a token in the request body", async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(createTvSession("", "mkt_abc")).resolves.toBe(true);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/tv/session");
    expect(init.body).toBe(JSON.stringify({ token: "mkt_abc" }));
    fetchMock.mockResolvedValueOnce(new Response("{}", { status: 401 }));
    await expect(createTvSession("", "mkt_bad")).resolves.toBe(false);
  });
});
