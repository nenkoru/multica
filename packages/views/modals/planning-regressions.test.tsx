import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CreateEpicModal } from "./create-epic";
import { CreateSprintModal } from "./create-sprint";
import { EpicDetail } from "../epics/components/epic-detail";
import { SprintDetail } from "../sprints/components/sprint-detail";
import epics from "../locales/en/epics.json";
import sprints from "../locales/en/sprints.json";

const mocks = vi.hoisted(() => ({
  copy: vi.fn(async () => true), push: vi.fn(), close: vi.fn(), create: vi.fn(),
  pending: true, pathname: "/acme/epics/entity",
}));
const entity = {
  id: "entity", title: "Epic", name: "Sprint", status: "planned", color: "#6366f1",
  description: "", goal: "", issue_count: 0, start_date: "2026-10-09", end_date: "2026-10-16",
};
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("@multica/core/paths", () => ({ useWorkspacePaths: () => ({
  epics: () => "/acme/epics", sprints: () => "/acme/sprints",
  epicDetail: (id: string) => `/acme/epics/${id}`, sprintDetail: (id: string) => `/acme/sprints/${id}`,
}) }));
vi.mock("../navigation", async (importOriginal) => ({
  ...await importOriginal<typeof import("../navigation")>(),
  useNavigation: () => ({
    push: mocks.push, pathname: mocks.pathname, searchParams: new URLSearchParams("from=desktop"), hash: "#notes",
    getShareableUrl: (path: string) => "https://app.example.test" + path,
  }),
}));
vi.mock("../i18n", () => ({ useT: (ns: string) => ({
  t: (fn: (copy: unknown) => string) => fn(ns === "epics" ? epics : sprints),
}) }));
vi.mock("@multica/ui/lib/clipboard", () => ({ copyText: mocks.copy }));
vi.mock("../editor", () => ({ TitleEditor: () => null, ContentEditor: () => null }));
vi.mock("@multica/core/epics", () => ({
  useCreateEpic: () => ({ isPending: mocks.pending, mutate: mocks.create }),
  useUpdateEpic: () => ({ mutate: vi.fn() }), useDeleteEpic: () => ({ mutate: vi.fn(), isPending: false }),
  epicDetailOptions: () => ({ queryKey: ["epic"], queryFn: async () => ({ ...entity, status: "open" }) }),
}));
vi.mock("@multica/core/sprints", () => ({
  useCreateSprint: () => ({ isPending: mocks.pending, mutate: mocks.create }),
  useUpdateSprint: () => ({ mutate: vi.fn() }), useDeleteSprint: () => ({ mutate: vi.fn(), isPending: false }),
  sprintDetailOptions: () => ({ queryKey: ["sprint"], queryFn: async () => entity }),
}));

beforeEach(() => { mocks.pending = true; });
afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("planning modal and Desktop regressions", () => {
  it.each([["epic", CreateEpicModal], ["sprint", CreateSprintModal]] as const)(
    "%s preserves the form during pending dismissal and permits closing afterwards", (_name, Modal) => {
      const view = render(<Modal onClose={mocks.close} />);
      const name = screen.getAllByRole("textbox")[0];
      fireEvent.change(name, { target: { value: "Keep this input" } });
      expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
      fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
      fireEvent.click(screen.getByRole("button", { name: "Close" }));
      fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
      expect(mocks.close).not.toHaveBeenCalled();
      expect(name).toHaveValue("Keep this input");
      mocks.pending = false;
      view.rerender(<Modal onClose={mocks.close} />);
      expect(name).toHaveValue("Keep this input");
      fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
      expect(mocks.close).toHaveBeenCalledOnce();
    },
  );

  it.each([["epic", CreateEpicModal], ["sprint", CreateSprintModal]] as const)(
    "%s retains input on server failure and closes after success", (noun, Modal) => {
      mocks.pending = false;
      render(<Modal onClose={mocks.close} />);
      const name = screen.getAllByRole("textbox")[0];
      fireEvent.change(name, { target: { value: "Keep this input" } });
      if (noun === "sprint") {
        const dates = document.querySelectorAll("input[type=date]");
        fireEvent.change(dates[0], { target: { value: "2026-10-09" } });
        fireEvent.change(dates[1], { target: { value: "2026-10-16" } });
      }
      fireEvent.click(screen.getByRole("button", { name: noun === "epic" ? epics.create.submit : sprints.create.submit }));
      expect(mocks.create).toHaveBeenCalledOnce();
      const callbacks = mocks.create.mock.calls[0][1];
      act(() => callbacks.onError(new Error("Server failure")));
      expect(name).toHaveValue("Keep this input");
      expect(mocks.close).not.toHaveBeenCalled();
      act(() => callbacks.onSuccess({ id: "created" }));
      expect(mocks.close).toHaveBeenCalledOnce();
      expect(mocks.push).toHaveBeenCalledWith(`/acme/${noun}s/created`);
    },
  );

  it.each(["epic", "sprint"])("%s copies a shareable route including query and hash", async (noun) => {
    mocks.pathname = `/acme/${noun}s/entity`;
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { container } = render(
      <QueryClientProvider client={client}>
        {noun === "epic" ? <EpicDetail epicId="entity" /> : <SprintDetail sprintId="entity" />}
      </QueryClientProvider>,
    );
    await screen.findByText(noun === "epic" ? "Open" : "Planned");
    fireEvent.click(container.querySelector("button")!);
    expect(mocks.copy).toHaveBeenCalledWith(`https://app.example.test/acme/${noun}s/entity?from=desktop#notes`);
    client.clear();
  });
});
