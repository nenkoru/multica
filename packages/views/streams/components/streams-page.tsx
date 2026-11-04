"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { multikaOptions, streamListOptions, useSetMultikaFocus } from "@multica/core/streams";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { PageHeader } from "../../layout/page-header";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import { multikaChatHref, streamStateDotClass } from "./stream-state";

/**
 * Fabrika streams and Мультика's one chat (harness-design §7, §9.2): what each
 * stream is doing, where Мультика's focus is, and the way into the chat.
 */
export function StreamsPage() {
  const { t } = useT("streams");
  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const streamsQuery = useQuery(streamListOptions(wsId));
  const { data: multika } = useQuery(multikaOptions(wsId));
  const setFocus = useSetMultikaFocus();
  const streams = streamsQuery.data ?? [];
  const focus = multika?.focus_slug ?? "";

  return (
    <div className="flex h-full flex-col">
      <PageHeader>
        <span className="text-body font-medium">{t(($) => $.title)}</span>
      </PageHeader>
      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-3xl space-y-6 p-6">
          {multika?.enabled && (
            <section className="flex flex-wrap items-center gap-3 rounded-lg border p-4">
              <div className="min-w-0 flex-1">
                <div className="text-body font-medium">{t(($) => $.multika)}</div>
                <div className="truncate text-caption text-muted-foreground">
                  {multika.focus_title
                    ? t(($) => $.focus_on, { title: multika.focus_title })
                    : t(($) => $.no_focus)}
                </div>
              </div>
              {focus && (
                <Button variant="outline" size="sm" disabled={setFocus.isPending} onClick={() => setFocus.mutate("")}>
                  {t(($) => $.clear_focus)}
                </Button>
              )}
              <AppLink
                href={multikaChatHref(p.chat(), multika.chat_session_id)}
                className={buttonVariants({ size: "sm" })}
              >
                {t(($) => $.open_chat)}
              </AppLink>
            </section>
          )}

          {streamsQuery.isError && (
            <p className="text-body text-muted-foreground">{t(($) => $.unavailable)}</p>
          )}
          {streamsQuery.isSuccess && streams.length === 0 && (
            <p className="text-body text-muted-foreground">{t(($) => $.empty)}</p>
          )}

          <ul className="space-y-2">
            {streams.map((s) => (
              <li key={s.id} className="rounded-lg border p-4" data-testid={`stream-${s.slug}`}>
                <div className="flex items-center gap-2">
                  <span aria-hidden className={cn("size-2 shrink-0 rounded-full", streamStateDotClass(s.state))} />
                  <span className="min-w-0 truncate text-body font-medium">{s.title || s.slug}</span>
                  <span className="text-caption text-muted-foreground">{s.state_label}</span>
                  {multika?.enabled && s.slug !== focus && (
                    <Button
                      className="ml-auto"
                      variant="outline"
                      size="xs"
                      disabled={setFocus.isPending}
                      onClick={() => setFocus.mutate(s.slug)}
                    >
                      {t(($) => $.set_focus)}
                    </Button>
                  )}
                </div>
                {s.reason && <p className="mt-1 text-caption text-muted-foreground">{s.reason}</p>}
                {s.summary && <p className="mt-2 line-clamp-3 whitespace-pre-line text-caption">{s.summary}</p>}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}
