"use client";

import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useWSEvent, useWSReconnect } from "../realtime/hooks";
import { streamKeys } from "./queries";
import { ScreenFinalEventSchema, ScreenLineEventSchema, type ListScreenBlocksResponse } from "./schemas";
import { applyScreenFinal, applyScreenLine } from "./screens";

/** Patches a chat's screens cache from chat:ui_line / chat:ui_final. */
export function useScreenRealtime(wsId: string, sessionId: string) {
  const qc = useQueryClient();

  const onLine = useCallback(
    (payload: unknown) => {
      const ev = ScreenLineEventSchema.safeParse(payload);
      if (!ev.success || ev.data.chat_session_id !== sessionId) return;
      qc.setQueryData<ListScreenBlocksResponse>(streamKeys.screens(wsId, sessionId), (d) => applyScreenLine(d, ev.data));
    },
    [qc, wsId, sessionId],
  );
  const onFinal = useCallback(
    (payload: unknown) => {
      const ev = ScreenFinalEventSchema.safeParse(payload);
      if (!ev.success || ev.data.chat_session_id !== sessionId) return;
      qc.setQueryData<ListScreenBlocksResponse>(streamKeys.screens(wsId, sessionId), (d) => applyScreenFinal(d, ev.data));
    },
    [qc, wsId, sessionId],
  );
  const onReconnect = useCallback(() => {
    qc.invalidateQueries({ queryKey: streamKeys.screens(wsId, sessionId) });
  }, [qc, wsId, sessionId]);

  useWSEvent("chat:ui_line", onLine);
  useWSEvent("chat:ui_final", onFinal);
  useWSReconnect(onReconnect);
}
