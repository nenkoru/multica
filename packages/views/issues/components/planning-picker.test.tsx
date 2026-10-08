import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import epics from "../../locales/en/epics.json";
import sprints from "../../locales/en/sprints.json";
import { EpicPicker } from "./epic-picker";
import { SprintPicker } from "./sprint-picker";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("../../i18n", () => ({
  useT: (namespace: string) => ({
    t: (select: (copy: unknown) => string) => select(namespace === "epics" ? epics : sprints),
  }),
}));
vi.mock("@multica/core/epics/queries", () => ({
  epicListOptions: (wsId: string) => ({
    queryKey: ["epics", wsId],
    queryFn: async () => [{ id: "epic-one", title: "Epic One", color: "#6366f1" }],
  }),
}));
vi.mock("@multica/core/sprints/queries", () => ({
  sprintListOptions: (wsId: string) => ({
    queryKey: ["sprints", wsId],
    queryFn: async () => [{ id: "sprint-one", name: "Sprint One", status: "planned" }],
  }),
}));

describe("planning issue pickers", () => {
  it("sets and clears each relation with no parent/stage/project writes", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const onUpdate = vi.fn();
    const wrapper = ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const epic = render(<EpicPicker epicId={null} onUpdate={onUpdate} />, { wrapper });
    fireEvent.click(screen.getByRole("button", { name: "No epic" }));
    fireEvent.click(await screen.findByText("Epic One"));
    expect(onUpdate).toHaveBeenLastCalledWith({ epic_id: "epic-one" });
    epic.rerender(<EpicPicker epicId="epic-one" onUpdate={onUpdate} />);
    fireEvent.click(screen.getByRole("button", { name: "Epic One" }));
    fireEvent.click(await screen.findByText("Remove epic"));
    expect(onUpdate).toHaveBeenLastCalledWith({ epic_id: null });
    epic.unmount();

    const sprint = render(<SprintPicker sprintId={null} onUpdate={onUpdate} />, { wrapper });
    fireEvent.click(screen.getByRole("button", { name: "No sprint" }));
    fireEvent.click(await screen.findByText("Sprint One"));
    expect(onUpdate).toHaveBeenLastCalledWith({ sprint_id: "sprint-one" });
    sprint.rerender(<SprintPicker sprintId="sprint-one" onUpdate={onUpdate} />);
    fireEvent.click(screen.getByRole("button", { name: "Sprint One" }));
    fireEvent.click(await screen.findByText("Remove sprint"));
    expect(onUpdate).toHaveBeenLastCalledWith({ sprint_id: null });
    client.clear();
  });
});
