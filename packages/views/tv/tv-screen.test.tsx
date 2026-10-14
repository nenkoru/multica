import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { EMPTY_TV_OVERVIEW, type TvIssue, type TvOverview } from "@multica/core/tv";
import { TV_COPY } from "./copy";
import { TvDashboard, TvScreen } from "./tv-screen";

const now = new Date("2026-10-16T09:00:00Z");

function issue(n: number, over: Partial<TvIssue> = {}): TvIssue {
  return {
    id: `i${n}`,
    identifier: `RUSL-${n}`,
    title: `Задача ${n}`,
    status: "in_progress",
    assignee_type: "agent",
    assignee_name: "Разработчик",
    since: "2026-10-16T06:45:00Z",
    last_activity_at: "2026-10-16T08:50:00Z",
    stale: false,
    ...over,
  };
}

const overview: TvOverview = {
  ...EMPTY_TV_OVERVIEW,
  workspace_name: "Ruslan",
  owner_label: "Нужен Руслан",
  sprint: { id: "s", name: "Спринт 2", status: "active", start_date: "2026-10-15", end_date: "2026-10-17", total: 10, done: 4 },
  agents_working: 3,
  needs_owner_total: 6,
  stale_total: 2,
  no_epic_active: 7,
  epics_hidden: 2,
  epics: [
    {
      id: "e1",
      title: "ТВ-режим",
      in_progress: 5,
      in_review: 1,
      blocked: 1,
      done_in_window: 2,
      last_activity_at: "2026-10-16T08:50:00Z",
      issues: [issue(1), issue(2), issue(3, { stale: true, title: "Старая задача" })],
    },
  ],
  needs_owner: [issue(10, { status: "blocked", title: "Нужно решение по бюджету", since: "2026-10-13T09:00:00Z" })],
  feed: [
    { kind: "run_failed", at: "2026-10-16T08:40:00Z", issue_identifier: "RUSL-2", issue_title: "Задача 2", actor_name: "Разработчик", failure_reason: "timeout" },
    { kind: "done", at: "2026-10-16T08:30:00Z", issue_identifier: "RUSL-4", issue_title: "Готовая", actor_name: null, failure_reason: null },
  ],
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("TvDashboard", () => {
  it("shows the sprint, the three counters and the clock in Moscow time", () => {
    render(<TvDashboard data={overview} copy={TV_COPY.ru} now={now} offline={false} />);
    expect(screen.getByText("Спринт 2")).toBeTruthy();
    expect(screen.getByText("день 2 из 3 · осталось 1 день")).toBeTruthy();
    expect(screen.getByLabelText("Агенты работают: 3")).toBeTruthy();
    expect(screen.getByLabelText("Ждут Руслана: 6")).toBeTruthy();
    expect(screen.getByLabelText("Застряли > 24 ч: 2")).toBeTruthy();
    // One clock per layout; CSS shows the portrait or the landscape one.
    expect(screen.getAllByText("12:00", { exact: false })).toHaveLength(2);
  });

  it("lists at most the epic's three issues, folds the rest into numbers and dims stale work", () => {
    render(<TvDashboard data={overview} copy={TV_COPY.ru} now={now} offline={false} />);
    const epic = screen.getByRole("heading", { name: "ТВ-режим" }).closest("section")!;
    expect(within(epic).getAllByRole("listitem")).toHaveLength(4);
    expect(within(epic).getByText("+ ещё 2 в работе")).toBeTruthy();
    expect(within(epic).getAllByText("Разработчик · 2 ч 15 мин")).toHaveLength(3);
    expect(within(epic).getByText("Старая задача").closest("li")!.className).toContain("opacity-45");
    expect(within(epic).getByText("2 готово за спринт")).toBeTruthy();
    expect(screen.getByText("Без эпика: 7")).toBeTruthy();
    expect(screen.getByText("ещё 2 эпика с активностью")).toBeTruthy();
  });

  it("puts owner-blocked work in the needs-you column, oldest wait shown", () => {
    render(<TvDashboard data={overview} copy={TV_COPY.ru} now={now} offline={false} />);
    const column = screen.getByRole("complementary", { name: "Нужен ты" });
    expect(within(column).getByText("Нужно решение по бюджету")).toBeTruthy();
    expect(within(column).getByText("ждёт 3 д")).toBeTruthy();
    expect(within(column).getByText("и ещё 5")).toBeTruthy();
  });

  it("explains an unconfigured owner label instead of claiming nothing waits", () => {
    render(<TvDashboard data={{ ...overview, owner_label: "", needs_owner: [], needs_owner_total: 0 }} copy={TV_COPY.ru} now={now} offline={false} />);
    expect(screen.getByText(TV_COPY.ru.needsYouNoLabel)).toBeTruthy();
  });

  it("shows the feed with labelled icons", () => {
    render(<TvDashboard data={overview} copy={TV_COPY.ru} now={now} offline={false} />);
    expect(screen.getByLabelText("запуск упал")).toBeTruthy();
    expect(screen.getByLabelText("готово")).toBeTruthy();
    expect(screen.getByText("11:40")).toBeTruthy();
  });
});

describe("TvScreen pairing", () => {
  function renderScreen() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
      <QueryClientProvider client={client}>
        <TvScreen apiBaseUrl="" lang="ru" />
      </QueryClientProvider>,
    );
  }

  it("asks for a token when the server rejects the kiosk credential, then loads", async () => {
    let paired = false;
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      if (url === "/api/tv/session" && init?.method === "POST") {
        paired = JSON.parse(String(init.body)).token === "mkt_good";
        return new Response(null, { status: paired ? 204 : 401 });
      }
      if (!paired) return new Response("{}", { status: 401 });
      return new Response(JSON.stringify(overview), { status: 200 });
    });
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("WebSocket", class { close() {} } as unknown as typeof WebSocket);

    renderScreen();
    const input = await screen.findByLabelText("Подключение экрана");
    fireEvent.change(input, { target: { value: "mkt_bad" } });
    fireEvent.click(screen.getByRole("button", { name: "Подключить" }));
    expect(await screen.findByRole("alert")).toBeTruthy();

    fireEvent.change(input, { target: { value: "mkt_good" } });
    fireEvent.click(screen.getByRole("button", { name: "Подключить" }));
    await waitFor(() => expect(screen.getByText("Спринт 2")).toBeTruthy());
    // The token went in a POST body, never in a URL.
    for (const [url] of fetchMock.mock.calls) expect(String(url)).not.toContain("mkt_");
  });
});

