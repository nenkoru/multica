import { act, fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { TvGameController, silentPlayer } from "./controller";
import { emptySnapshot } from "./model";
import { TvGameLayer } from "./tv-game-layer";

const NOON = Date.UTC(2026, 9, 14, 9, 0, 0);

function setup() {
  HTMLCanvasElement.prototype.getContext = vi.fn(() => null) as never;
  const player = silentPlayer();
  vi.spyOn(player, "stop");
  const controller = new TvGameController({ player, now: () => NOON });
  renderWithI18n(<TvGameLayer controller={controller} />);
  return { controller, player };
}

describe("TvGameLayer", () => {
  it("shows Supply and Done and asks to enable sound", async () => {
    const { controller } = setup();
    act(() => controller.update({ ...emptySnapshot(NOON), runningRuns: 5, hostCeiling: 12, doneInSprint: 7 }));
    expect(screen.getByText("5/12")).toBeInTheDocument();
    expect(screen.getByText("7")).toBeInTheDocument();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /enable sound/i }));
    });
    expect(screen.getByRole("button", { name: /quiet for 1 h/i })).toBeInTheDocument();
  });

  it("acknowledges a flashing plate by click and by any key", async () => {
    const { controller, player } = setup();
    act(() => controller.update({ ...emptySnapshot(NOON) }));
    act(() =>
      controller.update({ ...emptySnapshot(NOON), health: [{ key: "site", status: "warning", count: 2 }] }),
    );
    const plate = screen.getByRole("button", { name: /warning: сайт недоступен/i });
    expect(plate.className).toContain("animate-pulse");
    fireEvent.click(plate);
    expect(player.stop).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: /warning: сайт недоступен/i }).className).not.toContain("animate-pulse");

    act(() => controller.pushEvents([{ type: "need_you", issueId: "n-1", title: "x" }]));
    // The first key also unlocks audio, which settles asynchronously.
    await act(async () => {
      fireEvent.keyDown(window, { key: "Enter" });
    });
    expect(player.stop).toHaveBeenCalledTimes(2);
  });
});
