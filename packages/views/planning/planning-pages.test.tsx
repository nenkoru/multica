import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Sprint } from "@multica/core/types";
import { EpicDetail } from "../epics/components/epic-detail";
import { SprintDetail } from "../sprints/components/sprint-detail";
import { CompleteSprintDialog, sprintMoveTargets } from "./complete-sprint-dialog";
import common from "../locales/en/common.json";
import epics from "../locales/en/epics.json";
import issuesCopy from "../locales/en/issues.json";
import planning from "../locales/en/planning.json";
import sprints from "../locales/en/sprints.json";

const ZERO = { total: 0, done: 0, in_progress: 0, in_review: 0, blocked: 0, not_started: 0, cancelled: 0 };

const mocks = vi.hoisted(() => ({
  copy: vi.fn(async () => true),
  push: vi.fn(),
  openModal: vi.fn(),
  updateIssue: vi.fn(),
  updateSprint: vi.fn(),
  completeSprint: vi.fn(),
  pathname: "/acme/epics/entity",
  epic: {} as object,
  sprint: {} as object,
  epicOverview: {} as object,
  sprintOverview: {} as object,
  issues: [] as object[],
  issuesTotal: null as number | null,
  epicList: [] as object[],
  sprintList: [] as object[],
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace" }));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    epics: () => "/acme/epics",
    sprints: () => "/acme/sprints",
    epicDetail: (id: string) => `/acme/epics/${id}`,
    sprintDetail: (id: string) => `/acme/sprints/${id}`,
    issueDetail: (id: string) => `/acme/issues/${id}`,
  }),
}));
// AppLink reads the adapter from the context module directly, so the stand-in
// goes there rather than on the package index.
vi.mock("../navigation/context", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../navigation/context")>()),
  useNavigation: () => ({
    push: mocks.push,
    pathname: mocks.pathname,
    searchParams: new URLSearchParams("from=desktop"),
    hash: "#notes",
    getShareableUrl: (path: string) => "https://app.example.test" + path,
  }),
}));
vi.mock("../i18n", () => ({
  useT: (ns: string) => ({
    t: (pick: (copy: unknown) => string, vars?: Record<string, unknown>) => {
      const bundles: Record<string, unknown> = { common, epics, issues: issuesCopy, planning, sprints };
      return pick(bundles[ns]).replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars?.[key] ?? ""));
    },
  }),
  useLocale: () => "en",
  useTimeAgo: () => () => "2h ago",
}));
vi.mock("@multica/ui/lib/clipboard", () => ({ copyText: mocks.copy }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("../editor", () => ({ TitleEditor: () => null, ContentEditor: () => null }));
vi.mock("../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("../issues/surface/issue-surface", () => ({
  IssueSurface: ({ scope, modes }: { scope: unknown; modes: string[] }) => (
    <div data-testid="board" data-scope={JSON.stringify(scope)} data-modes={modes.join(",")} />
  ),
}));
vi.mock("../modals/issue-picker-modal", () => ({
  IssuePickerModal: ({ open, excludeIds, onSelect }: { open: boolean; excludeIds: string[]; onSelect: (issue: unknown) => void }) =>
    open ? (
      <button type="button" data-exclude={excludeIds.join(",")} onClick={() => onSelect({ id: "picked", identifier: "ACME-9" })}>
        pick existing
      </button>
    ) : null,
}));
vi.mock("@multica/core/modals", () => ({ useModalStore: { getState: () => ({ open: mocks.openModal }) } }));
vi.mock("@multica/core/issues/mutations", () => ({ useUpdateIssue: () => ({ mutate: mocks.updateIssue }) }));
vi.mock("@multica/core/issues/date", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/issues/date")>()),
  todayDateOnly: () => "2026-10-11",
}));
vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({ colorOf: () => null, iconOf: () => null, labelOf: (key: string) => key }),
}));
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: (type: string, id: string) => `${type} ${id}` }),
}));
vi.mock("@multica/core/epics", () => ({
  useUpdateEpic: () => ({ mutate: vi.fn() }),
  useDeleteEpic: () => ({ mutate: vi.fn(), isPending: false }),
  epicDetailOptions: () => ({ queryKey: ["epic"], queryFn: async () => mocks.epic }),
  epicOverviewOptions: () => ({ queryKey: ["epic-overview"], queryFn: async () => mocks.epicOverview }),
  epicIssuesOptions: () => ({
    queryKey: ["epic-issues"],
    queryFn: async () => ({ issues: mocks.issues, total: mocks.issuesTotal ?? mocks.issues.length }),
  }),
  epicListOptions: () => ({ queryKey: ["epics"], queryFn: async () => mocks.epicList }),
}));
vi.mock("@multica/core/sprints", () => ({
  useUpdateSprint: () => ({ mutate: mocks.updateSprint }),
  useDeleteSprint: () => ({ mutate: vi.fn(), isPending: false }),
  useCompleteSprint: () => ({ mutate: mocks.completeSprint, isPending: false }),
  sprintDetailOptions: () => ({ queryKey: ["sprint"], queryFn: async () => mocks.sprint }),
  sprintOverviewOptions: () => ({ queryKey: ["sprint-overview"], queryFn: async () => mocks.sprintOverview }),
  sprintIssuesOptions: () => ({
    queryKey: ["sprint-issues"],
    queryFn: async () => ({ issues: mocks.issues, total: mocks.issuesTotal ?? mocks.issues.length }),
  }),
  sprintListOptions: () => ({ queryKey: ["sprints"], queryFn: async () => mocks.sprintList }),
}));

