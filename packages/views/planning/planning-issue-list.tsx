"use client";

import { useMemo } from "react";
import { CornerDownRight, Timer } from "lucide-react";
import type { Epic, Issue, PlanningBucket, Sprint } from "@multica/core/types";
import {
  PLANNING_NO_GROUP,
  groupPlanningIssues,
  type PlanningGrouping,
  type PlanningIssueGroup,
} from "@multica/core/planning";
import { issueStatusCategory } from "@multica/core/issues";
import { formatDateOnly } from "@multica/core/issues/date";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useActorName } from "@multica/core/workspace/hooks";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../common/actor-avatar";
import { PriorityIcon } from "../issues/components/priority-icon";
import { StatusIcon } from "../issues/components/status-icon";
import { AppLink } from "../navigation";
import { useLocale, useT } from "../i18n";
import { PLANNING_BUCKET_COLOR } from "./planning-progress";

const TREE_INDENT_PX = 20;

function compareByCountDesc(a: [string, number], b: [string, number]) {
  return b[1] - a[1] || a[0].localeCompare(b[0]);
}

/** Rank of the non-status groups: sprints by date, epics as listed, people by load. */
function groupOrder(
  grouping: PlanningGrouping,
  issues: Issue[],
  epics: Epic[],
  sprints: Sprint[],
): string[] {
  switch (grouping) {
    case "sprint":
      return [...sprints]
        .sort((a, b) => a.start_date.localeCompare(b.start_date) || a.id.localeCompare(b.id))
        .map((sprint) => sprint.id);
    case "epic":
      return epics.map((epic) => epic.id);
    case "assignee": {
      const counts = new Map<string, number>();
      for (const issue of issues) {
        if (!issue.assignee_type || !issue.assignee_id) continue;
        const key = `${issue.assignee_type}:${issue.assignee_id}`;
        counts.set(key, (counts.get(key) ?? 0) + 1);
      }
      return [...counts.entries()].sort(compareByCountDesc).map(([key]) => key);
    }
    case "status":
      return [];
  }
}

export interface PlanningIssueListProps {
  issues: Issue[];
  grouping: PlanningGrouping;
  /** The other planning axis, shown as a chip on every row. */
  relation: "sprint" | "epic";
  epics: Epic[];
  sprints: Sprint[];
}

/**
 * Grouped issue list of an epic or sprint page. Inside a group, sub-issues are
 * indented under their parent.
 */
