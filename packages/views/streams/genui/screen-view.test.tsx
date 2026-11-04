import { afterEach, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type { ScreenBlock } from "@multica/core/streams";
import { renderWithI18n } from "../../test/i18n";
import { ScreenView } from "./screen-view";

vi.mock("@multica/core/api", () => {
  class ApiError extends Error {
    constructor(
      message: string,
      readonly status: number,
    ) {
      super(message);
    }
  }
  return { ApiError, api: { pressScreenAction: vi.fn(), cancelScreenAction: vi.fn() } };
});
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));

const press = vi.mocked(api.pressScreenAction);
const cancel = vi.mocked(api.cancelScreenAction);

afterEach(() => vi.clearAllMocks());

function block(status: string): ScreenBlock {
  return {
    id: "b1",
    task_id: "t1",
    chat_session_id: "s1",
    seq: 10,
    kind: "model",
    status,
    issues: 0,
    created_at: "",
    a2ui: {
      version: "a2ui-0.8+fabrika-1",
      root: "root",
      components: [
        { id: "root", component: { Stack: { children: ["kpis", "acts", "ghost"] } } },
        { id: "kpis", component: { KpiStrip: { items: ["kpis~0"] } } },
        { id: "kpis~0", component: { Kpi: { label: "API за 14 дней", value: "$4.50" } } },
        {
          id: "acts",
          component: {
            Actions: {
              buttons: [
                { label: "Урезать долю вдвое", action: { name: "set_share", args: { stream: "tern", factor: 0.5 } }, zone: "normal", action_id: "a-cut" },
                { label: "Поднять на $20", action: { name: "raise_budget", args: { stream: "tern", usd: 20 } }, zone: "red", action_id: "a-raise" },
                { label: "Спросить", action: { name: "followup", args: { text: "почему" } }, zone: "normal", action_id: "a-ask" },
              ],
            },
          },
        },
      ],
    },
  };
}

function renderScreen(status: string, onFollowup = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  renderWithI18n(
    <QueryClientProvider client={client}>
      <ScreenView block={block(status)} onFollowup={onFollowup} />
    </QueryClientProvider>,
  );
  return onFollowup;
}

it("draws a live screen with its buttons off until it is stored", () => {
  renderScreen("live");
  expect(screen.getByText("$4.50")).toBeTruthy();
  expect(screen.getByRole("button", { name: /Урезать долю вдвое/ }).hasAttribute("disabled")).toBe(true);
});

it("presses a stored button once", async () => {
  press.mockResolvedValue({ status: "done", result: { message: "Доля «Терн»: 20% потолка" } });
  renderScreen("valid");
  const cut = screen.getByRole("button", { name: /Урезать долю вдвое/ });
  fireEvent.click(cut);
  await waitFor(() => expect(press).toHaveBeenCalledWith("a-cut", false));
  await waitFor(() => expect(cut.hasAttribute("disabled")).toBe(true));
  fireEvent.click(cut);
  expect(press).toHaveBeenCalledTimes(1);
});

it("asks in the confirmation window before a red action and cancels on close", async () => {
  press.mockResolvedValueOnce({ status: "confirm", confirm: { title: "Красная зона: подтверди", text: "Поднять бюджет на $20.00" } });
  cancel.mockResolvedValue({ status: "cancelled" });
  renderScreen("valid");
  fireEvent.click(screen.getByRole("button", { name: /Поднять на \$20/ }));
  expect(await screen.findByText("Поднять бюджет на $20.00")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(cancel).toHaveBeenCalledWith("a-raise"));

  press.mockResolvedValueOnce({ status: "confirm", confirm: { title: "Красная зона: подтверди", text: "Поднять бюджет на $20.00" } });
  press.mockResolvedValueOnce({ status: "done", result: {} });
  fireEvent.click(screen.getByRole("button", { name: /Поднять на \$20/ }));
  fireEvent.click(await screen.findByRole("button", { name: "Confirm" }));
  await waitFor(() => expect(press).toHaveBeenLastCalledWith("a-raise", true));
  expect(cancel).toHaveBeenCalledTimes(1);
});

it("sends a follow-up button's text as the next message", async () => {
  press.mockResolvedValue({ status: "done", result: { followup: "почему" } });
  const onFollowup = renderScreen("valid");
  fireEvent.click(screen.getByRole("button", { name: /Спросить/ }));
  await waitFor(() => expect(onFollowup).toHaveBeenCalledWith("From the screen: почему"));
});

it("says so when the screen could not be built", () => {
  const client = new QueryClient();
  renderWithI18n(
    <QueryClientProvider client={client}>
      <ScreenView block={{ ...block("failed"), a2ui: null }} />
    </QueryClientProvider>,
  );
  expect(screen.getByText("This screen could not be built.")).toBeTruthy();
});
