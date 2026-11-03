"use client";

import { useMemo, useState } from "react";
import { Plus, Diamond, Search } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { epicListOptions } from "@multica/core/epics";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useModalStore } from "@multica/core/modals";
import { resolvePlanningProgress } from "@multica/core/planning";
import type { Epic } from "@multica/core/types";
import { AppLink } from "../../navigation";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { PageHeader } from "../../layout/page-header";
import { useLocale, useT } from "../../i18n";
import { PlanningListProgress } from "../../planning";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";

function EpicRow({ epic }: { epic: Epic }) {
  const { t } = useT("epics");
  const { t: tPlanning } = useT("planning");
  const locale = useLocale();
  const wsPaths = useWorkspacePaths();
  const progress = resolvePlanningProgress(epic);
  const open = progress.total - progress.done - progress.cancelled;

  return (
    <li>
      <AppLink
        href={wsPaths.epicDetail(epic.id)}
        className="flex min-h-12 items-center gap-3 px-5 py-2 text-body hover:bg-surface-hover transition-colors"
      >
        <span className="size-3.5 shrink-0 rounded-sm" style={{ backgroundColor: epic.color }} />
        <div className="min-w-0 flex-1">
          <div className="truncate font-medium">{epic.title}</div>
          {epic.description ? (
            <div className="truncate text-caption text-muted-foreground">{epic.description}</div>
          ) : null}
        </div>
        <span
          className={`shrink-0 rounded-full px-1.5 py-0.5 text-micro ${
            epic.status === "open" ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground"
          }`}
        >
          {t(($) => $.status[epic.status])}
        </span>
        <PlanningListProgress progress={progress} />
        <span className="hidden w-28 shrink-0 text-caption text-muted-foreground tabular-nums lg:block">
          {progress.total > 0 ? tPlanning(($) => $.list.open, { count: open }) : null}
          {progress.blocked > 0 ? (
            <span className="block text-destructive">{tPlanning(($) => $.list.blocked, { count: progress.blocked })}</span>
          ) : null}
        </span>
        <span className="hidden w-36 shrink-0 text-right text-caption text-muted-foreground md:block">
          {tPlanning(($) => $.list.created, {
            date: new Date(epic.created_at).toLocaleDateString(locale, { year: "numeric", month: "short", day: "numeric" }),
          })}
        </span>
      </AppLink>
    </li>
  );
}

export function EpicsPage() {
  const { t } = useT("epics");
  const wsId = useWorkspaceId();
  const { data: epics = [], isLoading } = useQuery(epicListOptions(wsId));
  const openCreateEpic = () => useModalStore.getState().open("create-epic");

  const [search, setSearch] = useState("");
  const filteredEpics = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return epics;
    return epics.filter(
      (e) =>
        e.title.toLowerCase().includes(q) ||
        (e.description && e.description.toLowerCase().includes(q)) ||
        matchesPinyin(e.title, q),
    );
  }, [epics, search]);

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <Diamond className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">{t(($) => $.page.title)}</h1>
          {!isLoading && epics.length > 0 && (
            <span className="text-xs text-muted-foreground tabular-nums">
              {epics.length}
            </span>
          )}
        </div>
        <Button size="sm" variant="outline" onClick={openCreateEpic}>
          <Plus className="h-3.5 w-3.5 mr-1" />
          {t(($) => $.page.new_epic)}
        </Button>
      </PageHeader>

      <div className="flex flex-1 min-h-0 flex-col overflow-hidden">
        {(epics.length > 0 || isLoading) && (
          <div className="flex h-12 shrink-0 items-center justify-between border-b px-4">
            <div className="relative flex-1 sm:flex-none">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t(($) => $.page.search_placeholder)}
                className="h-8 w-full sm:w-64 pl-8 text-sm"
              />
            </div>
            <span className="hidden sm:inline-block font-mono text-xs tabular-nums text-muted-foreground">
              {filteredEpics.length} / {epics.length}
            </span>
          </div>
        )}

        <div className="flex-1 overflow-y-auto">
          {isLoading ? (
            <div className="pt-4 px-5 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
              {Array.from({ length: 4 }).map((_, i) => (
                <div key={i} className="flex flex-col rounded-md border p-3 gap-2">
                  <div className="flex items-center gap-2">
                    <Skeleton className="h-4 w-4 rounded" />
                    <Skeleton className="h-4 w-3/4" />
                  </div>
                  <Skeleton className="h-3 w-full" />
                </div>
              ))}
            </div>
          ) : epics.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-24 text-muted-foreground">
              <Diamond className="h-10 w-10 mb-3 opacity-30" />
              <p className="text-sm">{t(($) => $.page.empty)}</p>
              <Button
                size="sm"
                variant="outline"
                className="mt-3"
                onClick={openCreateEpic}
              >
                {t(($) => $.page.create_first)}
              </Button>
            </div>
          ) : filteredEpics.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-24 text-muted-foreground">
              <Search className="h-10 w-10 mb-3 opacity-30" />
              <p className="text-sm">{t(($) => $.page.no_search_results)}</p>
            </div>
          ) : (
            <ul className="divide-y border-b">
              {filteredEpics.map((epic) => (
                <EpicRow key={epic.id} epic={epic} />
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}
