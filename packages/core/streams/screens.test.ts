// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  LIVE_SCREEN_STATUS,
  applyScreenFinal,
  applyScreenLine,
  screensOfTask,
  standaloneScreens,
  stripScreenSource,
} from "./screens";
import type { ScreenBlock, ScreenDoc } from "./schemas";
import { chartRows, formatAmount, indexDoc } from "./screen-props";

const node = (id: string, name = "Text", props: Record<string, unknown> = { text: id }) => ({
  id,
  component: { [name]: props },
});

describe("screen cache", () => {
  it("grows a live screen line by line and ignores a redelivered line", () => {
    let d = applyScreenLine(undefined, { task_id: "t", chat_session_id: "s", block: 0, line: 0, nodes: [node("root", "Stack", { children: ["a"] })], root: "root" });
    d = applyScreenLine(d, { task_id: "t", chat_session_id: "s", block: 0, line: 1, nodes: [node("a")] });
    d = applyScreenLine(d, { task_id: "t", chat_session_id: "s", block: 0, line: 1, nodes: [node("a")] });
    expect(d.blocks).toHaveLength(1);
    expect(d.blocks[0]).toMatchObject({ status: LIVE_SCREEN_STATUS, seq: 10, task_id: "t" });
    expect(d.blocks[0]?.a2ui?.root).toBe("root");
    expect(d.blocks[0]?.a2ui?.components.map((n) => n.id)).toEqual(["root", "a"]);
  });

  it("takes the first line as root when no line is called root", () => {
    const d = applyScreenLine(undefined, { task_id: "t", chat_session_id: "s", block: 1, line: 0, nodes: [node("k")] });
    expect(d.blocks[0]?.a2ui?.root).toBe("k");
    expect(d.blocks[0]?.seq).toBe(11);
  });

  it("replaces the live draft with the stored screen, and a late line does not bring it back", () => {
    let d = applyScreenLine({ blocks: [] }, { task_id: "t", chat_session_id: "s", block: 0, line: 0, nodes: [node("root")] });
    const stored: ScreenBlock = {
      id: "b1", task_id: "t", chat_session_id: "s", seq: 10, kind: "model", status: "valid",
      a2ui: { version: "a2ui-0.8+fabrika-1", root: "root", components: [node("root")] }, issues: 0, created_at: "",
    };
    d = applyScreenFinal(d, { task_id: "t", chat_session_id: "s", block: stored });
    expect(d.blocks.map((b) => b.id)).toEqual(["b1"]);
    d = applyScreenLine(d, { task_id: "t", chat_session_id: "s", block: 0, line: 1, nodes: [node("x")] });
    expect(d.blocks.map((b) => b.id)).toEqual(["b1"]);
    d = applyScreenFinal(d, { task_id: "t", chat_session_id: "s", block: stored });
    expect(d.blocks).toHaveLength(1);
  });

  it("orders a reply's screens and separates the digest", () => {
    const b = (id: string, task_id: string, seq: number) =>
      ({ id, task_id, seq, chat_session_id: "s", kind: "model", status: "valid", issues: 0, created_at: "" }) as ScreenBlock;
    const all = [b("m2", "t", 11), b("d", "", 0), b("c", "t", 0), b("m1", "t", 10), b("x", "u", 10)];
    expect(screensOfTask(all, "t").map((x) => x.id)).toEqual(["c", "m1", "m2"]);
    expect(standaloneScreens(all).map((x) => x.id)).toEqual(["d"]);
  });

  it("hides screen source, closed or still open", () => {
    expect(stripScreenSource("Вот:\n```openui\nroot = Text(\"x\")\n```\n\nИтог.")).toBe("Вот:\n\nИтог.");
    expect(stripScreenSource("Вот:\n```openui\nroot = Te")).toBe("Вот:");
    expect(stripScreenSource("```go\nx\n```")).toBe("```go\nx\n```");
  });
});

describe("screen props", () => {
  it("reads known components and drops unknown or broken ones", () => {
    const doc: ScreenDoc = {
      version: "",
      root: "root",
      components: [
        { id: "root", component: { Stack: { children: ["k", "m", "x"] } } },
        { id: "k", component: { Kpi: { label: "Всего", value: "$4" } } },
        { id: "m", component: { Marquee: { text: "x" } } },
        { id: "x", component: { Actions: { buttons: [{ label: "без действия" }] } } },
      ],
    };
    const idx = indexDoc(doc);
    expect([...idx.keys()]).toEqual(["root", "k"]);
    const root = idx.get("root");
    expect(root?.name === "Stack" && root.props.direction).toBe("column");
  });

  it("lays out chart rows by label", () => {
    expect(chartRows(["пн", "вт"], [{ name: "Терн", values: [1, 2] }, { name: "Работа", values: [3] }])).toEqual([
      { label: "пн", s0: 1, s1: 3 },
      { label: "вт", s0: 2, s1: 0 },
    ]);
    expect(formatAmount(4.5, "$")).toBe("$4.50");
    expect(formatAmount(3, "шаг")).toBe("3 шаг");
  });
});
