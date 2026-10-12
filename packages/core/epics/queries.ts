import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { fetchPlanningIssues } from "../issues/planning-issues";

export const epicKeys = {
  all: (wsId: string) => ["epics", wsId] as const,
  list: (wsId: string) => [...epicKeys.all(wsId), "list"] as const,
  detail: (wsId: string, id: string) =>
    [...epicKeys.all(wsId), "detail", id] as const,
  overview: (wsId: string, id: string) =>
    [...epicKeys.all(wsId), "overview", id] as const,
  issues: (wsId: string, id: string) =>
    [...epicKeys.all(wsId), "issues", id] as const,
};

export function epicListOptions(wsId: string) {
  return queryOptions({
    queryKey: epicKeys.list(wsId),
    queryFn: () => api.listEpics(undefined, wsId),
    select: (data) => data.epics,
  });
}

export function epicDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: epicKeys.detail(wsId, id),
    queryFn: () => api.getEpic(id, wsId),
  });
}

export function epicOverviewOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: epicKeys.overview(wsId, id),
    queryFn: () => api.getEpicOverview(id, wsId),
  });
}

/**
 * Every issue of the epic, for the detail page's grouped list. Keyed under
 * the epic root so the issue events and mutations that already invalidate
 * it refresh the list and the overview together.
 */
export function epicIssuesOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: epicKeys.issues(wsId, id),
    queryFn: () => fetchPlanningIssues({ epic_id: id }),
  });
}