export function PlanningIssueList({ issues, grouping, relation, epics, sprints }: PlanningIssueListProps) {
  const epicById = useMemo(() => new Map(epics.map((epic) => [epic.id, epic])), [epics]);
  const sprintById = useMemo(() => new Map(sprints.map((sprint) => [sprint.id, sprint])), [sprints]);
  const groups = useMemo(
    () => groupPlanningIssues(issues, grouping, groupOrder(grouping, issues, epics, sprints)),
    [issues, grouping, epics, sprints],
  );
  return (
    <div className="overflow-hidden rounded-md border">
      {groups.map((group) => (
        <section key={group.key} data-group={group.key}>
          <GroupHeader group={group} grouping={grouping} epicById={epicById} sprintById={sprintById} />
          <ul>
            {group.nodes.map(({ issue, depth }) => (
              <li key={issue.id}>
                <IssueRow
                  issue={issue}
                  depth={depth}
                  relation={relation}
                  epicById={epicById}
                  sprintById={sprintById}
                />
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

function GroupHeader({
  group,
  grouping,
  epicById,
  sprintById,
}: {
  group: PlanningIssueGroup;
  grouping: PlanningGrouping;
  epicById: Map<string, Epic>;
  sprintById: Map<string, Sprint>;
}) {
  const { t } = useT("planning");
  const locale = useLocale();
  const paths = useWorkspacePaths();
  const { getActorName } = useActorName();

  let label: React.ReactNode;
  if (grouping === "status") {
    const bucket = group.key as PlanningBucket;
    label = (
      <>
        <span className={cn("size-2 rounded-full", PLANNING_BUCKET_COLOR[bucket])} />
        <span>{t(($) => $.progress.bucket[bucket])}</span>
      </>
    );
  } else if (grouping === "sprint") {
    const sprint = sprintById.get(group.key);
    label =
      group.key === PLANNING_NO_GROUP ? (
        <span className="text-muted-foreground">{t(($) => $.issues.no_sprint)}</span>
      ) : sprint ? (
        <>
          <Timer className="size-3.5 text-muted-foreground" />
          <AppLink href={paths.sprintDetail(sprint.id)} className="hover:underline">
            {sprint.name}
          </AppLink>
          <span className="font-normal text-caption text-muted-foreground">
            {formatDateOnly(sprint.start_date, { month: "short", day: "numeric" }, locale)}
            {" – "}
            {formatDateOnly(sprint.end_date, { month: "short", day: "numeric" }, locale)}
          </span>
        </>
      ) : (
        <span className="text-muted-foreground">{t(($) => $.issues.unknown_sprint)}</span>
      );
  } else if (grouping === "epic") {
    const epic = epicById.get(group.key);
    label =
      group.key === PLANNING_NO_GROUP ? (
        <span className="text-muted-foreground">{t(($) => $.issues.no_epic)}</span>
      ) : epic ? (
        <>
          <span className="size-2.5 rounded-sm" style={{ backgroundColor: epic.color }} />
          <AppLink href={paths.epicDetail(epic.id)} className="hover:underline">
            {epic.title}
          </AppLink>
        </>
      ) : (
        <span className="text-muted-foreground">{t(($) => $.issues.unknown_epic)}</span>
      );
  } else if (group.key === PLANNING_NO_GROUP) {
    label = <span className="text-muted-foreground">{t(($) => $.issues.unassigned)}</span>;
  } else {
    const separator = group.key.indexOf(":");
    const actorType = group.key.slice(0, separator);
    const actorId = group.key.slice(separator + 1);
    label = (
      <>
        <ActorAvatar actorType={actorType} actorId={actorId} size="xs" />
        <span>{getActorName(actorType, actorId)}</span>
      </>
    );
  }

  return (
    <h3 className="flex h-8 items-center gap-2 border-b bg-muted/40 px-3 text-label font-medium [section:not(:first-child)>&]:border-t">
      {label}
      <span className="font-normal text-caption text-muted-foreground tabular-nums">{group.nodes.length}</span>
    </h3>
  );
}

function IssueRow({
  issue,
  depth,
  relation,
  epicById,
  sprintById,
}: {
  issue: Issue;
  depth: number;
  relation: "sprint" | "epic";
  epicById: Map<string, Epic>;
  sprintById: Map<string, Sprint>;
}) {
  const paths = useWorkspacePaths();
  const { colorOf, iconOf, labelOf } = useIssueStatuses(useWorkspaceId());
  const sprint = relation === "sprint" && issue.sprint_id ? sprintById.get(issue.sprint_id) : undefined;
  const epic = relation === "epic" && issue.epic_id ? epicById.get(issue.epic_id) : undefined;
  return (
    <AppLink
      href={paths.issueDetail(issue.id)}
      data-depth={depth}
      className="flex h-9 items-center gap-2 pr-3 text-body hover:bg-surface-hover transition-colors"
      style={{ paddingLeft: 12 + depth * TREE_INDENT_PX }}
    >
      {depth > 0 ? <CornerDownRight className="size-3 shrink-0 text-muted-foreground" /> : null}
      <PriorityIcon priority={issue.priority} />
      <span className="w-20 shrink-0 truncate text-caption text-muted-foreground tabular-nums">
        {issue.identifier}
      </span>
      <StatusIcon
        status={issue.status}
        color={colorOf(issue.status)}
        icon={iconOf(issue.status)}
        category={issueStatusCategory(issue) ?? undefined}
        className="size-3.5 shrink-0"
      />
      <span className="min-w-0 flex-1 truncate">{issue.title}</span>
      <span className="hidden shrink-0 text-caption text-muted-foreground md:inline">{labelOf(issue.status)}</span>
      {sprint ? (
        <span className="hidden max-w-36 shrink-0 items-center gap-1 truncate rounded-full border px-2 py-0.5 text-caption text-muted-foreground sm:inline-flex">
          <Timer className="size-3 shrink-0" />
          <span className="truncate">{sprint.name}</span>
        </span>
      ) : null}
      {epic ? (
        <span className="hidden max-w-36 shrink-0 items-center gap-1 truncate rounded-full border px-2 py-0.5 text-caption text-muted-foreground sm:inline-flex">
          <span className="size-2 shrink-0 rounded-sm" style={{ backgroundColor: epic.color }} />
          <span className="truncate">{epic.title}</span>
        </span>
      ) : null}
      <span className="flex w-5 shrink-0 justify-end">
        {issue.assignee_type && issue.assignee_id ? (
          <ActorAvatar actorType={issue.assignee_type} actorId={issue.assignee_id} size="sm" />
        ) : null}
      </span>
    </AppLink>
  );
}
