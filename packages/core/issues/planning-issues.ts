import { api } from "../api";
import type { Issue } from "../types";

export const PLANNING_ISSUES_PAGE_LIMIT = 100;

/**
 * Cap on the page walk below. An epic or sprint past this size is shown
 * truncated (with the exact total) rather than fetched without bound.
 */
export const PLANNING_ISSUES_MAX = 1000;

export interface PlanningIssues {
  issues: Issue[];
  /** Server total; larger than `issues.length` when the list was truncated. */
  total: number;
}

/** Walks the issue list API for one epic or sprint until its total is reached. */
export async function fetchPlanningIssues(
  filter: { epic_id: string } | { sprint_id: string },
): Promise<PlanningIssues> {
  const issues: Issue[] = [];
  let total = 0;
  for (let offset = 0; offset < PLANNING_ISSUES_MAX; offset += PLANNING_ISSUES_PAGE_LIMIT) {
    const res = await api.listIssues({ ...filter, limit: PLANNING_ISSUES_PAGE_LIMIT, offset });
    issues.push(...res.issues);
    total = Math.max(res.total, issues.length);
    if (res.issues.length < PLANNING_ISSUES_PAGE_LIMIT || issues.length >= res.total) break;
  }
  return { issues, total };
}
