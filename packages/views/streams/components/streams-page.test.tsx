import { expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { NavigationProvider } from "../../navigation";
import { renderWithI18n } from "../../test/i18n";
import { StreamsPage } from "./streams-page";

vi.mock("@multica/core/api", () => ({
  api: { listStreams: vi.fn(), getMultika: vi.fn(), setMultikaFocus: vi.fn() },
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws" }));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ chat: () => "/ws/chat", streams: () => "/ws/streams" }),
}));

const listStreams = vi.mocked(api.listStreams);
const getMultika = vi.mocked(api.getMultika);
const setMultikaFocus = vi.mocked(api.setMultikaFocus);

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={client}>
      <NavigationProvider
        value={{
          pathname: "/ws/streams",
          searchParams: new URLSearchParams(),
          hash: "",
          push: () => {},
          replace: () => {},
          back: () => {},
          getShareableUrl: (v) => v,
        }}
      >
        <StreamsPage />
      </NavigationProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  listStreams.mockReset();
  getMultika.mockReset();
  setMultikaFocus.mockReset().mockResolvedValue(undefined);
});

it("shows streams, Мультика's focus and the way into the one chat", async () => {
  listStreams.mockResolvedValue({
    streams: [
      { id: "1", slug: "tern", title: "Терн", state: "running", state_label: "идёт", reason: "", summary: "ноды живы", active: true },
      { id: "2", slug: "work", title: "Работа", state: "asking", state_label: "ждёт тебя", reason: "вопрос 1", summary: "", active: true },
    ],
  });
  getMultika.mockResolvedValue({ enabled: true, chat_session_id: "cs-1", focus_slug: "tern", focus_title: "Терн" });
  renderPage();

  expect(await screen.findByTestId("stream-tern")).toHaveTextContent("ноды живы");
  expect(screen.getByTestId("stream-work")).toHaveTextContent("ждёт тебя");
  expect(await screen.findByText("Focus: Терн")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Open chat" })).toHaveAttribute("href", "/ws/chat?session=cs-1");

  // The focused stream offers no "Focus" button; the other one does.
  const focusButtons = screen.getAllByRole("button", { name: "Focus" });
  expect(focusButtons).toHaveLength(1);
  fireEvent.click(focusButtons[0]!);
  await waitFor(() => expect(setMultikaFocus).toHaveBeenCalledWith("work"));

  fireEvent.click(screen.getByRole("button", { name: "Clear focus" }));
  await waitFor(() => expect(setMultikaFocus).toHaveBeenCalledWith(""));
});

it("says the streams API is missing instead of failing", async () => {
  listStreams.mockRejectedValue(new Error("404"));
  getMultika.mockRejectedValue(new Error("404"));
  renderPage();
  expect(await screen.findByText("Streams are not available on this server.")).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: "Open chat" })).not.toBeInTheDocument();
});
