// @vitest-environment jsdom
import { StrictMode, type PropsWithChildren } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useIssueSurfaceAudit } from "./use-issue-surface-audit";

const record = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
vi.mock("../api", () => ({ api: { recordIssueSurfaceOpen: record } }));

function wrapper({ children }: PropsWithChildren) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  return <QueryClientProvider client={client}><StrictMode>{children}</StrictMode></QueryClientProvider>;
}

describe("issue surface audit", () => {
  beforeEach(() => record.mockClear());
  it("counts one mount despite StrictMode replay and data rerenders", async () => {
    const { rerender } = renderHook(() => useIssueSurfaceAudit("workspace"), { wrapper });
    await waitFor(() => expect(record).toHaveBeenCalledTimes(1));
    rerender();
    expect(record).toHaveBeenCalledTimes(1);
    expect(record.mock.calls[0]?.[0]).toMatch(/^[0-9a-f-]{36}$/);
    expect(record.mock.calls[0]?.[1]).toBeUndefined();
  });
  it("records scope changes with distinct IDs and no issue content", async () => {
    const { rerender } = renderHook(({ ws, project }) => useIssueSurfaceAudit(ws, project), {
      initialProps: { ws: "one", project: "project-one" }, wrapper,
    });
    await waitFor(() => expect(record).toHaveBeenCalledTimes(1));
    rerender({ ws: "one", project: "project-two" });
    await waitFor(() => expect(record).toHaveBeenCalledTimes(2));
    expect(record.mock.calls[0]?.[0]).not.toBe(record.mock.calls[1]?.[0]);
    expect(record.mock.calls[1]?.[1]).toBe("project-two");
    expect(record.mock.calls[1]).toHaveLength(2);
  });
  it("does not record before workspace resolution", () => {
    renderHook(() => useIssueSurfaceAudit(""), { wrapper });
    expect(record).not.toHaveBeenCalled();
  });
});
