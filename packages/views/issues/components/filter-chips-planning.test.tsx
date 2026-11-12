import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { createStore } from "zustand/vanilla";
import { buildIssueStatusCatalog } from "@multica/core/issue-statuses";
import { baselineFromQuery } from "@multica/core/issue-views/baseline";
import { type IssueViewState, viewStoreSlice } from "@multica/core/issues/stores/view-store";
import { ViewStoreProvider } from "@multica/core/issues/stores/view-store-context";
import { renderWithI18n } from "../../test/i18n";
import { useFilterChips } from "./filter-chips-bar";

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => buildIssueStatusCatalog([]),
}));
vi.mock("@tanstack/react-query", async (original) => ({
  ...await original<typeof import("@tanstack/react-query")>(),
  useQuery: () => ({ data: [] }),
}));

describe("saved planning view chips", () => {
  it("keeps epic and sprint membership when removing an added status", () => {
    const baseline = baselineFromQuery({
      epicFilters: ["epic-A"], includeNoEpic: true,
      sprintFilters: ["sprint-A"], includeNoSprint: true,
    });
    const store = createStore<IssueViewState>()(viewStoreSlice);
    store.getState().resetFiltersTo(baseline.raw);
    store.getState().toggleStatusFilter("todo");
    function Chips() {
      const { chips } = useFilterChips(null, undefined, baseline);
      return <>{chips.map((chip) => <button key={chip.key} onClick={chip.onRemove}>{chip.key}</button>)}</>;
    }
    renderWithI18n(<ViewStoreProvider store={store}><Chips /></ViewStoreProvider>);
    fireEvent.click(screen.getByRole("button", { name: "status" }));
    expect(store.getState()).toMatchObject({
      statusFilters: [], epicFilters: ["epic-A"], includeNoEpic: true,
      sprintFilters: ["sprint-A"], includeNoSprint: true,
    });
  });
});
