"use client";

import { useState } from "react";
import { toast } from "sonner";
import { Check } from "@multica/ui/icons";
import { Button } from "@multica/ui/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { ApiError } from "@multica/core/api";
import { useCancelScreenAction, usePressScreenAction, type ScreenAction, type ScreenPressResult } from "@multica/core/streams";
import { useT } from "../../i18n";
import { useScreenEnv } from "./screen-context";

type Phase = "idle" | "busy" | "done";

/** The URL of an open(...) button, checked by the server when stored. */
function openUrl(action: ScreenAction | undefined): string | null {
  const url = action?.name === "open" ? action.args.url : undefined;
  return typeof url === "string" && /^https:\/\//.test(url) ? url : null;
}

/** One screen button: presses its ui_action; a red action asks first in
 *  the app's confirmation window, whose text comes from the server. */
export function ScreenActionButton({
  label,
  actionId,
  action,
  zone,
  variant = "outline",
}: {
  label: string;
  actionId?: string;
  action?: ScreenAction;
  zone: string;
  variant?: "outline" | "default";
}) {
  const { t } = useT("streams");
  const env = useScreenEnv();
  const press = usePressScreenAction();
  const cancel = useCancelScreenAction();
  const [phase, setPhase] = useState<Phase>("idle");
  const [confirm, setConfirm] = useState<{ title: string; text: string } | null>(null);

  const finish = (res: ScreenPressResult) => {
    switch (res.status) {
      case "done":
      case "already": {
        setPhase("done");
        const followup = res.result?.followup;
        if (res.status === "done" && typeof followup === "string" && followup) {
          env.onFollowup?.(t(($) => $.screen.from_screen, { text: followup }));
        }
        const message = res.result?.message;
        if (res.status === "already") toast(t(($) => $.screen.already));
        else if (typeof message === "string" && message) toast.success(message);
        return;
      }
      case "confirm":
        setPhase("idle");
        setConfirm({ title: res.confirm?.title || label, text: res.confirm?.text ?? "" });
        return;
      case "cancelled":
        setPhase("idle");
        return;
      default:
        setPhase("idle");
        toast.error(t(($) => $.screen.action_failed));
    }
  };
  const fail = (err: unknown) => {
    const stale = err instanceof ApiError && err.status === 410;
    setPhase(stale ? "done" : "idle");
    toast.error(stale ? t(($) => $.screen.stale) : t(($) => $.screen.action_failed));
  };
  const run = (withConfirm: boolean) => {
    if (!actionId) return;
    setPhase("busy");
    press.mutateAsync({ actionId, confirm: withConfirm }).then(finish, fail);
  };

  const onClick = () => {
    // Opened inside the click so the browser does not block the new tab;
    // the press only records it.
    const url = openUrl(action);
    if (url) window.open(url, "_blank", "noopener,noreferrer");
    run(false);
  };
  const onConfirmOpenChange = (open: boolean) => {
    if (open || !confirm) return;
    setConfirm(null);
    if (actionId) cancel.mutate(actionId);
  };

  const disabled = !env.interactive || !actionId || phase !== "idle";
  return (
    <>
      <Button
        type="button"
        size="sm"
        variant={zone === "red" ? "destructive" : variant}
        disabled={disabled}
        aria-busy={phase === "busy"}
        onClick={onClick}
        className="max-w-full"
      >
        {phase === "done" ? <Check data-icon="inline-start" aria-label={t(($) => $.screen.done)} /> : null}
        <span className="truncate">{label}</span>
      </Button>
      <AlertDialog open={confirm !== null} onOpenChange={onConfirmOpenChange}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirm?.title}</AlertDialogTitle>
            {confirm?.text ? <AlertDialogDescription>{confirm.text}</AlertDialogDescription> : null}
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.screen.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                setConfirm(null);
                run(true);
              }}
            >
              {t(($) => $.screen.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
