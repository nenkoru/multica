import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { fetchPlanningIssues } from "../issues/planning-issues";

export const sprintKeys = {
  all: (wsId: string) => ["sprints", wsId] as const,
  list: (wsId: string) => [...sprintKeys.all(wsId), "list"] as const,
  detail: (wsId: string, id: string) =>
    [...sprintKeys.all(wsId), "detail", id] as const,
  overview: (wsId: string, id: string) =>
    [...sprintKeys.all(wsId), "overview", id] as const,
  issues: (wsId: string, id: string) =>
    [...sprintKeys.all(wsId), "issues", id] as const,
};

export function sprintListOptions(wsId: string) {
  return queryOptions({
    queryKey: sprintKeys.list(wsId),
    queryFn: () => api.listSprints(undefined, wsId),
    select: (data) => data.sprints,
  });
}

export function sprintDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: sprintKeys.detail(wsId, id),
    queryFn: () => api.getSprint(id, wsId),
  });
}

export function sprintOverviewOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: sprintKeys.overview(wsId, id),
    queryFn: () => api.getSprintOverview(id, wsId),
  });
}

/**
 * Every issue of the sprint, for the detail page's grouped list. Keyed under
 * the sprint root so the issue events and mutations that already invalidate
 * it refresh the list and the overview together.
 */
export function sprintIssuesOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: sprintKeys.issues(wsId, id),
    queryFn: () => fetchPlanningIssues({ sprint_id: id }),
  });
}
