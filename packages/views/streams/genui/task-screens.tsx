"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight } from "@multica/ui/icons";
import { screenBlocksOptions, screenSources, screensOfTask, standaloneScreens, type ScreenBlock } from "@multica/core/streams";
import { useT } from "../../i18n";
import { ScreenFallback, ScreenView } from "./screen-view";

/** The screens of one reply, under its text. */
export function TaskScreens({
  wsId,
  sessionId,
  taskId,
  content,
  onFollowup,
}: {
  wsId: string;
  sessionId: string;
  taskId: string;
  content?: string;
  onFollowup?: (text: string) => void;
}) {
  const { data: blocks = [], isPending } = useQuery({
    ...screenBlocksOptions(wsId, sessionId),
    select: (d) => screensOfTask(d.blocks, taskId),
  });
  const sources = screenSources(content ?? "");
  if (blocks.length === 0 && sources.length === 0) return null;
  const modelSeqs = new Set(blocks.filter((b) => b.kind === "model").map((b) => b.seq));
  const entries: { seq: number; block?: ScreenBlock; source?: string }[] = blocks.map((block) => ({
    seq: block.seq, block, source: block.kind === "model" ? sources[block.seq - 10] : undefined,
  }));
  sources.forEach((source, i) => {
    if (!modelSeqs.has(10 + i)) entries.push({ seq: 10 + i, source });
  });
  entries.sort((a, b) => a.seq - b.seq);
  return (
    <div className="mt-2 flex flex-col gap-3">
      {entries.map(({ seq, block, source }) => block ? (
        <ScreenView key={block.id} block={block} source={source} onFollowup={onFollowup} />
      ) : <ScreenFallback key={`source:${seq}`} source={source} loading={isPending} />)}
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
