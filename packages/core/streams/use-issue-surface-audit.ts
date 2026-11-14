import { useEffect, useRef } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../api";

/** A page opening is a surface mount, including list/table and empty results.
 * StrictMode effect replay and network retries reuse the same event ID. */
export function useIssueSurfaceAudit(workspaceId: string, projectId?: string) {
  const sent = useRef<string | null>(null);
  const event = useRef(crypto.randomUUID());
  const { mutate } = useMutation({
    mutationFn: ({ id, project }: { id: string; project?: string }) =>
      api.recordIssueSurfaceOpen(id, project),
    retry: false,
  });
  useEffect(() => {
    const scope = `${workspaceId}:${projectId ?? ""}`;
    if (!workspaceId || sent.current === scope) return;
    if (sent.current !== null) event.current = crypto.randomUUID();
    sent.current = scope;
    mutate({ id: event.current, project: projectId });
  }, [workspaceId, projectId, mutate]);
}
