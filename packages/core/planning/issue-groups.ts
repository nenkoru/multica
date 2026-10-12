import type { Issue, PlanningBucket } from "../types";
import { planningBucketOf } from "./progress";

export type PlanningGrouping = "status" | "sprint" | "epic" | "assignee";

/** Key of the group collecting issues with no value on the grouping axis. */
export const PLANNING_NO_GROUP = "__none__";

export interface PlanningIssueNode {
  issue: Issue;
  /** 0 for a row at the top of its group, 1 for its child, and so on. */
  depth: number;
}

export interface PlanningIssueGroup {
  /** Bucket, `type:id` assignee key, sprint/epic id, or PLANNING_NO_GROUP. */
  key: string;
  nodes: PlanningIssueNode[];
}

function groupKeyOf(issue: Issue, grouping: PlanningGrouping): string {
  switch (grouping) {
    case "status":
      return planningBucketOf(issue);
    case "sprint":
      return issue.sprint_id ?? PLANNING_NO_GROUP;
    case "epic":
      return issue.epic_id ?? PLANNING_NO_GROUP;
    case "assignee":
      return issue.assignee_type && issue.assignee_id
        ? `${issue.assignee_type}:${issue.assignee_id}`
        : PLANNING_NO_GROUP;
  }
}

const STATUS_GROUP_ORDER: readonly PlanningBucket[] = [
  "in_progress",
  "in_review",
  "blocked",
  "not_started",
  "done",
  "cancelled",
];

/**
 * Orders one group's issues as a parent/child tree. A child sits under its
 * parent only when the parent is in the same group; otherwise it stays a root
 * of the group it belongs to, so regrouping never hides an issue.
 */
function toTree(issues: Issue[]): PlanningIssueNode[] {
  const inGroup = new Set(issues.map((issue) => issue.id));
  const children = new Map<string, Issue[]>();
  const roots: Issue[] = [];
  for (const issue of issues) {
    const parent = issue.parent_issue_id;
    if (parent && parent !== issue.id && inGroup.has(parent)) {
      const siblings = children.get(parent);
      if (siblings) siblings.push(issue);
      else children.set(parent, [issue]);
    } else {
      roots.push(issue);
    }
  }
  const nodes: PlanningIssueNode[] = [];
  const seen = new Set<string>();
  const walk = (issue: Issue, depth: number) => {
    // A cycle in parent links must not loop; the repeat is simply skipped.
    if (seen.has(issue.id)) return;
    seen.add(issue.id);
    nodes.push({ issue, depth });
    for (const child of children.get(issue.id) ?? []) walk(child, depth + 1);
  };
  for (const root of roots) walk(root, 0);
  // Members of a parent cycle have no root above them; list them flat.
  for (const issue of issues) walk(issue, 0);
  return nodes;
}

/**
 * Groups an epic's or sprint's issues for the detail list. `order` ranks the
 * non-status groups (sprints by date, people by load); unknown keys follow in
 * first-seen order and the no-value group is always last.
 */
export function groupPlanningIssues(
  issues: Issue[],
  grouping: PlanningGrouping,
  order: readonly string[] = [],
): PlanningIssueGroup[] {
  const byKey = new Map<string, Issue[]>();
  for (const issue of issues) {
    const key = groupKeyOf(issue, grouping);
    const bucket = byKey.get(key);
    if (bucket) bucket.push(issue);
    else byKey.set(key, [issue]);
  }
  const ranked: readonly string[] = grouping === "status" ? STATUS_GROUP_ORDER : order;
  const keys = [
    ...ranked.filter((key) => key !== PLANNING_NO_GROUP && byKey.has(key)),
    ...[...byKey.keys()].filter((key) => key !== PLANNING_NO_GROUP && !ranked.includes(key)),
    ...(byKey.has(PLANNING_NO_GROUP) ? [PLANNING_NO_GROUP] : []),
  ];
  return keys.map((key) => ({ key, nodes: toTree(byKey.get(key) ?? []) }));
}

