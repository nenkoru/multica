import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CreateEpicModal } from "./create-epic";
import { CreateSprintModal } from "./create-sprint";
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
  it("gives each sprint form uniquely labeled date fields", () => {
    const first = render(<CreateSprintModal onClose={mocks.close} />);
    const start = screen.getByLabelText(sprints.create.start_date);
    const end = screen.getByLabelText(sprints.create.end_date);
    expect(start).toHaveAttribute("type", "date");
    expect(end).toHaveAttribute("type", "date");
    expect(start.id).not.toBe(end.id);
    const firstId = start.id;
    first.unmount();
    render(<CreateSprintModal onClose={mocks.close} />);
    expect(screen.getByLabelText(sprints.create.start_date).id).not.toBe(firstId);
  });

  it.each([["epic", CreateEpicModal], ["sprint", CreateSprintModal]] as const)(
    "%s preserves the form during pending dismissal and permits closing afterwards", (_name, Modal) => {
      const view = render(<Modal onClose={mocks.close} />);
      const name = screen.getByLabelText(_name === "epic" ? epics.create.label_title : sprints.create.label_name);
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
      const name = screen.getByLabelText(noun === "epic" ? epics.create.label_title : sprints.create.label_name);
      fireEvent.change(name, { target: { value: "Keep this input" } });
      if (noun === "sprint") {
        fireEvent.change(screen.getByLabelText(sprints.create.start_date), { target: { value: "2026-10-09" } });
        fireEvent.change(screen.getByLabelText(sprints.create.end_date), { target: { value: "2026-10-16" } });
      }
      fireEvent.click(screen.getByRole("button", { name: noun === "epic" ? epics.create.submit : sprints.create.submit }));
      expect(mocks.create).toHaveBeenCalledOnce();
      const callbacks = mocks.create.mock.calls[0]![1];
      act(() => callbacks.onError(new Error("Server failure")));
      expect(name).toHaveValue("Keep this input");
      expect(mocks.close).not.toHaveBeenCalled();
      act(() => callbacks.onSuccess({ id: "created" }));
      expect(mocks.close).toHaveBeenCalledOnce();
      expect(mocks.push).toHaveBeenCalledWith(`/acme/${noun}s/created`);
    },
  );
});
