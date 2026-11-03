"use client";

import type { PlanningAttentionIssue } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useIssueStatuses } from "@multica/core/issue-statuses/hooks";
import { AlertTriangle } from "lucide-react";
import { ActorAvatar } from "../common/actor-avatar";
import { StatusIcon } from "../issues/components/status-icon";
import { AppLink } from "../navigation";
import { useT } from "../i18n";

/**
 * Blocked issues and issues waiting for review. Renders nothing when there are
 * none: an empty "needs attention" box would itself demand attention.
 */
export function PlanningAttention({
  issues,
  total,
}: {
  issues: PlanningAttentionIssue[];
  /** Exact count of blocked + in-review issues; the list itself is capped. */
  total: number;
}) {
  const { t } = useT("planning");
  const paths = useWorkspacePaths();
  const { colorOf, iconOf } = useIssueStatuses(useWorkspaceId());
  if (issues.length === 0) return null;
  const hidden = total - issues.length;
  return (
    <section aria-label={t(($) => $.attention.title)} className="rounded-md border border-warning/40 bg-warning/5">
      <h2 className="flex items-center gap-1.5 px-3 pt-2.5 text-label font-medium">
        <AlertTriangle className="size-3.5 text-warning" />
        {t(($) => $.attention.title)}
        <span className="text-caption font-normal text-muted-foreground tabular-nums">{total}</span>
      </h2>
      <ul className="py-1.5">
        {issues.map((issue) => (
          <li key={issue.id}>
            <AppLink
              href={paths.issueDetail(issue.id)}
              className="flex items-center gap-2 px-3 py-1 text-body hover:bg-surface-hover transition-colors"
            >
              <StatusIcon status={issue.status} color={colorOf(issue.status)} icon={iconOf(issue.status)} className="size-3.5 shrink-0" />
              <span className="shrink-0 text-caption text-muted-foreground tabular-nums">{issue.identifier}</span>
              <span className="min-w-0 flex-1 truncate">{issue.title}</span>
              <span className="shrink-0 text-caption text-muted-foreground">
                {t(($) => $.progress.bucket[issue.bucket])}
              </span>
              {issue.assignee_type && issue.assignee_id ? (
                <ActorAvatar actorType={issue.assignee_type} actorId={issue.assignee_id} size="xs" />
              ) : null}
            </AppLink>
          </li>
        ))}
      </ul>
      {hidden > 0 ? (
        <p className="px-3 pb-2.5 text-caption text-muted-foreground">
          {t(($) => $.attention.more, { count: hidden })}
        </p>
      ) : null}
    </section>
  );
}
