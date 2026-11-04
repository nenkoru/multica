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

/** Turns a recorded voice note into text for the composer. */
export function useTranscribeMultikaVoice() {
  return useMutation({
    mutationFn: ({ audio, filename }: { audio: Blob; filename: string }) =>
      api.transcribeMultikaVoice(audio, filename),
  });
}
