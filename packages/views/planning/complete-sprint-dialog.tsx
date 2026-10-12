"use client";

import { useId, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { sprintListOptions, useCompleteSprint } from "@multica/core/sprints";
import { useWorkspaceId } from "@multica/core/hooks";
import type { Sprint, SprintOpenIssuesMode } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { useT } from "../i18n";

/**
 * Sprints unfinished issues may move to, best candidate first: planned ones
 * that start on or after this sprint, then other planned ones, then active
 * ones. The first entry mirrors the server's default target.
 */
export function sprintMoveTargets(sprint: Sprint, sprints: Sprint[]): Sprint[] {
  const rank = (candidate: Sprint) =>
    candidate.status !== "planned" ? 2 : candidate.start_date >= sprint.start_date ? 0 : 1;
  return sprints
    .filter((candidate) => candidate.id !== sprint.id && (candidate.status === "planned" || candidate.status === "active"))
    .sort((a, b) => rank(a) - rank(b) || a.start_date.localeCompare(b.start_date) || a.id.localeCompare(b.id));
}

/**
 * Confirms sprint completion and asks what to do with unfinished issues. No
 * option discards work: issues are moved, left where they are, or detached
 * from the sprint, and the request always names the choice explicitly.
 */
export function CompleteSprintDialog({
  sprint,
  openCount,
  totalCount,
  onClose,
}: {
  sprint: Sprint;
  /** Unfinished issues in the sprint right now. */
  openCount: number;
  totalCount: number;
  onClose: () => void;
}) {
  const { t } = useT("planning");
  const formId = useId();
  const wsId = useWorkspaceId();
  const { data: sprints = [] } = useQuery(sprintListOptions(wsId));
  const completeSprint = useCompleteSprint();
  const targets = useMemo(() => sprintMoveTargets(sprint, sprints), [sprint, sprints]);
  // Null until the user picks: the default follows the data, so a sprint list
  // that arrives after the dialog opened still preselects "move".
  const [picked, setPicked] = useState<SprintOpenIssuesMode | null>(null);
  const [pickedTarget, setPickedTarget] = useState<string | null>(null);
  const canMove = targets.length > 0;
  const mode: SprintOpenIssuesMode = picked ?? (canMove ? "move" : "keep");
  const targetId = pickedTarget && targets.some((s) => s.id === pickedTarget) ? pickedTarget : targets[0]?.id;
  const hasOpen = openCount > 0;

  const submit = () => {
    const request =
      hasOpen && mode === "move" && targetId
        ? { open_issues: "move" as const, target_sprint_id: targetId }
        : { open_issues: hasOpen && mode === "unlink" ? ("unlink" as const) : ("keep" as const) };
    completeSprint.mutate(
      { id: sprint.id, ...request },
      {
        onSuccess: (result) => {
          if (result.open_issues === "move") {
            toast.success(
              t(($) => $.complete.done_moved, {
                count: result.moved_count,
                sprint: result.target_sprint?.name ?? "",
              }),
            );
          } else if (result.open_issues === "unlink") {
            toast.success(t(($) => $.complete.done_unlinked, { count: result.unlinked_count }));
          } else {
            toast.success(t(($) => $.complete.done_kept, { count: result.kept_count }));
          }
          onClose();
        },
        onError: (err) => toast.error(err instanceof Error && err.message ? err.message : t(($) => $.complete.failed)),
      },
    );
  };

  const option = (value: SprintOpenIssuesMode, label: string, disabled = false) => (
    <label className="flex items-center gap-2 text-body has-disabled:text-muted-foreground">
      <input
        type="radio"
        name={`${formId}-open-issues`}
        value={value}
        checked={mode === value}
        disabled={disabled || completeSprint.isPending}
        onChange={() => setPicked(value)}
        className="accent-primary"
      />
      {label}
    </label>
  );

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !completeSprint.isPending) onClose(); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.complete.title)}</DialogTitle>
          <DialogDescription>
            {hasOpen
              ? t(($) => $.complete.summary_open, { open: openCount, total: totalCount })
              : t(($) => $.complete.summary_none)}
          </DialogDescription>
        </DialogHeader>
        {hasOpen ? (
          <fieldset className="space-y-3">
            <legend className="mb-2 text-label font-medium">{t(($) => $.complete.choice_label)}</legend>
            <div className="space-y-2">
              {option("move", t(($) => $.complete.move), !canMove)}
              {canMove ? (
                mode === "move" ? (
                  <div className="ml-6 space-y-1">
                    <Label htmlFor={`${formId}-target`} className="text-caption text-muted-foreground">
                      {t(($) => $.complete.target)}
                    </Label>
                    <Select
                      items={targets.map((s) => ({ value: s.id, label: s.name }))}
                      value={targetId}
                      onValueChange={(value) => value && setPickedTarget(value)}
                    >
                      <SelectTrigger id={`${formId}-target`} className="w-full" disabled={completeSprint.isPending}>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {targets.map((s) => (
                          <SelectItem key={s.id} value={s.id}>
                            {s.name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                ) : null
              ) : (
                <p className="ml-6 text-caption text-muted-foreground">{t(($) => $.complete.move_unavailable)}</p>
              )}
            </div>
            {option("keep", t(($) => $.complete.keep))}
            <div className="space-y-1">
              {option("unlink", t(($) => $.complete.unlink))}
              <p className="ml-6 text-caption text-muted-foreground">{t(($) => $.complete.unlink_hint)}</p>
            </div>
          </fieldset>
        ) : null}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose} disabled={completeSprint.isPending}>
            {t(($) => $.complete.cancel)}
          </Button>
          <Button type="button" onClick={submit} disabled={completeSprint.isPending} aria-busy={completeSprint.isPending}>
            {t(($) => $.complete.confirm)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
