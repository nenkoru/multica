import { queryOptions } from "@tanstack/react-query";
import { parseWithFallback } from "../api/schema";
import { EMPTY_TV_OVERVIEW, TvOverviewSchema, type TvOverview } from "./schema";

// The TV dashboard authenticates with a kiosk token, not a member session, so
// it talks to the server without the session-bound ApiClient: a 401 here
// means "pair this screen again", never "log the user out".

/** A kiosk request the server rejected for its credential. */
export class TvUnauthorizedError extends Error {
  constructor() {
    super("kiosk token missing, invalid or revoked");
    this.name = "TvUnauthorizedError";
  }
}

function tvUrl(baseUrl: string, path: string): string {
  return `${baseUrl.replace(/\/+$/, "")}${path}`;
}

export async function fetchTvOverview(baseUrl: string): Promise<TvOverview> {
  const res = await fetch(tvUrl(baseUrl, "/api/tv/overview"), {
    credentials: "include",
    cache: "no-store",
  });
  if (res.status === 401) throw new TvUnauthorizedError();
  if (!res.ok) throw new Error(`GET /api/tv/overview: ${res.status}`);
  return parseWithFallback(await res.json(), TvOverviewSchema, EMPTY_TV_OVERVIEW, {
    endpoint: "GET /api/tv/overview",
  });
}

/** Trades a kiosk token for the kiosk cookie. Resolves false when rejected. */
export async function createTvSession(baseUrl: string, token: string): Promise<boolean> {
  const res = await fetch(tvUrl(baseUrl, "/api/tv/session"), {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }),
  });
  if (res.status === 204) return true;
  if (res.status === 401 || res.status === 400) return false;
  throw new Error(`POST /api/tv/session: ${res.status}`);
}

export async function deleteTvSession(baseUrl: string): Promise<void> {
  await fetch(tvUrl(baseUrl, "/api/tv/session"), { method: "DELETE", credentials: "include" });
}

/** Fallback refresh when the realtime signal is silent. */
export const TV_POLL_INTERVAL_MS = 60_000;

export const tvKeys = {
  overview: (baseUrl: string) => ["tv", "overview", baseUrl] as const,
};

export function tvOverviewOptions(baseUrl: string) {
  return queryOptions({
    queryKey: tvKeys.overview(baseUrl),
    queryFn: () => fetchTvOverview(baseUrl),
    refetchInterval: TV_POLL_INTERVAL_MS,
    refetchIntervalInBackground: true,
    // Keep the last good picture on screen while refetching or offline.
    placeholderData: (previous) => previous,
    retry: (failures, error) => !(error instanceof TvUnauthorizedError) && failures < 3,
  });
}

/**
 * The kiosk WebSocket URL. It sits next to the regular realtime endpoint:
 * an explicit `.../ws` URL keeps its origin, otherwise the API base (or the
 * page origin when the API is same-origin) is used.
 */
export function tvWsUrl(opts: { wsUrl?: string; apiBaseUrl?: string; pageOrigin: string }): string {
  if (opts.wsUrl) {
    const u = new URL(opts.wsUrl, opts.pageOrigin);
    u.pathname = u.pathname.replace(/\/ws\/?$/, "") + "/api/tv/ws";
    u.search = "";
    return u.toString();
  }
  const u = new URL(opts.apiBaseUrl || opts.pageOrigin, opts.pageOrigin);
  u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
  u.pathname = u.pathname.replace(/\/+$/, "") + "/api/tv/ws";
  u.search = "";
  return u.toString();
}
