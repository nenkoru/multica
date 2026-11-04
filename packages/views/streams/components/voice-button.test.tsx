import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { toast } from "sonner";
import { renderWithI18n } from "../../test/i18n";
import { VoiceButton, extensionFor } from "./voice-button";

vi.mock("@multica/core/api", () => ({ api: { transcribeMultikaVoice: vi.fn() } }));
vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));

const transcribe = vi.mocked(api.transcribeMultikaVoice);
const stopTrack = vi.fn();

class FakeRecorder {
  static last: FakeRecorder | null = null;
  state: "inactive" | "recording" = "inactive";
  mimeType = "audio/ogg;codecs=opus";
  ondataavailable: ((e: { data: Blob }) => void) | null = null;
  onstop: (() => void) | null = null;
  constructor(public stream: MediaStream) {
    FakeRecorder.last = this;
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

it("names the file after the recorded format", () => {
  expect(extensionFor("audio/webm;codecs=opus")).toBe("webm");
  expect(extensionFor("audio/mp4")).toBe("m4a");
  expect(extensionFor("audio/ogg")).toBe("ogg");
});
