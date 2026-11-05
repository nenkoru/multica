import { queryOptions } from "@tanstack/react-query";
import { parseTVGame, parseTVHealth, type TVGame, type TVHealth } from "../api/tv-game-schemas";
import { TvUnauthorizedError } from "./client";

// The Orbit screen's feeds (RUSL-1786): /api/tv/game and /api/tv/health,
// with the same kiosk cookie as the overview. They poll only while the page
// is in the foreground: a phone in a pocket does not wake up every minute.

async function getTv<T>(baseUrl: string, path: string, parse: (data: unknown) => T): Promise<T> {
  const res = await fetch(`${baseUrl.replace(/\/+$/, "")}${path}`, { credentials: "include", cache: "no-store" });
  if (res.status === 401) throw new TvUnauthorizedError();
  if (!res.ok) throw new Error(`GET ${path}: ${res.status}`);
  return parse(await res.json());
}

export const fetchTvGame = (baseUrl: string): Promise<TVGame> => getTv(baseUrl, "/api/tv/game", parseTVGame);
export const fetchTvHealth = (baseUrl: string): Promise<TVHealth> => getTv(baseUrl, "/api/tv/health", parseTVHealth);

const TV_GAME_POLL_MS = 60_000;
const TV_HEALTH_POLL_MS = 30_000;

export const tvGameKeys = {
  game: (baseUrl: string) => ["tv", "game", baseUrl] as const,
  health: (baseUrl: string) => ["tv", "health", baseUrl] as const,
};

const common = {
  refetchIntervalInBackground: false,
  retry: (failures: number, error: unknown) => !(error instanceof TvUnauthorizedError) && failures < 3,
};

export function tvGameOptions(baseUrl: string) {
  return queryOptions({
    ...common,
    queryKey: tvGameKeys.game(baseUrl),
    queryFn: () => fetchTvGame(baseUrl),
    refetchInterval: TV_GAME_POLL_MS,
  });
}

export function tvHealthOptions(baseUrl: string) {
  return queryOptions({
    ...common,
    queryKey: tvGameKeys.health(baseUrl),
    queryFn: () => fetchTvHealth(baseUrl),
    // Health drives the warnings: a little more often than the work feed.
    refetchInterval: TV_HEALTH_POLL_MS,
  });
}
