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

/** A screen button. A red action answers {status:"confirm"} with the text
 *  of the confirmation window; the window presses again with confirm. */
export function usePressScreenAction() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ actionId, confirm }: { actionId: string; confirm: boolean }) =>
      api.pressScreenAction(actionId, confirm),
    // Shares, pauses and focus show in the streams list and Мультика's state.
    onSuccess: (res) => {
      if (res.status !== "done") return;
      qc.invalidateQueries({ queryKey: streamKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: streamKeys.multika(wsId) });
    },
  });
}

/** The confirmation window was closed without confirming. */
export function useCancelScreenAction() {
  return useMutation({
    mutationFn: (actionId: string) => api.cancelScreenAction(actionId),
  });
}
