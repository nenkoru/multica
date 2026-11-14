import { expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { streamKeys, type ScreenBlock } from "@multica/core/streams";
import { renderWithI18n } from "../../test/i18n";
import { TaskScreens } from "./task-screens";

vi.mock("@multica/core/api", () => ({ ApiError: class extends Error {}, api: { listScreenBlocks: vi.fn() } }));

const block = (id: string, seq: number): ScreenBlock => ({
  id, seq, task_id: "t", chat_session_id: "s", kind: "model", status: "pruned", issues: 1, created_at: "",
  a2ui: { version: "", root: "root", components: [{ id: "root", component: { Stack: { children: [] } } }] },
});

function show(blocks: ScreenBlock[], content: string) {
  const client = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
  client.setQueryData(streamKeys.screens("ws", "s"), { blocks });
  return renderWithI18n(<QueryClientProvider client={client}><TaskScreens wsId="ws" sessionId="s" taskId="t" content={content} /></QueryClientProvider>);
}

it("reloads an old pruned message with its original escaped source", () => {
  const source = 'root = Unknown("<script>oops</script>")';
  const { container, unmount } = show([block("b", 10)], `before\n\`\`\`openui\n${source}\n\`\`\`\nafter`);
  expect(screen.getByText(source)).toBeTruthy();
  expect(container.querySelector("script")).toBeNull();
  unmount();
  show([block("b", 10)], `\`\`\`openui\n${source}\n\`\`\``);
  expect(screen.getByText(source)).toBeTruthy();
});

it("shows readable source when delivery returned no screen records", () => {
  show([], '```openui\nroot = Broken()\n```');
  expect(screen.getByText("This screen could not be built.")).toBeTruthy();
  expect(screen.getByText("root = Broken()")).toBeTruthy();
});

it("keeps screen order and associates each source by model sequence", () => {
  const { container } = show([block("second", 11), block("first", 10)], '```openui\nroot = First()\n```\n```openui\nroot = Second()\n```');
  const screens = container.querySelectorAll("[data-screen]");
  expect([...screens].map((s) => s.getAttribute("data-screen"))).toEqual(["first", "second"]);
  expect(screens[0]?.textContent).toContain("First()");
  expect(screens[1]?.textContent).toContain("Second()");
});

it("leaves a missing earlier screen's source ahead of later stored screens", () => {
  const { container } = show([block("second", 11)], '```openui\nroot = First()\n```\n```openui\nroot = Second()\n```');
  expect([...container.querySelectorAll("pre")].map((p) => p.textContent)).toEqual(["root = First()", "root = Second()"]);
});
