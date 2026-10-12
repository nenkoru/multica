"use client";

import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, Link2, Play, Trash2 } from "lucide-react";
import {
  sprintDetailOptions,
  sprintOverviewOptions,
  useUpdateSprint,
  useDeleteSprint,
} from "@multica/core/sprints";
import {
  SPRINT_STATUS_CONFIG,
  SPRINT_STATUS_ORDER,
} from "@multica/core/sprints/config";
import { resolvePlanningProgress, sprintTimeline } from "@multica/core/planning";
import { todayDateOnly } from "@multica/core/issues/date";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import type { Sprint, SprintStatus } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { toast } from "sonner";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { AlertDialog, AlertDialogContent, AlertDialogHeader, AlertDialogTitle, AlertDialogDescription, AlertDialogFooter, AlertDialogCancel, AlertDialogAction } from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { TitleEditor, ContentEditor, type ContentEditorRef } from "../../editor";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { currentPath, useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import {
  AssigneeBreakdown,
  BurndownChart,
  CompleteSprintDialog,
  EpicBreakdown,
  PlanningAttention,
  PlanningLayout,
  PlanningProgressSummary,
  PlanningWorkArea,
} from "../../planning";

/** "Day 3 of 8 · Days left: 5" and its before/after variants. */
function SprintTimelineText({ sprint }: { sprint: Sprint }) {
  const { t } = useT("planning");
  const timeline = sprintTimeline(sprint.start_date, sprint.end_date, todayDateOnly());
  if (!timeline) return null;
  // A finished or cancelled sprint is no longer counting days.
  if (sprint.status === "completed" || sprint.status === "cancelled") {
    return <span>{t(($) => $.sprint.length, { count: timeline.totalDays })}</span>;
  }
  if (timeline.phase === "upcoming") {
    return (
      <span>
        {t(($) => $.sprint.starts_in, { count: timeline.startsInDays })}
        {" · "}
        {t(($) => $.sprint.length, { count: timeline.totalDays })}
      </span>
    );
  }
  if (timeline.phase === "over") {
    return <span className="text-destructive">{t(($) => $.sprint.ended_ago, { count: timeline.daysOver })}</span>;
  }
  return (
    <span>
      {t(($) => $.sprint.day_of, { day: timeline.day, total: timeline.totalDays })}
      {" · "}
      {timeline.daysLeft === 0
        ? t(($) => $.sprint.last_day)
        : t(($) => $.sprint.days_left, { count: timeline.daysLeft })}
    </span>
  );
}

export function SprintDetail({ sprintId }: { sprintId: string }) {
  const { t } = useT("sprints");
  const { t: tPlanning } = useT("planning");
  const router = useNavigation();
  const wsPaths = useWorkspacePaths();
  const wsId = useWorkspaceId();
  const { data: sprint, isLoading } = useQuery(
    sprintDetailOptions(wsId, sprintId),
  );
  const { data: overview } = useQuery(sprintOverviewOptions(wsId, sprintId));
  const updateSprint = useUpdateSprint();
  const deleteSprint = useDeleteSprint();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [completeOpen, setCompleteOpen] = useState(false);
  const descRef = useRef<ContentEditorRef>(null);
  const handleUpdate = (data: Parameters<typeof updateSprint.mutate>[0]) =>
    updateSprint.mutate(data, { onError: (err) => toast.error(err.message) });

  if (isLoading) {
    return (
      <div className="flex flex-1 flex-col p-6 gap-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-4 w-full max-w-md" />
        <Skeleton className="h-32 w-full" />
      </div>
    );
  }

  if (!sprint) {
    return (
      <div className="flex flex-1 items-center justify-center text-muted-foreground">
        {t(($) => $.detail.not_found)}
      </div>
    );
  }

  const statusCfg = SPRINT_STATUS_CONFIG[sprint.status];
  const progress = overview?.progress ?? resolvePlanningProgress(sprint);
  const openCount = progress.total - progress.done - progress.cancelled;
  const canComplete = sprint.status === "planned" || sprint.status === "active";

  const handleDelete = () => {
    deleteSprint.mutate(sprint.id, {
      onError: (err) => toast.error(err.message),
      onSuccess: () => {
        router.push(wsPaths.sprints());
        setDeleteOpen(false);
      },
    });
  };

  // Completing is never a bare status flip: it goes through the dialog, which
  // asks what to do with unfinished issues.
  const handleStatus = (status: SprintStatus) => {
    if (status === sprint.status) return;
    if (status === "completed" && canComplete) setCompleteOpen(true);
    else handleUpdate({ id: sprint.id, status });
  };

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.detail.delete_confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>{t(($) => $.detail.delete_confirm_description)}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteSprint.isPending}>{t(($) => $.create.cancel)}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={deleteSprint.isPending} onClick={handleDelete}>{t(($) => $.detail.delete_action)}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {completeOpen ? (
        <CompleteSprintDialog
          sprint={sprint}
          openCount={openCount}
          totalCount={progress.total}
          onClose={() => setCompleteOpen(false)}
        />
      ) : null}

      <BreadcrumbHeader
        segments={[{ href: wsPaths.sprints(), label: t(($) => $.detail.breadcrumb_fallback) }]}
        leaf={<span className="truncate font-medium text-foreground">{sprint.name}</span>}
        actions={
          <>
            {sprint.status === "planned" ? (
              <Button size="sm" variant="outline" onClick={() => handleUpdate({ id: sprint.id, status: "active" })}>
                <Play />
                {tPlanning(($) => $.sprint.start)}
              </Button>
            ) : null}
            {sprint.status === "active" ? (
              <Button size="sm" variant="outline" onClick={() => setCompleteOpen(true)}>
                <CheckCircle2 />
                {tPlanning(($) => $.sprint.complete)}
              </Button>
            ) : null}
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground"
              aria-label={t(($) => $.detail.copy_link)}
              onClick={() => {
                void copyText(router.getShareableUrl(currentPath(router))).then((ok) => {
                  if (ok) toast.success(t(($) => $.detail.copy_link));
                });
              }}
            >
              <Link2 />
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button variant="ghost" size="icon-sm" className="text-muted-foreground" aria-label={t(($) => $.detail.delete_action)}>
                    <Trash2 />
                  </Button>
                }
              />
              <DropdownMenuContent align="end" className="w-auto">
                <DropdownMenuItem variant="destructive" onClick={() => setDeleteOpen(true)}>
                  {t(($) => $.detail.delete_action)}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      />

      <PlanningLayout
        summaryLabel={tPlanning(($) => $.sidebar.details)}
        main={
          <>
            <header className="space-y-3">
              <TitleEditor
                key={`name-${sprint.id}`}
                defaultValue={sprint.name}
                placeholder={t(($) => $.detail.title_placeholder)}
                onBlur={(value) => {
                  const name = value.trim();
                  if (name && name !== sprint.name) handleUpdate({ id: sprint.id, name });
                }}
                className="text-title-lg font-semibold"
              />

              <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-caption text-muted-foreground">
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <button
                        type="button"
                        className={cn(
                          "text-caption px-2 py-0.5 rounded-full cursor-pointer",
                          statusCfg.badgeBg,
                          statusCfg.badgeText,
                        )}
                      >
                        {t(($) => $.status[sprint.status])}
                      </button>
                    }
                  />
                  <DropdownMenuContent align="start">
                    {SPRINT_STATUS_ORDER.map((key) => (
                      <DropdownMenuItem key={key} onClick={() => handleStatus(key)}>
                        {t(($) => $.status[key])}
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
                <div className="flex items-center gap-1.5">
                  <Input
                    type="date"
                    aria-label={t(($) => $.create.start_date)}
                    value={sprint.start_date}
                    onChange={(e) => handleUpdate({ id: sprint.id, start_date: e.target.value })}
                    className="h-7 w-36 text-caption"
                  />
                  <span>—</span>
                  <Input
                    type="date"
                    aria-label={t(($) => $.create.end_date)}
                    value={sprint.end_date}
                    onChange={(e) => handleUpdate({ id: sprint.id, end_date: e.target.value })}
                    className="h-7 w-36 text-caption"
                  />
                </div>
                <SprintTimelineText sprint={sprint} />
              </div>

              <div>
                <div className="mb-1 text-caption font-medium text-muted-foreground">
                  {tPlanning(($) => $.sprint.goal)}
                </div>
                <ContentEditor
                  key={`goal-${sprint.id}`}
                  ref={descRef}
                  defaultValue={sprint.goal ?? ""}
                  placeholder={t(($) => $.detail.description_placeholder)}
                  onBlur={() => {
                    const md = descRef.current?.getMarkdown() ?? "";
                    if ((md || null) !== (sprint.goal ?? null)) {
                      handleUpdate({ id: sprint.id, goal: md || null });
                    }
                  }}
                />
              </div>
            </header>

            {progress.total > 0 ? <PlanningProgressSummary progress={progress} /> : null}
            {overview?.burndown && overview.burndown.days.length > 0 ? (
              <div className="max-w-xl">
                <BurndownChart burndown={overview.burndown} />
              </div>
            ) : null}
            <PlanningWorkArea kind="sprint" id={sprint.id} />
          </>
        }
        summary={
          <>
            {overview ? (
              <PlanningAttention
                issues={overview.attention}
                total={overview.progress.blocked + overview.progress.in_review}
              />
            ) : null}
            <AssigneeBreakdown
              title={tPlanning(($) => $.sidebar.workload)}
              assignees={overview?.assignees ?? []}
              showOpen
            />
            <EpicBreakdown epics={overview?.epics ?? []} />
          </>
        }
      />
    </div>
  );
}
