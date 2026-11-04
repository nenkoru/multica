import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { streamKeys } from "./queries";

/** Sets (slug) or clears ("") Мультика's focus in the one chat. */
export function useSetMultikaFocus() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (slug: string) => api.setMultikaFocus(slug),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: streamKeys.multika(wsId) });
    },
  });
}
