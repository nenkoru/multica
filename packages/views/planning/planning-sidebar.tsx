"use client";

import { Timer } from "lucide-react";
import type {
  PlanningActivityEntry,
  PlanningAssigneeBreakdown,
  PlanningEpicBreakdown,
  PlanningSprintBreakdown,
} from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import { useActorName } from "@multica/core/workspace/hooks";
import { ActorAvatar } from "../common/actor-avatar";
import { AppLink } from "../navigation";
import { useT, useTimeAgo } from "../i18n";

export function SidebarSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section aria-label={title} className="space-y-2">
      <h2 className="text-caption font-medium text-muted-foreground">{title}</h2>
      {children}
    </section>
  );
}

/** Done share of one breakdown row. */
function MiniBar({ done, total }: { done: number; total: number }) {
  const percent = total > 0 ? (done / total) * 100 : 0;
  return (
    <div className="h-1 w-full overflow-hidden rounded-full bg-muted" aria-hidden="true">
      <div className="h-full rounded-full bg-success" style={{ width: `${percent}%` }} />
    </div>
  );
}

function BreakdownRow({
  label,
  done,
  total,
  detail,
}: {
  label: React.ReactNode;
  done: number;
  total: number;
  detail?: string;
}) {
  const { t } = useT("planning");
  return (
    <li className="space-y-1">
      <div className="flex items-center gap-2 text-label">
        <div className="flex min-w-0 flex-1 items-center gap-1.5">{label}</div>
        {detail ? <span className="shrink-0 text-caption text-muted-foreground">{detail}</span> : null}
        <span className="shrink-0 text-caption text-muted-foreground tabular-nums">
          {t(($) => $.progress.done_of_total, { done, total })}
        </span>
      </div>
      <MiniBar done={done} total={total} />
    </li>
  );
}

export function AssigneeBreakdown({
  title,
  assignees,
  showOpen = false,
}: {
  title: string;
  assignees: PlanningAssigneeBreakdown[];
  /** Sprint workload: also name how much each person still has open. */
  showOpen?: boolean;
}) {
  const { t } = useT("planning");
  const { getActorName } = useActorName();
  if (assignees.length === 0) return null;
  return (
    <SidebarSection title={title}>
      <ul className="space-y-2.5">
        {assignees.map((row) => {
          const assigned = row.assignee_type !== null && row.assignee_id !== null;
          return (
            <BreakdownRow
              key={assigned ? `${row.assignee_type}:${row.assignee_id}` : "unassigned"}
              done={row.done}
              total={row.total}
              detail={showOpen ? t(($) => $.sidebar.open_count, { count: row.open }) : undefined}
              label={
                assigned ? (
                  <>
                    <ActorAvatar actorType={row.assignee_type!} actorId={row.assignee_id!} size="sm" />
                    <span className="truncate">{getActorName(row.assignee_type!, row.assignee_id!)}</span>
                  </>
                ) : (
                  <span className="truncate text-muted-foreground">{t(($) => $.issues.unassigned)}</span>
                )
              }
            />
          );
        })}
      </ul>
    </SidebarSection>
  );
}

export function SprintBreakdown({ sprints }: { sprints: PlanningSprintBreakdown[] }) {
  const { t } = useT("planning");
  const paths = useWorkspacePaths();
  if (sprints.length === 0) return null;
  return (
    <SidebarSection title={t(($) => $.sidebar.sprints)}>
      <ul className="space-y-2.5">
        {sprints.map((row) => (
          <BreakdownRow
            key={row.sprint_id ?? "none"}
            done={row.done}
            total={row.total}
            label={
              row.sprint_id ? (
                <>
                  <Timer className="size-3.5 shrink-0 text-muted-foreground" />
                  <AppLink href={paths.sprintDetail(row.sprint_id)} className="truncate hover:underline">
                    {row.name}
                  </AppLink>
                </>
              ) : (
                <span className="truncate text-muted-foreground">{t(($) => $.issues.no_sprint)}</span>
              )
            }
          />
        ))}
      </ul>
    </SidebarSection>
  );
}

