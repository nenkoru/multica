"use client";

import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Timer, Link2, Trash2 } from "lucide-react";
import {
  sprintDetailOptions,
  useUpdateSprint,
  useDeleteSprint,
} from "@multica/core/sprints";
import {
  SPRINT_STATUS_CONFIG,
  SPRINT_STATUS_ORDER,
} from "@multica/core/sprints/config";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
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
import { currentPath, useNavigation } from "../../navigation";
import { useT } from "../../i18n";

export function SprintDetail({ sprintId }: { sprintId: string }) {
  const { t } = useT("sprints");
  const router = useNavigation();
  const wsPaths = useWorkspacePaths();
  const wsId = useWorkspaceId();
  const { data: sprint, isLoading } = useQuery(
    sprintDetailOptions(wsId, sprintId),
  );
  const updateSprint = useUpdateSprint();
  const deleteSprint = useDeleteSprint();
  const [deleteOpen, setDeleteOpen] = useState(false);
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

  const handleDelete = () => {
    deleteSprint.mutate(sprint.id, {
      onError: (err) => toast.error(err.message),
      onSuccess: () => {
        router.push(wsPaths.sprints());
        setDeleteOpen(false);
      },
    });
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
      {/* Header */}
      <div className="flex items-center justify-between border-b px-5 py-3">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Timer className="h-3.5 w-3.5" />
          <span>{t(($) => $.detail.breadcrumb_fallback)}</span>
        </div>
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
            aria-label={t(($) => $.detail.copy_link)}
            onClick={() =>
              copyText(router.getShareableUrl(currentPath(router))).then((ok) => {
                if (ok) toast.success(t(($) => $.detail.copy_link));
              })
            }
          >
            <Link2 className="h-3.5 w-3.5" />
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={<Button variant="ghost" size="sm" aria-label={t(($) => $.detail.delete_action)}><Trash2 className="h-3.5 w-3.5" /></Button>}
            />
            <DropdownMenuContent align="end">
              <DropdownMenuItem
                className="text-destructive"
                onClick={() => setDeleteOpen(true)}
              >
                {t(($) => $.detail.delete_action)}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      {/* Content */}
      <div className="flex flex-1 min-h-0 overflow-y-auto">
        <div className="flex-1 max-w-3xl mx-auto px-6 py-6 space-y-6">
          {/* Title */}
          <TitleEditor
            defaultValue={sprint.name}
            placeholder={t(($) => $.detail.title_placeholder)}
            onChange={(name) => handleUpdate({ id: sprint.id, name })}
            className="text-xl font-semibold"
          />

          {/* Status */}
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground w-16">{t(($) => $.table.status)}</span>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <button
                    type="button"
                    className={cn(
                      "text-xs px-2 py-0.5 rounded-full cursor-pointer",
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
                  <DropdownMenuItem
                    key={key}
                    onClick={() =>
                      handleUpdate({ id: sprint.id, status: key })
                    }
                  >
                    {t(($) => $.status[key])}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </div>

          {/* Dates */}
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground w-16">{t(($) => $.table.dates)}</span>
            <div className="flex items-center gap-2">
              <Input
                type="date"
                value={sprint.start_date}
                onChange={(e) =>
                  handleUpdate({
                    id: sprint.id,
                    start_date: e.target.value,
                  })
                }
                className="h-7 w-36 text-xs"
              />
              <span className="text-xs text-muted-foreground">—</span>
              <Input
                type="date"
                value={sprint.end_date}
                onChange={(e) =>
                  handleUpdate({
                    id: sprint.id,
                    end_date: e.target.value,
                  })
                }
                className="h-7 w-36 text-xs"
              />
            </div>
          </div>

          {/* Progress */}
          {sprint.issue_count > 0 && (
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground w-16">
                {t(($) => $.table.progress)}
              </span>
              <span className="text-xs tabular-nums">
                {sprint.done_count}/{sprint.issue_count} {t(($) => $.table.issues)}
              </span>
            </div>
          )}

          {/* Goal */}
          <div>
            <div className="text-xs font-medium text-muted-foreground mb-2">
              {t(($) => $.detail.section_description)}
            </div>
            <ContentEditor
              ref={descRef}
              defaultValue={sprint.goal ?? ""}
              placeholder={t(($) => $.detail.description_placeholder)}
              onBlur={() => {
                const md = descRef.current?.getMarkdown() ?? "";
                handleUpdate({
                  id: sprint.id,
                  goal: md || null,
                });
              }}
            />
          </div>
        </div>
      </div>
    </div>
  );
}
