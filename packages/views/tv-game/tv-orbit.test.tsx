import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { EMPTY_TV_OVERVIEW, type TvOverview } from "@multica/core/tv";
import { TvGameController, silentPlayer } from "./controller";
import { emptySnapshot } from "./model";
import { TvOrbit } from "./tv-orbit";

const NOW = Date.UTC(2026, 9, 14, 13, 40, 0);

function setup(overview: TvOverview = EMPTY_TV_OVERVIEW) {
  HTMLCanvasElement.prototype.getContext = vi.fn(() => null) as never;
  const player = silentPlayer();
  vi.spyOn(player, "stop");
  const controller = new TvGameController({ player, now: () => NOW });
  render(<TvOrbit controller={controller} overview={overview} now={new Date(NOW)} />);
  return { controller, player };
}

describe("TvOrbit", () => {
  it("is calm, counts today's wins and asks to enable sound", async () => {
    const { controller } = setup();
    act(() => controller.update({ ...emptySnapshot(NOW), doneToday: 5, streakDays: 3 }));
    expect(screen.getByTestId("tv-orbit").dataset.mood).toBe("calm");
    expect(screen.getByTestId("orbit-say").textContent).toBe("Всё спокойно.");
    expect(screen.getByText(/Сегодня закрыто: 5 ●●●●●/)).toBeInTheDocument();
    expect(screen.getByText(/серия 3 дня/)).toBeInTheDocument();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /Включить звук/ }));
    });
    expect(screen.getByRole("button", { name: /Тихо на 1 ч/ })).toBeInTheDocument();
  });

  it("turns red on a warning and a tap acknowledges it", async () => {
    const { controller, player } = setup();
    act(() => controller.update({ ...emptySnapshot(NOW) }));
    act(() => controller.update({ ...emptySnapshot(NOW), health: [{ key: "daemon", status: "warning", count: 1 }] }));
    expect(screen.getByTestId("tv-orbit").dataset.mood).toBe("alarm");
    expect(screen.getByTestId("orbit-say").textContent).toMatch(/^Mika зовёт: Демон Multica не на связи/);
    await act(async () => {
      fireEvent.pointerDown(window);
    });
    expect(player.stop).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Сигнал принят, ждём исправления")).toBeInTheDocument();
  });

  it("focus mode keeps only the orb and what needs you", () => {
    const overview: TvOverview = {
      ...EMPTY_TV_OVERVIEW,
      needs_owner_total: 1,
      needs_owner: [{ id: "n", identifier: "R-1", title: "Выбрать шаблон", status: "blocked", assignee_type: null, assignee_name: null, since: new Date(NOW).toISOString(), last_activity_at: "", stale: false }],
    };
    const { controller } = setup(overview);
    act(() => controller.update({ ...emptySnapshot(NOW) }));
    expect(screen.getByTestId("tv-orbit").dataset.mood).toBe("call");
    expect(screen.getByText(/Одно решение:/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Фокус" }));
    expect(screen.queryByText(/Одно решение:/)).not.toBeInTheDocument();
    expect(screen.getAllByText("Выбрать шаблон").length).toBeGreaterThan(0);
  });
});
