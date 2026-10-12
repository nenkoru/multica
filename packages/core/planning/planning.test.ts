// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { Issue, PlanningProgress } from "../types";
import {
  PLANNING_NO_GROUP,
  groupPlanningIssues,
  isPlanningIssueOpen,
  planningBucketOf,
  planningPercentDone,
  planningSegments,
  resolvePlanningProgress,
  sprintTimeline,
} from "./index";

function issue(id: string, over: Record<string, unknown> = {}): Issue {
  return {
    id,
    status: "todo",
    parent_issue_id: null,
    assignee_type: null,
    assignee_id: null,
    epic_id: null,
    sprint_id: null,
    ...over,
  } as Issue;
}

const progress = (over: Partial<PlanningProgress>): PlanningProgress => ({
  total: 0, done: 0, in_progress: 0, in_review: 0, blocked: 0, not_started: 0, cancelled: 0,
  ...over,
});

describe("planning buckets", () => {
  it("maps every built-in status to its own bucket", () => {
    const cases: Record<string, string> = {
      backlog: "not_started",
      todo: "not_started",
      in_progress: "in_progress",
      in_review: "in_review",
      blocked: "blocked",
      done: "done",
      cancelled: "cancelled",
    };
    for (const [status, bucket] of Object.entries(cases)) {
      expect(planningBucketOf(issue("i", { status }))).toBe(bucket);
    }
  });

  it("classifies a custom status by its lifecycle category", () => {
    expect(planningBucketOf(issue("i", { status: "qa", status_category: "started" }))).toBe("in_progress");
    expect(planningBucketOf(issue("i", { status: "shipped", status_category: "done" }))).toBe("done");
    expect(planningBucketOf(issue("i", { status: "wontfix", status_category: "closed" }))).toBe("cancelled");
    expect(planningBucketOf(issue("i", { status: "triage", status_category: "unstarted" }))).toBe("not_started");
    // An unresolved custom key is not silently counted as finished.
    expect(planningBucketOf(issue("i", { status: "mystery" }))).toBe("not_started");
  });

  it("treats done and cancelled as closed, everything else as open", () => {
    expect(isPlanningIssueOpen(issue("i", { status: "blocked" }))).toBe(true);
    expect(isPlanningIssueOpen(issue("i", { status: "in_review" }))).toBe(true);
    expect(isPlanningIssueOpen(issue("i", { status: "done" }))).toBe(false);
    expect(isPlanningIssueOpen(issue("i", { status: "cancelled" }))).toBe(false);
  });
});

describe("planning progress", () => {
  it("leaves cancelled issues out of the percent base", () => {
    expect(planningPercentDone(progress({ total: 10, done: 4, cancelled: 2, not_started: 4 }))).toBe(50);
    expect(planningPercentDone(progress({ total: 3, cancelled: 3 }))).toBe(0);
    expect(planningPercentDone(progress({}))).toBe(0);
  });

  it("returns only non-empty segments whose widths fill the bar", () => {
    const segments = planningSegments(progress({ total: 8, done: 2, in_review: 1, blocked: 1, not_started: 4 }));
    expect(segments.map((s) => s.bucket)).toEqual(["done", "in_review", "blocked", "not_started"]);
    expect(segments.reduce((sum, s) => sum + s.percent, 0)).toBeCloseTo(100);
    expect(planningSegments(progress({}))).toEqual([]);
  });

  it("uses the server breakdown when present and degrades to the count pair otherwise", () => {
    const server = progress({ total: 3, done: 1, blocked: 2 });
    expect(resolvePlanningProgress({ issue_count: 3, done_count: 1, progress: server })).toBe(server);
    // Older server: no breakdown, or an all-zero default beside real counts.
    expect(resolvePlanningProgress({ issue_count: 5, done_count: 2 })).toEqual(
      progress({ total: 5, done: 2, not_started: 3 }),
    );
    expect(resolvePlanningProgress({ issue_count: 5, done_count: 2, progress: progress({}) })).toEqual(
      progress({ total: 5, done: 2, not_started: 3 }),
    );
    expect(resolvePlanningProgress({ issue_count: 0, done_count: 0, progress: progress({}) }).total).toBe(0);
  });
});

