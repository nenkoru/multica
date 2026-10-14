import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useTvRealtime } from "./use-tv-realtime";

class FakeSocket {
  static last: FakeSocket | null = null;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  constructor() {
    FakeSocket.last = this;
  }
  close() {}
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("useTvRealtime", () => {
  it("refreshes within 3 s even when change signals never pause", () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeSocket as unknown as typeof WebSocket);
    const onChange = vi.fn();
    renderHook(() => useTvRealtime("wss://x/api/tv/ws", onChange));
    for (let t = 0; t < 3_500; t += 500) {
      act(() => {
        FakeSocket.last!.onmessage!({ data: '{"type":"tv:changed"}' });
        vi.advanceTimersByTime(500);
      });
    }
    expect(onChange).toHaveBeenCalledTimes(1);
  });
});
