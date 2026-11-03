"use client";

import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link2, Trash2 } from "lucide-react";
import { epicDetailOptions, epicOverviewOptions, useUpdateEpic, useDeleteEpic } from "@multica/core/epics";
import { EPIC_STATUS_CONFIG, DEFAULT_EPIC_COLORS } from "@multica/core/epics/config";
import { resolvePlanningProgress } from "@multica/core/planning";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import type { EpicStatus } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { toast } from "sonner";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { AlertDialog, AlertDialogContent, AlertDialogHeader, AlertDialogTitle, AlertDialogDescription, AlertDialogFooter, AlertDialogCancel, AlertDialogAction } from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { TitleEditor, ContentEditor, type ContentEditorRef } from "../../editor";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { currentPath, useNavigation } from "../../navigation";
import { useLocale, useT, useTimeAgo } from "../../i18n";
import {
  AssigneeBreakdown,
  PlanningActivity,
  PlanningAttention,
  PlanningLayout,
  PlanningProgressSummary,
  PlanningWorkArea,
  SprintBreakdown,
} from "../../planning";

export function EpicDetail({ epicId }: { epicId: string }) {
  const { t } = useT("epics");
  const { t: tPlanning } = useT("planning");
  const locale = useLocale();
  const timeAgo = useTimeAgo();
  const router = useNavigation();
  const wsPaths = useWorkspacePaths();
  const wsId = useWorkspaceId();
  const { data: epic, isLoading } = useQuery(epicDetailOptions(wsId, epicId));
  const { data: overview } = useQuery(epicOverviewOptions(wsId, epicId));
  const updateEpic = useUpdateEpic();
  const deleteEpic = useDeleteEpic();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const descRef = useRef<ContentEditorRef>(null);
  const handleUpdate = (data: Parameters<typeof updateEpic.mutate>[0]) =>
    updateEpic.mutate(data, { onError: (err) => toast.error(err.message) });

  if (isLoading) {
    return (
      <div className="flex flex-1 flex-col p-6 gap-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-4 w-full max-w-md" />
        <Skeleton className="h-32 w-full" />
      </div>
    );
  }

  if (!epic) {
    return (
      <div className="flex flex-1 items-center justify-center text-muted-foreground">
        {t(($) => $.detail.not_found)}
      </div>
    );
  }

  const handleDelete = () => {
    deleteEpic.mutate(epic.id, {
      onError: (err) => toast.error(err.message),
      onSuccess: () => {
        router.push(wsPaths.epics());
        setDeleteOpen(false);
      },
    });
  };

  // The overview is the exact, current breakdown; until it arrives the bar is
  // drawn from what the epic itself already carries.
  const progress = overview?.progress ?? resolvePlanningProgress(epic);
  const lastActivityAt = overview?.last_activity_at ?? null;

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.detail.delete_confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>{t(($) => $.detail.delete_confirm_description)}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteEpic.isPending}>{t(($) => $.create.cancel)}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={deleteEpic.isPending} onClick={handleDelete}>{t(($) => $.detail.delete_action)}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <BreadcrumbHeader
        segments={[{ href: wsPaths.epics(), label: t(($) => $.detail.breadcrumb_fallback) }]}
        leaf={<span className="truncate font-medium text-foreground">{epic.title}</span>}
        actions={
          <>
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
              <div className="flex items-start gap-3">
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <button
                        type="button"
                        aria-label={t(($) => $.create.color)}
                        className="mt-1 size-6 rounded-sm shrink-0 cursor-pointer hover:opacity-80 transition-opacity"
                        style={{ backgroundColor: epic.color }}
                      />
                    }
                  />
                  <DropdownMenuContent align="start">
                    <div className="flex items-center gap-2 p-2">
                      {DEFAULT_EPIC_COLORS.map((c) => (
                        <button
                          key={c}
                          type="button"
                          aria-label={c}
                          onClick={() => handleUpdate({ id: epic.id, color: c })}
                          className={cn(
                            "size-5 rounded-full transition-all",
                            epic.color === c
                              ? "ring-2 ring-offset-1 ring-foreground"
                              : "hover:scale-110",
                          )}
                          style={{ backgroundColor: c }}
                        />
                      ))}
                    </div>
                  </DropdownMenuContent>
                </DropdownMenu>
                <TitleEditor
                  key={`title-${epic.id}`}
                  defaultValue={epic.title}
                  placeholder={t(($) => $.detail.title_placeholder)}
                  onBlur={(value) => {
                    const title = value.trim();
                    if (title && title !== epic.title) handleUpdate({ id: epic.id, title });
                  }}
                  className="min-w-0 flex-1 text-title-lg font-semibold"
                />
              </div>

              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-muted-foreground">
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <button
                        type="button"
                        className={cn(
                          "text-caption px-2 py-0.5 rounded-full cursor-pointer",
                          epic.status === "open"
                            ? "bg-primary/10 text-primary"
                            : "bg-muted text-muted-foreground",
                        )}
                      >
                        {t(($) => $.status[epic.status])}
                      </button>
                    }
                  />
                  <DropdownMenuContent align="start">
                    {(Object.keys(EPIC_STATUS_CONFIG) as EpicStatus[]).map((key) => (
                      <DropdownMenuItem key={key} onClick={() => handleUpdate({ id: epic.id, status: key })}>
                        {t(($) => $.status[key])}
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
                <span>
                  {tPlanning(($) => $.sidebar.created)}{" "}
                  {new Date(epic.created_at).toLocaleDateString(locale, { year: "numeric", month: "short", day: "numeric" })}
                </span>
                <span>
                  {tPlanning(($) => $.sidebar.last_activity)}{" "}
                  {lastActivityAt ? timeAgo(lastActivityAt) : tPlanning(($) => $.sidebar.never)}
                </span>
              </div>

              <ContentEditor
                key={`description-${epic.id}`}
                ref={descRef}
                defaultValue={epic.description ?? ""}
                placeholder={t(($) => $.detail.description_placeholder)}
                onBlur={() => {
                  const md = descRef.current?.getMarkdown() ?? "";
                  if ((md || null) !== (epic.description ?? null)) {
                    handleUpdate({ id: epic.id, description: md || null });
                  }
                }}
              />
            </header>

            {progress.total > 0 ? <PlanningProgressSummary progress={progress} /> : null}
            {overview ? (
              <PlanningAttention
                issues={overview.attention}
                total={overview.progress.blocked + overview.progress.in_review}
              />
            ) : null}
            <PlanningWorkArea kind="epic" id={epic.id} />
          </>
        }
        summary={
          <>
            <AssigneeBreakdown
              title={tPlanning(($) => $.sidebar.assignees)}
              assignees={overview?.assignees ?? []}
            />
            <SprintBreakdown sprints={overview?.sprints ?? []} />
            <PlanningActivity activity={overview?.activity ?? []} />
          </>
        }
      />
    </div>
  );
}
