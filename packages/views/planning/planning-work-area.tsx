"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link2, Plus } from "lucide-react";
import { toast } from "sonner";
import { epicIssuesOptions, epicListOptions } from "@multica/core/epics";
import { sprintIssuesOptions, sprintListOptions } from "@multica/core/sprints";
import { useUpdateIssue } from "@multica/core/issues/mutations";
import { useWorkspaceId } from "@multica/core/hooks";
import { useModalStore } from "@multica/core/modals";
import type { PlanningGrouping } from "@multica/core/planning";
import type { IssueScope } from "@multica/core/issues/surface/scope";
import type { Issue } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";
import { IssueSurface } from "../issues/surface/issue-surface";
import { IssuePickerModal } from "../modals/issue-picker-modal";
import { useT } from "../i18n";
import { PlanningIssueList } from "./planning-issue-list";

type PlanningKind = "epic" | "sprint";
type PlanningView = "list" | "board";

const GROUPINGS: Record<PlanningKind, PlanningGrouping[]> = {
  epic: ["status", "sprint", "assignee"],
  sprint: ["status", "epic", "assignee"],
};

function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: { value: T; label: string }[];
  onChange: (value: T) => void;
}) {
  return (
    <div role="group" aria-label={label} className="flex items-center gap-0.5 rounded-lg bg-muted p-0.5">
      {options.map((option) => (
        <Button
          key={option.value}
          type="button"
          size="xs"
          variant={option.value === value ? "outline" : "ghost"}
          aria-pressed={option.value === value}
          className={option.value === value ? "bg-background" : "text-muted-foreground"}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </Button>
      ))}
    </div>
  );
}

/**
 * The working area of an epic or sprint page: the grouped issue list, the
 * status board, and the two ways to add an issue.
 */
export function PlanningWorkArea({ kind, id }: { kind: PlanningKind; id: string }) {
  const { t } = useT("planning");
  const wsId = useWorkspaceId();
  const epicIssues = useQuery({ ...epicIssuesOptions(wsId, id), enabled: kind === "epic" });
  const sprintIssues = useQuery({ ...sprintIssuesOptions(wsId, id), enabled: kind === "sprint" });
  const issuesQuery = kind === "epic" ? epicIssues : sprintIssues;
  const { data: epics = [] } = useQuery(epicListOptions(wsId));
  const { data: sprints = [] } = useQuery(sprintListOptions(wsId));
  const updateIssue = useUpdateIssue();
  const [grouping, setGrouping] = useState<PlanningGrouping>("status");
  const [view, setView] = useState<PlanningView>("list");
  const [linkOpen, setLinkOpen] = useState(false);

  const issues = issuesQuery.data?.issues;
  const total = issuesQuery.data?.total ?? 0;
  const linkedIds = useMemo(() => (issues ?? []).map((issue) => issue.id), [issues]);
  const boardScope = useMemo<IssueScope>(
    () => (kind === "epic" ? { type: "epic", epicId: id } : { type: "sprint", sprintId: id }),
    [kind, id],
  );

  const openCreate = () =>
    useModalStore.getState().open("create-issue", kind === "epic" ? { epic_id: id } : { sprint_id: id });
  const linkIssue = (issue: Issue) => {
    updateIssue.mutate(
      { id: issue.id, ...(kind === "epic" ? { epic_id: id } : { sprint_id: id }) },
      {
        onSuccess: () => toast.success(t(($) => $.link.linked, { identifier: issue.identifier })),
        onError: () => toast.error(t(($) => $.link.failed)),
      },
    );
  };

  const addActions = (
    <>
      <Button type="button" size="sm" variant="outline" onClick={() => setLinkOpen(true)}>
        <Link2 />
        {t(($) => $.issues.link)}
      </Button>
      <Button type="button" size="sm" onClick={openCreate}>
        <Plus />
        {t(($) => $.issues.create)}
      </Button>
    </>
  );

  let body: React.ReactNode;
  if (issuesQuery.isError && !issues) {
    body = (
      <div role="alert" className="flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-body">
        <span>{t(($) => $.issues.load_failed)}</span>
        <Button type="button" size="sm" variant="outline" onClick={() => void issuesQuery.refetch()}>
          {t(($) => $.issues.retry)}
        </Button>
      </div>
    );
  } else if (!issues) {
    body = (
      <div className="space-y-2" aria-busy="true">
        <Skeleton className="h-8 w-full" />
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-full" />
      </div>
    );
  } else if (issues.length === 0) {
    body = (
      <Empty className="rounded-md border border-dashed py-10">
        <EmptyHeader>
          <EmptyTitle>{kind === "epic" ? t(($) => $.empty.epic_title) : t(($) => $.empty.sprint_title)}</EmptyTitle>
          <EmptyDescription>{t(($) => $.empty.hint)}</EmptyDescription>
        </EmptyHeader>
        <EmptyContent className="flex-row justify-center gap-2">{addActions}</EmptyContent>
      </Empty>
    );
  } else {
    body = (
      <>
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-label font-medium">
            {t(($) => $.issues.title)}
            <span className="ml-1.5 font-normal text-caption text-muted-foreground tabular-nums">{total}</span>
          </h2>
          <Segmented
            label={t(($) => $.issues.view_label)}
            value={view}
            onChange={setView}
            options={[
              { value: "list", label: t(($) => $.issues.view.list) },
              { value: "board", label: t(($) => $.issues.view.board) },
            ]}
          />
          {view === "list" ? (
            <Segmented
              label={t(($) => $.issues.group_by)}
              value={grouping}
              onChange={setGrouping}
              options={GROUPINGS[kind].map((value) => ({
                value,
                label: t(($) => $.issues.grouping[value]),
              }))}
            />
          ) : null}
          <div className="ml-auto flex items-center gap-2">{addActions}</div>
        </div>
        {view === "list" ? (
          <>
            <PlanningIssueList
              issues={issues}
              grouping={grouping}
              relation={kind === "epic" ? "sprint" : "epic"}
              epics={epics}
              sprints={sprints}
            />
            {total > issues.length ? (
              <p className="text-caption text-muted-foreground">
                {t(($) => $.issues.truncated, { shown: issues.length, total })}
              </p>
            ) : null}
          </>
        ) : (
          <div className="flex h-[calc(100dvh-10rem)] min-h-96 flex-col overflow-hidden rounded-md border">
            <IssueSurface scope={boardScope} modes={["board"]} />
          </div>
        )}
      </>
    );
  }

  return (
    <section aria-label={t(($) => $.issues.title)} className="space-y-3">
      {body}
      <IssuePickerModal
        open={linkOpen}
        onOpenChange={setLinkOpen}
        title={kind === "epic" ? t(($) => $.link.epic_title) : t(($) => $.link.sprint_title)}
        description={kind === "epic" ? t(($) => $.link.epic_description) : t(($) => $.link.sprint_description)}
        excludeIds={linkedIds}
        onSelect={linkIssue}
      />
    </section>
  );
}
