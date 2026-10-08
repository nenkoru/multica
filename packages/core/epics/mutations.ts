import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { epicKeys } from "./queries";
import { useWorkspaceId } from "../hooks";
import type { Epic } from "../types";

export function useCreateEpic() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: { title: string; description?: string; color?: string }) =>
      api.createEpic(data, wsId),
    onSuccess: (newEpic) => {
      qc.setQueryData<{ epics: Epic[]; total: number }>(epicKeys.list(wsId), (old) =>
        old && !old.epics.some((e) => e.id === newEpic.id)
          ? { epics: [...old.epics, newEpic], total: old.total + 1 }
          : old,
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["issues", wsId] });
      qc.invalidateQueries({ queryKey: epicKeys.list(wsId) });
    },
  });
}

export function useUpdateEpic() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string } & Record<string, unknown>) =>
      api.updateEpic(id, data, wsId),
    onMutate: async ({ id, ...data }) => {
      await qc.cancelQueries({ queryKey: epicKeys.list(wsId) });
      await qc.cancelQueries({ queryKey: epicKeys.detail(wsId, id) });
      const prevList = qc.getQueryData<{ epics: Epic[]; total: number }>(epicKeys.list(wsId));
      const prevDetail = qc.getQueryData<Epic>(epicKeys.detail(wsId, id));
      qc.setQueryData<{ epics: Epic[]; total: number }>(epicKeys.list(wsId), (old) =>
        old ? { ...old, epics: old.epics.map((e) => (e.id === id ? { ...e, ...data } : e)) } : old,
      );
      qc.setQueryData<Epic>(epicKeys.detail(wsId, id), (old) =>
        old ? { ...old, ...data } : old,
      );
      return { prevList, prevDetail, id };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prevList) qc.setQueryData(epicKeys.list(wsId), ctx.prevList);
      if (ctx?.prevDetail) qc.setQueryData(epicKeys.detail(wsId, ctx.id), ctx.prevDetail);
    },
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({ queryKey: epicKeys.detail(wsId, vars.id) });
      qc.invalidateQueries({ queryKey: epicKeys.list(wsId) });
    },
  });
}

export function useDeleteEpic() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.deleteEpic(id, wsId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["issues", wsId] });
      qc.invalidateQueries({ queryKey: epicKeys.list(wsId) });
    },
  });
}