function sprintRow(over: Partial<Sprint>): Sprint {
  return {
    id: "entity", workspace_id: "workspace", name: "Sprint 1", goal: null,
    start_date: "2026-10-09", end_date: "2026-10-16", status: "active",
    issue_count: 0, done_count: 0, created_at: "2026-10-01T10:00:00Z", updated_at: "2026-10-01T10:00:00Z",
    ...over,
  };
}

function issue(id: string, over: Record<string, unknown> = {}) {
  return {
    id, identifier: `ACME-${id}`, title: `Issue ${id}`, status: "todo", priority: "none",
    parent_issue_id: null, assignee_type: null, assignee_id: null, epic_id: null, sprint_id: null,
    ...over,
  };
}

function renderWithQuery(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

const groupKeys = () =>
  [...document.querySelectorAll("[data-group]")].map((node) => node.getAttribute("data-group"));

beforeEach(() => {
  mocks.pathname = "/acme/epics/entity";
  mocks.epic = {
    id: "entity", title: "Self-host move", description: "", color: "#6366f1", status: "open",
    issue_count: 4, done_count: 1, created_at: "2026-10-01T10:00:00Z", updated_at: "2026-10-01T10:00:00Z",
  };
  mocks.sprint = sprintRow({});
  mocks.epicOverview = {
    epic_id: "entity",
    progress: { ...ZERO, total: 4, done: 1, in_review: 1, blocked: 1, not_started: 1 },
    last_activity_at: "2026-10-11T08:00:00Z",
    assignees: [
      { assignee_type: "member", assignee_id: "u1", total: 3, done: 1, open: 2 },
      { assignee_type: null, assignee_id: null, total: 1, done: 0, open: 1 },
    ],
    sprints: [
      { sprint_id: "s1", name: "Sprint 1", status: "active", start_date: "2026-10-09", end_date: "2026-10-16", total: 3, done: 1 },
      { sprint_id: null, name: "", status: "planned", start_date: "", end_date: "", total: 1, done: 0 },
    ],
    attention: [
      { id: "b", identifier: "ACME-b", title: "Issue b", status: "blocked", bucket: "blocked", assignee_type: "member", assignee_id: "u1", updated_at: "" },
      { id: "r", identifier: "ACME-r", title: "Issue r", status: "in_review", bucket: "in_review", assignee_type: null, assignee_id: null, updated_at: "" },
    ],
    activity: [
      { id: "a1", issue_id: "r", issue_identifier: "ACME-r", issue_title: "Issue r", actor_type: "member", actor_id: "u1", action: "status_changed", details: { from: "todo", to: "in_review" }, created_at: "2026-10-11T08:00:00Z" },
    ],
  };
  mocks.sprintOverview = {
    sprint_id: "entity",
    progress: { ...ZERO, total: 5, done: 2, in_progress: 1, blocked: 1, not_started: 1 },
    last_activity_at: null,
    assignees: [{ assignee_type: "agent", assignee_id: "a1", total: 5, done: 2, open: 3 }],
    epics: [{ epic_id: "e1", title: "Self-host move", color: "#6366f1", status: "open", total: 5, done: 2 }],
    attention: [
      { id: "b", identifier: "ACME-b", title: "Issue b", status: "blocked", bucket: "blocked", assignee_type: null, assignee_id: null, updated_at: "" },
    ],
    burndown: {
      total: 5, start_date: "2026-10-09", end_date: "2026-10-16",
      days: [{ date: "2026-10-09", remaining: 5 }, { date: "2026-10-10", remaining: 3 }, { date: "2026-10-11", remaining: 2 }],
    },
  };
  mocks.issues = [
    issue("d", { status: "done", sprint_id: "s1" }),
    issue("b", { status: "blocked", sprint_id: "s1" }),
    issue("r", { status: "in_review", sprint_id: "s1" }),
    issue("p", { status: "todo" }),
    issue("c", { status: "todo", parent_issue_id: "p" }),
  ];
  mocks.issuesTotal = null;
  mocks.epicList = [{ id: "e1", title: "Self-host move", color: "#6366f1", status: "open" }];
  mocks.sprintList = [sprintRow({ id: "s1" })];
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("epic page", () => {
  it("shows the status progress, what needs attention, the grouped issues and the breakdowns", async () => {
    renderWithQuery(<EpicDetail epicId="entity" />);

    const attention = await screen.findByRole("region", { name: planning.attention.title });
    expect(within(attention).getByText("Issue b")).toBeInTheDocument();
    expect(within(attention).getByText("Issue r")).toBeInTheDocument();

    const progress = screen.getByRole("region", { name: planning.progress.title });
    // 1 done of 4, none cancelled.
    expect(within(progress).getByText("25% done")).toBeInTheDocument();
    expect(within(progress).getByText("1 of 4")).toBeInTheDocument();
    const bar = within(progress).getByRole("img");
    expect([...bar.children].map((segment) => segment.getAttribute("data-bucket"))).toEqual([
      "done", "in_review", "blocked", "not_started",
    ]);

    await screen.findByText("Issue p");
    expect(groupKeys()).toEqual(["in_review", "blocked", "not_started", "done"]);
    // The sub-issue sits under its parent, one level in.
    expect(screen.getByText("Issue c").closest("a")).toHaveAttribute("data-depth", "1");
    expect(screen.getByText("Issue p").closest("a")).toHaveAttribute("data-depth", "0");
    expect(screen.getByText("Issue p").closest("a")).toHaveAttribute("href", "/acme/issues/p");

    fireEvent.click(within(screen.getByRole("group", { name: planning.issues.group_by })).getByRole("button", { name: planning.issues.grouping.sprint }));
    expect(groupKeys()).toEqual(["s1", "__none__"]);
    fireEvent.click(within(screen.getByRole("group", { name: planning.issues.group_by })).getByRole("button", { name: planning.issues.grouping.assignee }));
    expect(groupKeys()).toEqual(["__none__"]);

    const summary = screen.getByRole("complementary");
    expect(within(summary).getByRole("region", { name: planning.sidebar.assignees })).toHaveTextContent("member u1");
    expect(within(summary).getByRole("region", { name: planning.sidebar.assignees })).toHaveTextContent("1 of 3");
    expect(within(summary).getByRole("region", { name: planning.sidebar.sprints })).toHaveTextContent(planning.issues.no_sprint);
    const activity = within(summary).getByRole("region", { name: planning.sidebar.activity });
    expect(activity).toHaveTextContent("member u1 moved to in_review");
    expect(activity).toHaveTextContent("ACME-r");
  });

  it("offers to create or link an issue when the epic is empty", async () => {
    mocks.epic = { ...mocks.epic, issue_count: 0, done_count: 0 };
    mocks.epicOverview = { epic_id: "entity", progress: ZERO, last_activity_at: null, assignees: [], sprints: [], attention: [], activity: [] };
    mocks.issues = [];
    renderWithQuery(<EpicDetail epicId="entity" />);

    expect(await screen.findByText(planning.empty.epic_title)).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: planning.progress.title })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: planning.attention.title })).not.toBeInTheDocument();
    expect(screen.getByText(planning.sidebar.no_activity)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: planning.issues.create }));
    expect(mocks.openModal).toHaveBeenCalledWith("create-issue", { epic_id: "entity" });

    fireEvent.click(screen.getByRole("button", { name: planning.issues.link }));
    fireEvent.click(screen.getByRole("button", { name: "pick existing" }));
    expect(mocks.updateIssue).toHaveBeenCalledWith({ id: "picked", epic_id: "entity" }, expect.anything());
  });

  it("says so when the list is cut short and never offers already linked issues", async () => {
    mocks.issuesTotal = 1200;
    renderWithQuery(<EpicDetail epicId="entity" />);
    expect(await screen.findByText("Showing the first 5 of 1200 issues. Open the board for the rest.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: planning.issues.link }));
    expect(screen.getByRole("button", { name: "pick existing" })).toHaveAttribute("data-exclude", "d,b,r,p,c");
  });

  it("copies a shareable route including query and hash", async () => {
    renderWithQuery(<EpicDetail epicId="entity" />);
    await screen.findByText("Open");
    expect(screen.getByRole("button", { name: epics.detail.delete_action })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: epics.detail.copy_link }));
    expect(mocks.copy).toHaveBeenCalledWith("https://app.example.test/acme/epics/entity?from=desktop#notes");
  });
});