export function EpicBreakdown({ epics }: { epics: PlanningEpicBreakdown[] }) {
  const { t } = useT("planning");
  const paths = useWorkspacePaths();
  if (epics.length === 0) return null;
  return (
    <SidebarSection title={t(($) => $.sidebar.epics)}>
      <ul className="space-y-2.5">
        {epics.map((row) => (
          <BreakdownRow
            key={row.epic_id ?? "none"}
            done={row.done}
            total={row.total}
            label={
              row.epic_id ? (
                <>
                  <span className="size-2.5 shrink-0 rounded-sm" style={{ backgroundColor: row.color }} />
                  <AppLink href={paths.epicDetail(row.epic_id)} className="truncate hover:underline">
                    {row.title}
                  </AppLink>
                </>
              ) : (
                <span className="truncate text-muted-foreground">{t(($) => $.issues.no_epic)}</span>
              )
            }
          />
        ))}
      </ul>
    </SidebarSection>
  );
}

function detailString(details: Record<string, unknown>, key: string): string {
  const value = details[key];
  return typeof value === "string" ? value : "";
}

export function PlanningActivity({ activity }: { activity: PlanningActivityEntry[] }) {
  const { t } = useT("planning");
  const { t: tIssues } = useT("issues");
  const timeAgo = useTimeAgo();
  const paths = useWorkspacePaths();
  const { getActorName } = useActorName();
  const { labelOf } = useIssueStatuses(useWorkspaceId());

  const describe = (entry: PlanningActivityEntry): string => {
    switch (entry.action) {
      case "created":
        return t(($) => $.activity.created);
      case "status_changed":
        return t(($) => $.activity.status_changed, { to: labelOf(detailString(entry.details, "to")) });
      case "assignee_changed":
        return t(($) => $.activity.assignee_changed);
      case "priority_changed": {
        const to = detailString(entry.details, "to");
        const label =
          to === "urgent" || to === "high" || to === "medium" || to === "low" || to === "none"
            ? tIssues(($) => $.priority[to])
            : to;
        return t(($) => $.activity.priority_changed, { to: label });
      }
      default:
        return t(($) => $.activity.title_changed);
    }
  };

  return (
    <SidebarSection title={t(($) => $.sidebar.activity)}>
      {activity.length === 0 ? (
        <p className="text-caption text-muted-foreground">{t(($) => $.sidebar.no_activity)}</p>
      ) : (
        <ul className="space-y-2.5">
          {activity.map((entry) => (
            <li key={entry.id} className="flex gap-2 text-caption">
              <span className="mt-0.5 shrink-0">
                {entry.actor_type && entry.actor_id ? (
                  <ActorAvatar actorType={entry.actor_type} actorId={entry.actor_id} size="xs" />
                ) : (
                  <span className="block size-4 rounded-full bg-muted" />
                )}
              </span>
              <div className="min-w-0 flex-1">
                <p className="text-muted-foreground">
                  <span className="text-foreground">
                    {entry.actor_type && entry.actor_id
                      ? getActorName(entry.actor_type, entry.actor_id)
                      : t(($) => $.activity.system)}
                  </span>{" "}
                  {describe(entry)}
                </p>
                <p className="flex items-center gap-1.5 text-muted-foreground">
                  <AppLink
                    href={paths.issueDetail(entry.issue_id)}
                    className="min-w-0 truncate hover:text-foreground hover:underline"
                    title={entry.issue_title}
                  >
                    <span className="tabular-nums">{entry.issue_identifier}</span> {entry.issue_title}
                  </AppLink>
                  <span className="shrink-0">· {timeAgo(entry.created_at)}</span>
                </p>
              </div>
            </li>
          ))}
        </ul>
      )}
    </SidebarSection>
  );
}
