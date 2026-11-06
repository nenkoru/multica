"use client";

import { useQuery } from "@tanstack/react-query";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import { multikaOptions, streamListOptions } from "@multica/core/streams";
import { MessageSquare } from "@multica/ui/icons";
import { cn } from "@multica/ui/lib/utils";
import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@multica/ui/components/ui/sidebar";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import { multikaChatHref, streamStateDotClass } from "./stream-state";

/**
 * Sidebar pulse of the Fabrika streams: one line per stream with its state,
 * plus the entry to Мультика's one chat. Renders nothing on servers without
 * the streams API.
 */
export function StreamsSidebarGroup({ itemClassName }: { itemClassName?: string }) {
  const { t } = useT("streams");
  // The sidebar also renders outside a workspace (switcher, invitations).
  const wsId = useCurrentWorkspace()?.id ?? "";
  const p = useWorkspacePaths();
  const { data: streams = [] } = useQuery({ ...streamListOptions(wsId), enabled: !!wsId });
  const { data: multika } = useQuery({ ...multikaOptions(wsId), enabled: !!wsId });
  if (streams.length === 0 && !multika?.enabled) return null;
  return (
    <SidebarGroup>
      <SidebarGroupLabel render={<AppLink href={p.streams()} />}>{t(($) => $.title)}</SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu className="gap-0.5">
          {multika?.enabled && (
            <SidebarMenuItem>
              <SidebarMenuButton
                render={<AppLink href={multikaChatHref(p.chat(), multika.chat_session_id)} />}
                className={itemClassName}
              >
                <MessageSquare />
                <span className="truncate">{t(($) => $.multika)}</span>
                {multika.focus_title && (
                  <span className="ml-auto truncate text-caption text-muted-foreground">{multika.focus_title}</span>
                )}
              </SidebarMenuButton>
            </SidebarMenuItem>
          )}
          {streams.map((s) => (
            <SidebarMenuItem key={s.id}>
              <SidebarMenuButton render={<AppLink href={p.streams()} />} className={itemClassName}>
                <span
                  aria-hidden
                  className={cn("mx-1 size-2 shrink-0 rounded-full", streamStateDotClass(s.state))}
                />
                <span className="truncate">{s.title || s.slug}</span>
                <span className="ml-auto shrink-0 text-caption text-muted-foreground">{s.state_label}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  );
}
