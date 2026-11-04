import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { toast } from "sonner";
import { renderWithI18n } from "../../test/i18n";
import { PERMISSION_WAIT_MS, VoiceButton, extensionFor } from "./voice-button";

vi.mock("@multica/core/api", () => ({ api: { transcribeMultikaVoice: vi.fn() } }));
vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));

const transcribe = vi.mocked(api.transcribeMultikaVoice);
const stopTrack = vi.fn();

class FakeRecorder {
  static last: FakeRecorder | null = null;
  state: "inactive" | "recording" = "inactive";
  mimeType = "audio/ogg;codecs=opus";
  static created = 0;
  ondataavailable: ((e: { data: Blob }) => void) | null = null;
  onstop: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public stream: MediaStream) {
    FakeRecorder.last = this;
    FakeRecorder.created++;
  }
  start() {
    this.state = "recording";
  }
  stop() {
    this.state = "inactive";
    this.ondataavailable?.({ data: new Blob(["sound"]) });
    this.onstop?.();
  }
}

beforeEach(() => {
  FakeRecorder.last = null;
  FakeRecorder.created = 0;
  vi.stubGlobal("MediaRecorder", FakeRecorder);
  Object.defineProperty(navigator, "mediaDevices", {
    configurable: true,
    value: { getUserMedia: vi.fn().mockResolvedValue({ getTracks: () => [{ stop: stopTrack }] }) },
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

function renderButton(onText: (text: string) => void) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={client}>
      <VoiceButton onText={onText} />
    </QueryClientProvider>,
  );
}

it("records, transcribes and hands the text to the draft without sending", async () => {
  transcribe.mockResolvedValue("  привет, Мультика  ");
  const onText = vi.fn();
  renderButton(onText);

  fireEvent.click(screen.getByRole("button", { name: "Dictate" }));
  fireEvent.click(await screen.findByRole("button", { name: "Stop recording" }));

  await waitFor(() => expect(onText).toHaveBeenCalledWith("привет, Мультика"));
  expect(transcribe).toHaveBeenCalledWith(expect.any(Blob), "voice.ogg");
  expect(stopTrack).toHaveBeenCalled();
  expect(await screen.findByRole("button", { name: "Dictate" })).toBeTruthy();
});

it("reports a failed transcription and keeps the draft untouched", async () => {
  transcribe.mockRejectedValue(new Error("502"));
  const onText = vi.fn();
  renderButton(onText);

  fireEvent.click(screen.getByRole("button", { name: "Dictate" }));
  fireEvent.click(await screen.findByRole("button", { name: "Stop recording" }));

  await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Could not transcribe the recording."));
  expect(onText).not.toHaveBeenCalled();
});

it("says so when the microphone is refused", async () => {
  vi.mocked(navigator.mediaDevices.getUserMedia).mockRejectedValue(new Error("denied"));
  renderButton(vi.fn());
  fireEvent.click(screen.getByRole("button", { name: "Dictate" }));
  await waitFor(() => expect(toast.error).toHaveBeenCalledWith("No access to the microphone."));
  expect(FakeRecorder.last).toBeNull();
});

it("opens one recorder however often it is clicked during the permission prompt", async () => {
  let grant: (s: unknown) => void = () => {};
  vi.mocked(navigator.mediaDevices.getUserMedia).mockReturnValue(
    new Promise((resolve) => (grant = resolve as (s: unknown) => void)),
  );
  renderButton(vi.fn());
  const button = screen.getByRole("button", { name: "Dictate" });
  fireEvent.click(button);
  fireEvent.click(button);
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(true));
  grant({ getTracks: () => [{ stop: stopTrack }] });
  await screen.findByRole("button", { name: "Stop recording" });
  expect(navigator.mediaDevices.getUserMedia).toHaveBeenCalledTimes(1);
  expect(FakeRecorder.created).toBe(1);
});

it("releases a microphone granted after the button is gone", async () => {
  let grant: (s: unknown) => void = () => {};
  vi.mocked(navigator.mediaDevices.getUserMedia).mockReturnValue(
    new Promise((resolve) => (grant = resolve as (s: unknown) => void)),
  );
  const onText = vi.fn();
  const view = renderButton(onText);
  fireEvent.click(screen.getByRole("button", { name: "Dictate" }));
  view.unmount();
  grant({ getTracks: () => [{ stop: stopTrack }] });
  await waitFor(() => expect(stopTrack).toHaveBeenCalled());
  expect(FakeRecorder.created).toBe(0);
  expect(onText).not.toHaveBeenCalled();
});

it("gets back to idle when the recorder fails", async () => {
  renderButton(vi.fn());
  fireEvent.click(screen.getByRole("button", { name: "Dictate" }));
  await screen.findByRole("button", { name: "Stop recording" });
  FakeRecorder.last!.onerror?.();
  expect(await screen.findByRole("button", { name: "Dictate" })).toBeTruthy();
  expect(toast.error).toHaveBeenCalledWith("Could not transcribe the recording.");
  expect(stopTrack).toHaveBeenCalled();
  expect(transcribe).not.toHaveBeenCalled();
});

it("stays usable when the recorder cannot be created", async () => {
  class BrokenRecorder {
    constructor() {
      throw new DOMException("no codec", "NotSupportedError");
    }
  }
  vi.stubGlobal("MediaRecorder", BrokenRecorder);
  renderButton(vi.fn());
  fireEvent.click(screen.getByRole("button", { name: "Dictate" }));
  await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Could not transcribe the recording."));
  const button = await screen.findByRole("button", { name: "Dictate" });
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
  expect(stopTrack).toHaveBeenCalled();
});

it("frees the button when the permission prompt never answers, and drops a late grant", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    let grant: (s: unknown) => void = () => {};
    vi.mocked(navigator.mediaDevices.getUserMedia).mockReturnValueOnce(
      new Promise((resolve) => (grant = resolve as (s: unknown) => void)),
    );
    renderButton(vi.fn());
    const button = screen.getByRole("button", { name: "Dictate" }) as HTMLButtonElement;
    fireEvent.click(button);
    await waitFor(() => expect(button.disabled).toBe(true));
    await vi.advanceTimersByTimeAsync(PERMISSION_WAIT_MS + 1);
    await waitFor(() => expect(button.disabled).toBe(false));
    grant({ getTracks: () => [{ stop: stopTrack }] });
    await waitFor(() => expect(stopTrack).toHaveBeenCalled());
    expect(FakeRecorder.created).toBe(0);
    // A new click works with a fresh prompt.
    fireEvent.click(button);
    await screen.findByRole("button", { name: "Stop recording" });
    expect(FakeRecorder.created).toBe(1);
  } finally {
    vi.useRealTimers();
  }
});

it("names the file after the recorded format", () => {
  expect(extensionFor("audio/webm;codecs=opus")).toBe("webm");
  expect(extensionFor("audio/mp4")).toBe("m4a");
  expect(extensionFor("audio/ogg")).toBe("ogg");
});
