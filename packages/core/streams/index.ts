import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export interface StreamView {
  id: string;
  slug: string;
  title: string;
  state: string;
  state_label: string;
  reason: string;
  until?: string | null;
  summary: string;
  max_parallel: number;
  active: boolean;
  last_milestone_at?: string | null;
  charter?: { goal?: string } & Record<string, unknown>;
}

export interface StreamStep {
  id: string;
  seq: number;
  kind: string;
  status: string;
  brief: string;
  last_error?: string;
  attempt?: number;
  max_attempts?: number;
}

export interface StreamAsk {
  id: string;
  kind: string;
  text: string;
  status: string;
  created_at?: string;
}

export interface StreamMilestone {
  id: string;
  text: string;
  created_at: string;
}

export interface StreamDetail {
  stream: StreamView;
  steps: StreamStep[];
  asks: StreamAsk[];
  milestones: StreamMilestone[];
}

export const streamKeys = {
  all: (wsId: string) => ["streams", wsId] as const,
  list: (wsId: string) => [...streamKeys.all(wsId), "list"] as const,
  detail: (wsId: string, slug: string) => [...streamKeys.all(wsId), "detail", slug] as const,
};

export function streamListOptions(wsId: string) {
  return queryOptions({
    queryKey: streamKeys.list(wsId),
    queryFn: async () => ((await api.listStreams()).streams ?? []) as StreamView[],
    refetchInterval: 15000,
  });
}

export function streamDetailOptions(wsId: string, slug: string) {
  return queryOptions({
    queryKey: streamKeys.detail(wsId, slug),
    queryFn: async () => {
      const d = (await api.getStream(slug)) as Partial<StreamDetail>;
      return {
        stream: d.stream as StreamView,
        steps: d.steps ?? [],
        asks: d.asks ?? [],
        milestones: d.milestones ?? [],
      } as StreamDetail;
    },
    refetchInterval: 15000,
  });
}