describe("sprint timeline", () => {
  it("counts both ends as sprint days", () => {
    expect(sprintTimeline("2026-10-05", "2026-10-12", "2026-10-05")).toEqual({ phase: "running", totalDays: 8, day: 1, daysLeft: 7 });
    expect(sprintTimeline("2026-10-05", "2026-10-12", "2026-10-12")).toEqual({ phase: "running", totalDays: 8, day: 8, daysLeft: 0 });
  });

  it("reports the time before the start and after the end", () => {
    expect(sprintTimeline("2026-10-05", "2026-10-12", "2026-10-02")).toEqual({ phase: "upcoming", totalDays: 8, startsInDays: 3 });
    expect(sprintTimeline("2026-10-05", "2026-10-12", "2026-10-14")).toEqual({ phase: "over", totalDays: 8, daysOver: 2 });
  });

  it("does not shift across a DST change or a month boundary", () => {
    expect(sprintTimeline("2026-03-27", "2026-04-02", "2026-03-30")).toEqual({ phase: "running", totalDays: 7, day: 4, daysLeft: 3 });
  });

  it("rejects malformed or reversed dates", () => {
    expect(sprintTimeline("", "2026-10-12", "2026-10-06")).toBeNull();
    expect(sprintTimeline("2026-10-12", "2026-10-05", "2026-10-06")).toBeNull();
  });
});

describe("planning issue groups", () => {
  it("orders status groups by what needs work first and skips empty ones", () => {
    const groups = groupPlanningIssues(
      [issue("a", { status: "done" }), issue("b", { status: "todo" }), issue("c", { status: "blocked" })],
      "status",
    );
    expect(groups.map((g) => g.key)).toEqual(["blocked", "not_started", "done"]);
  });

  it("nests a child under its parent inside one group", () => {
    const groups = groupPlanningIssues(
      [
        issue("child", { parent_issue_id: "parent" }),
        issue("parent"),
        issue("grandchild", { parent_issue_id: "child" }),
        issue("other"),
      ],
      "status",
    );
    expect(groups).toHaveLength(1);
    expect(groups[0]!.nodes.map((n) => [n.issue.id, n.depth])).toEqual([
      ["parent", 0],
      ["child", 1],
      ["grandchild", 2],
      ["other", 0],
    ]);
  });

  it("keeps a child visible as a root when its parent is in another group or absent", () => {
    const groups = groupPlanningIssues(
      [
        issue("parent", { status: "done" }),
        issue("child", { parent_issue_id: "parent", status: "todo" }),
        issue("orphan", { parent_issue_id: "elsewhere", status: "todo" }),
      ],
      "status",
    );
    const open = groups.find((g) => g.key === "not_started")!;
    expect(open.nodes.map((n) => [n.issue.id, n.depth])).toEqual([["child", 0], ["orphan", 0]]);
  });

  it("never drops or repeats an issue caught in a parent cycle", () => {
    const groups = groupPlanningIssues(
      [issue("a", { parent_issue_id: "b" }), issue("b", { parent_issue_id: "a" }), issue("self", { parent_issue_id: "self" })],
      "status",
    );
    expect(groups[0]!.nodes.map((n) => n.issue.id).sort()).toEqual(["a", "b", "self"]);
  });

  it("ranks sprint groups by the given order and puts the no-sprint group last", () => {
    const groups = groupPlanningIssues(
      [
        issue("none"),
        issue("late", { sprint_id: "s2" }),
        issue("early", { sprint_id: "s1" }),
        issue("unranked", { sprint_id: "s9" }),
      ],
      "sprint",
      ["s1", "s2"],
    );
    expect(groups.map((g) => g.key)).toEqual(["s1", "s2", "s9", PLANNING_NO_GROUP]);
  });

  it("groups by the full assignee identity, not the id alone", () => {
    const groups = groupPlanningIssues(
      [
        issue("m", { assignee_type: "member", assignee_id: "x" }),
        issue("a", { assignee_type: "agent", assignee_id: "x" }),
        issue("u"),
        issue("half", { assignee_type: "member", assignee_id: null }),
      ],
      "assignee",
    );
    expect(groups.map((g) => [g.key, g.nodes.length])).toEqual([
      ["member:x", 1],
      ["agent:x", 1],
      [PLANNING_NO_GROUP, 2],
    ]);
  });

  it("groups a sprint's issues by epic", () => {
    const groups = groupPlanningIssues([issue("e", { epic_id: "e1" }), issue("n")], "epic");
    expect(groups.map((g) => g.key)).toEqual(["e1", PLANNING_NO_GROUP]);
  });
});