describe("sprint page", () => {
  beforeEach(() => {
    mocks.pathname = "/acme/sprints/entity";
    mocks.issues = [issue("b", { status: "blocked", epic_id: "e1" }), issue("t", { status: "todo" })];
  });

  it("shows the day count, the burndown and the workload", async () => {
    renderWithQuery(<SprintDetail sprintId="entity" />);

    expect(await screen.findByText("Day 3 of 8 · Days left: 5")).toBeInTheDocument();
    expect(
      await screen.findByRole("img", { name: "Remaining issues per day: 2 of 5 left on Oct 11" }),
    ).toBeInTheDocument();

    const summary = screen.getByRole("complementary");
    expect(within(summary).getByRole("region", { name: planning.attention.title })).toHaveTextContent("Issue b");
    const workload = within(summary).getByRole("region", { name: planning.sidebar.workload });
    expect(workload).toHaveTextContent("agent a1");
    expect(workload).toHaveTextContent("Open: 3");
    expect(within(summary).getByRole("region", { name: planning.sidebar.epics })).toHaveTextContent("Self-host move");

    await screen.findByText("Issue t");
    fireEvent.click(within(screen.getByRole("group", { name: planning.issues.group_by })).getByRole("button", { name: planning.issues.grouping.epic }));
    expect(groupKeys()).toEqual(["e1", "__none__"]);
  });

  it("draws no burndown without data", async () => {
    mocks.sprintOverview = { ...mocks.sprintOverview, burndown: null };
    renderWithQuery(<SprintDetail sprintId="entity" />);
    await screen.findByRole("region", { name: planning.progress.title });
    expect(screen.queryByText(planning.sprint.burndown)).not.toBeInTheDocument();
  });

  it("keeps an empty sprint to its header and the two ways to add an issue", async () => {
    mocks.sprintOverview = { sprint_id: "entity", progress: ZERO, last_activity_at: null, assignees: [], epics: [], attention: [], burndown: null };
    mocks.issues = [];
    renderWithQuery(<SprintDetail sprintId="entity" />);
    expect(await screen.findByText(planning.empty.sprint_title)).toBeInTheDocument();
    expect(screen.queryByRole("complementary")).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: planning.progress.title })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: planning.issues.create }));
    expect(mocks.openModal).toHaveBeenCalledWith("create-issue", { sprint_id: "entity" });
  });

  it("opens the board on the sprint's own issues", async () => {
    renderWithQuery(<SprintDetail sprintId="entity" />);
    await screen.findByText("Issue t");
    fireEvent.click(within(screen.getByRole("group", { name: planning.issues.view_label })).getByRole("button", { name: planning.issues.view.board }));
    const board = screen.getByTestId("board");
    expect(JSON.parse(board.getAttribute("data-scope")!)).toEqual({ type: "sprint", sprintId: "entity" });
    expect(board).toHaveAttribute("data-modes", "board");
    expect(screen.queryByRole("group", { name: planning.issues.group_by })).not.toBeInTheDocument();
  });

  it("starts a planned sprint with a plain status update", async () => {
    mocks.sprint = sprintRow({ status: "planned" });
    renderWithQuery(<SprintDetail sprintId="entity" />);
    fireEvent.click(await screen.findByRole("button", { name: planning.sprint.start }));
    expect(mocks.updateSprint).toHaveBeenCalledWith({ id: "entity", status: "active" }, expect.anything());
    expect(screen.queryByRole("button", { name: planning.sprint.complete })).not.toBeInTheDocument();
  });

  it("completes only through the dialog, naming the choice and the target", async () => {
    mocks.sprintList = [sprintRow({}), sprintRow({ id: "next", name: "Sprint 2", status: "planned", start_date: "2026-10-16", end_date: "2026-10-23" })];
    renderWithQuery(<SprintDetail sprintId="entity" />);
    await screen.findByRole("region", { name: planning.progress.title });
    fireEvent.click(screen.getByRole("button", { name: planning.sprint.complete }));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("3 of 5 issues are not finished. Finished issues stay in this sprint.")).toBeInTheDocument();
    await waitFor(() => expect(within(dialog).getByRole("radio", { name: planning.complete.move })).toBeChecked());
    expect(mocks.updateSprint).not.toHaveBeenCalled();
    expect(mocks.completeSprint).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole("button", { name: planning.complete.confirm }));
    expect(mocks.completeSprint).toHaveBeenCalledWith(
      { id: "entity", open_issues: "move", target_sprint_id: "next" },
      expect.anything(),
    );
    expect(mocks.updateSprint).not.toHaveBeenCalled();
  });

  it("copies a shareable route including query and hash", async () => {
    renderWithQuery(<SprintDetail sprintId="entity" />);
    await screen.findByText("Active");
    expect(screen.getByRole("button", { name: sprints.detail.delete_action })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: sprints.detail.copy_link }));
    expect(mocks.copy).toHaveBeenCalledWith("https://app.example.test/acme/sprints/entity?from=desktop#notes");
  });
});

