"use client";

import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Diamond, Link2, Trash2 } from "lucide-react";
import { epicDetailOptions, useUpdateEpic, useDeleteEpic } from "@multica/core/epics";
import { EPIC_STATUS_CONFIG, DEFAULT_EPIC_COLORS } from "@multica/core/epics/config";
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
import { currentPath, useNavigation } from "../../navigation";
import { useT } from "../../i18n";

export function EpicDetail({ epicId }: { epicId: string }) {
  const { t } = useT("epics");
  const router = useNavigation();
  const wsPaths = useWorkspacePaths();
  const wsId = useWorkspaceId();
  const { data: epic, isLoading } = useQuery(epicDetailOptions(wsId, epicId));
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
      {/* Header */}
      <div className="flex items-center justify-between border-b px-5 py-3">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Diamond className="h-3.5 w-3.5" />
          <span>{t(($) => $.detail.breadcrumb_fallback)}</span>
        </div>
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
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
              render={<Button variant="ghost" size="sm"><Trash2 className="h-3.5 w-3.5" /></Button>}
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
          {/* Color + Title */}
          <div className="flex items-start gap-3">
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <button
                    type="button"
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
                      onClick={() =>
                        handleUpdate({ id: epic.id, color: c })
                      }
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
              defaultValue={epic.title}
              placeholder={t(($) => $.detail.title_placeholder)}
              onChange={(title) =>
                handleUpdate({ id: epic.id, title })
              }
              className="text-xl font-semibold"
            />
          </div>

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
                {(
                  Object.entries(EPIC_STATUS_CONFIG) as [
                    EpicStatus,
                    (typeof EPIC_STATUS_CONFIG)[EpicStatus],
                  ][]
                ).map(([key]) => (
                  <DropdownMenuItem
                    key={key}
                    onClick={() =>
                      handleUpdate({ id: epic.id, status: key })
                    }
                  >
                    {t(($) => $.status[key])}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </div>

          {/* Progress */}
          {epic.issue_count > 0 && (
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground w-16">
                {t(($) => $.table.progress)}
              </span>
              <span className="text-xs tabular-nums">
                {epic.done_count}/{epic.issue_count} {t(($) => $.table.issues)}
              </span>
            </div>
          )}

          {/* Description */}
          <div>
            <div className="text-xs font-medium text-muted-foreground mb-2">
              {t(($) => $.detail.section_description)}
            </div>
            <ContentEditor
              ref={descRef}
              defaultValue={epic.description ?? ""}
              placeholder={t(($) => $.detail.description_placeholder)}
              onBlur={() => {
                const md = descRef.current?.getMarkdown() ?? "";
                handleUpdate({
                  id: epic.id,
                  description: md || null,
                });
              }}
            />
          </div>
        </div>
      </div>
    </div>
  );
}
