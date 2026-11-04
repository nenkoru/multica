"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight } from "@multica/ui/icons";
import { screenBlocksOptions, screensOfTask, standaloneScreens } from "@multica/core/streams";
import { useT } from "../../i18n";
import { ScreenView } from "./screen-view";

/** The screens of one reply, under its text. */
export function TaskScreens({
  wsId,
  sessionId,
  taskId,
  onFollowup,
}: {
  wsId: string;
  sessionId: string;
  taskId: string;
  onFollowup?: (text: string) => void;
}) {
  const { data: blocks = [] } = useQuery({
    ...screenBlocksOptions(wsId, sessionId),
    select: (d) => screensOfTask(d.blocks, taskId),
  });
  if (blocks.length === 0) return null;
  return (
    <div className="mt-2 flex flex-col gap-3">
      {blocks.map((b) => (
        <ScreenView key={b.id} block={b} onFollowup={onFollowup} />
      ))}
    </div>
  );
}

/** The latest screen that belongs to no reply — the morning digest. */
export function DigestScreen({
  wsId,
  sessionId,
  onFollowup,
}: {
  wsId: string;
  sessionId: string;
  onFollowup?: (text: string) => void;
}) {
  const { t } = useT("streams");
  const [open, setOpen] = useState(false);
  const { data: digest } = useQuery({
    ...screenBlocksOptions(wsId, sessionId),
    select: (d) => standaloneScreens(d.blocks)[0] ?? null,
  });
  if (!digest) return null;
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <div className="border-b border-border px-4 py-2">
      <button
        type="button"
        className="flex items-center gap-1 text-label text-muted-foreground hover:text-foreground"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <Chevron className="size-4" />
        {t(($) => $.screen.digest)}
        {digest.created_at ? (
          <span className="ml-1 tabular-nums">
            {new Date(digest.created_at).toLocaleString(undefined, { dateStyle: "short", timeStyle: "short" })}
          </span>
        ) : null}
      </button>
      {open ? (
        <div className="mt-2 max-h-[50vh] overflow-y-auto">
          <ScreenView block={digest} onFollowup={onFollowup} />
        </div>
      ) : null}
    </div>
  );
}
