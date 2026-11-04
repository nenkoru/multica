import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const streamKeys = {
  all: (wsId: string) => ["streams", wsId] as const,
  list: (wsId: string) => [...streamKeys.all(wsId), "list"] as const,
  multika: (wsId: string) => [...streamKeys.all(wsId), "multika"] as const,
};

// The streams API exists only on the Fabrika fork; elsewhere it 404s and the
// UI simply shows nothing, so failures are neither retried nor polled.
const pollUnlessFailed = (q: { state: { status: string } }) => (q.state.status === "error" ? false : 60_000);
export function streamListOptions(wsId: string) {
  return queryOptions({
    queryKey: streamKeys.list(wsId),
    queryFn: () => api.listStreams(),
    select: (d) => d.streams,
    retry: false,
    refetchInterval: pollUnlessFailed,
  });
}

export function multikaOptions(wsId: string) {
  return queryOptions({
    queryKey: streamKeys.multika(wsId),
    queryFn: () => api.getMultika(),
    retry: false,
    refetchInterval: pollUnlessFailed,
  });
}
