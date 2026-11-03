"use client";

import { useState } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { Diamond } from "@multica/ui/icons";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  streamDetailOptions,
  streamListOptions,
  type StreamDetail,
  type StreamView,
} from "@multica/core/streams";
import { PageHeader } from "../layout/page-header";

/* eslint-disable i18next/no-literal-string -- Fabrika pages are Russian-only (fork) */

const MSK = "Europe/Moscow";

function msk(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${new Intl.DateTimeFormat("ru-RU", {
    timeZone: MSK,
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(d)} МСК`;
}

// green — идёт, yellow — стоит, red — ждёт Руслана.
function pulse(s: StreamView): { color: string; label: string } {
  if (s.state === "asking") return { color: "bg-red-500", label: "ждёт тебя" };
  if (s.state === "running") return { color: "bg-green-500", label: "идёт" };
  if (s.state === "broken") return { color: "bg-red-500", label: s.state_label };
  return { color: "bg-yellow-500", label: s.state_label || s.state };
}

function Row({ s, d, open, onClick }: { s: StreamView; d?: StreamDetail; open: boolean; onClick: () => void }) {
  const p = pulse(s);
  const cur = d?.steps.find((x) => x.status === "running" || x.status === "verifying") ?? d?.steps[0];
  const waits = s.state === "asking" || (d?.asks.length ?? 0) > 0;
  const goal = s.charter?.goal || s.summary;
  return (
    <li className="border-b">
      <button
        type="button"
        onClick={onClick}
        className="flex w-full items-start gap-3 px-5 py-3 text-left text-body hover:bg-surface-hover transition-colors"
      >
        <span title={p.label} className={`mt-1.5 size-2.5 shrink-0 rounded-full ${p.color}`} />
        <div className="min-w-0 flex-1">
          <div className="truncate font-medium">{s.title}</div>
          {goal ? <div className="truncate text-caption text-muted-foreground">{goal}</div> : null}
          <div className="truncate text-caption text-muted-foreground">
            Шаг: {cur ? `#${cur.seq} ${cur.brief}` : "—"}
          </div>
          <div className="text-caption text-muted-foreground">
            Веха: {msk(s.last_milestone_at)} · {p.label}
          </div>
        </div>
        <span
          className={`shrink-0 rounded-full px-2 py-0.5 text-micro ${
            waits ? "bg-red-500/10 text-red-600" : "bg-muted text-muted-foreground"
          }`}
        >
          ждёт тебя: {waits ? "да" : "нет"}
        </span>
      </button>
      {open && d ? (
        <div className="space-y-3 bg-muted/30 px-12 py-3 text-caption">
          <div>
            <div className="mb-1 font-medium">Вопросы к тебе</div>
            {d.asks.length === 0 ? (
              <div className="text-muted-foreground">Нет открытых вопросов</div>
            ) : (
              <ul className="space-y-1">
                {d.asks.map((a) => (
                  <li key={a.id}>• {a.text}</li>
                ))}
              </ul>
            )}
          </div>
          <div>
            <div className="mb-1 font-medium">Шаги (последние 20)</div>
            {d.steps.length === 0 ? (
              <div className="text-muted-foreground">Шагов пока нет</div>
            ) : (
              <ul className="space-y-1">
                {d.steps.slice(0, 20).map((x) => (
                  <li key={x.id}>
                    #{x.seq} [{x.status}] {x.brief}
                    {x.last_error ? <span className="text-red-600"> — {x.last_error}</span> : null}
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      ) : null}
    </li>
  );
}

export function StreamsPage() {
  const wsId = useWorkspaceId();
  const { data: streams = [], isLoading, isError } = useQuery(streamListOptions(wsId));
  const details = useQueries({ queries: streams.map((s) => streamDetailOptions(wsId, s.slug)) });
  const [openSlug, setOpenSlug] = useState<string | null>(null);

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <Diamond className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">Потоки</h1>
          {streams.length > 0 && (
            <span className="text-xs text-muted-foreground tabular-nums">{streams.length}</span>
          )}
        </div>
      </PageHeader>
      <div className="flex-1 overflow-y-auto">
        {isLoading ? (
          <div className="space-y-2 p-5">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </div>
        ) : isError ? (
          <div className="p-5 text-sm text-muted-foreground">Не удалось загрузить потоки</div>
        ) : streams.length === 0 ? (
          <div className="p-10 text-center text-sm text-muted-foreground">Потоков пока нет</div>
        ) : (
          <ul>
            {streams.map((s, i) => (
              <Row
                key={s.id}
                s={s}
                d={details[i]?.data}
                open={openSlug === s.slug}
                onClick={() => setOpenSlug(openSlug === s.slug ? null : s.slug)}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