describe("complete sprint dialog", () => {
  const onClose = vi.fn();

  it("defaults to leaving issues in place when there is nowhere to move them", async () => {
    mocks.sprintList = [sprintRow({}), sprintRow({ id: "old", status: "completed" })];
    renderWithQuery(<CompleteSprintDialog sprint={sprintRow({})} openCount={3} totalCount={5} onClose={onClose} />);
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("radio", { name: planning.complete.move })).toBeDisabled();
    expect(within(dialog).getByRole("radio", { name: planning.complete.keep })).toBeChecked();
    expect(within(dialog).getByText(planning.complete.move_unavailable)).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: planning.complete.confirm }));
    expect(mocks.completeSprint).toHaveBeenLastCalledWith({ id: "entity", open_issues: "keep" }, expect.anything());

    fireEvent.click(within(dialog).getByRole("radio", { name: planning.complete.unlink }));
    fireEvent.click(within(dialog).getByRole("button", { name: planning.complete.confirm }));
    expect(mocks.completeSprint).toHaveBeenLastCalledWith({ id: "entity", open_issues: "unlink" }, expect.anything());
  });

  it("asks nothing and moves nothing when every issue is finished", async () => {
    mocks.sprintList = [sprintRow({}), sprintRow({ id: "next", status: "planned" })];
    renderWithQuery(<CompleteSprintDialog sprint={sprintRow({})} openCount={0} totalCount={5} onClose={onClose} />);
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(planning.complete.summary_none)).toBeInTheDocument();
    expect(within(dialog).queryByRole("radio")).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: planning.complete.confirm }));
    expect(mocks.completeSprint).toHaveBeenCalledWith({ id: "entity", open_issues: "keep" }, expect.anything());
  });

  it("closes only after the server confirms", async () => {
    renderWithQuery(<CompleteSprintDialog sprint={sprintRow({})} openCount={0} totalCount={5} onClose={onClose} />);
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: planning.complete.confirm }));
    const callbacks = mocks.completeSprint.mock.calls[0]![1];
    callbacks.onError(new Error("Server failure"));
    expect(onClose).not.toHaveBeenCalled();
    callbacks.onSuccess({ open_issues: "keep", kept_count: 0, moved_count: 0, unlinked_count: 0, target_sprint: null });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("ranks move targets like the server's default: next planned first", () => {
    const current = sprintRow({ id: "current", start_date: "2026-10-09" });
    const targets = sprintMoveTargets(current, [
      current,
      sprintRow({ id: "active", status: "active", start_date: "2026-10-10" }),
      sprintRow({ id: "earlier", status: "planned", start_date: "2026-09-01" }),
      sprintRow({ id: "later", status: "planned", start_date: "2026-11-01" }),
      sprintRow({ id: "next", status: "planned", start_date: "2026-10-16" }),
      sprintRow({ id: "done", status: "completed", start_date: "2026-10-20" }),
      sprintRow({ id: "cancelled", status: "cancelled", start_date: "2026-10-20" }),
    ]);
    expect(targets.map((sprint) => sprint.id)).toEqual(["next", "later", "earlier", "active"]);
  });
});