describe("TvScreen one-time link and event stream", () => {
  it("redeems a #pair= code once, clears it from the address and reports changes", async () => {
    window.history.replaceState(null, "", "/tv#pair=abc123");
    let paired = false;
    let snapshot: TvOverview = overview;
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      if (url === "/api/tv/session") {
        paired = JSON.parse(String(init?.body)).code === "abc123";
        return new Response(null, { status: paired ? 204 : 401 });
      }
      return paired ? new Response(JSON.stringify(snapshot), { status: 200 }) : new Response("{}", { status: 401 });
    });
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("WebSocket", class { close() {} } as unknown as typeof WebSocket);
    const onEvents = vi.fn();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <TvScreen apiBaseUrl="" lang="ru" onEvents={onEvents} layer={(o) => <div>карта {o.agents_working}</div>} />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(screen.getByText("карта 3")).toBeTruthy());
    expect(window.location.hash).toBe("");
    expect(fetchMock.mock.calls.filter(([u]) => u === "/api/tv/session")).toHaveLength(1);

    snapshot = { ...overview, agents_working: 4 };
    await client.invalidateQueries();
    await waitFor(() => expect(onEvents).toHaveBeenCalled());
    expect(onEvents.mock.calls[0]![0]).toEqual([
      expect.objectContaining({ type: "counters_changed", to: expect.objectContaining({ agents_working: 4 }) }),
    ]);
  });
});
