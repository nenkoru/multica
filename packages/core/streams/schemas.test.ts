// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  FAILED_SCREEN_PRESS,
  ListScreenBlocksResponseSchema,
  ListStreamsResponseSchema,
  MultikaSchema,
  ScreenPressResultSchema,
  TranscriptionSchema,
} from "./schemas";
import { parseWithFallback } from "../api/schema";

describe("streams schemas", () => {
  it("defaults a malformed list to no streams", () => {
    expect(ListStreamsResponseSchema.parse({}).streams).toEqual([]);
    expect(
      parseWithFallback("nope", ListStreamsResponseSchema, { streams: [] }, { endpoint: "GET /api/streams" }).streams,
    ).toEqual([]);
  });

  it("reads a missing or malformed state as unknown, not running", () => {
    const [a, b] = ListStreamsResponseSchema.parse({
      streams: [{ id: "1", slug: "a" }, { id: "2", slug: "b", state: 7 }],
    }).streams;
    expect(a?.state).toBe("unknown");
    expect(b?.state).toBe("unknown");
  });

  it("keeps unknown stream states and fills missing fields", () => {
    const [s] = ListStreamsResponseSchema.parse({
      streams: [{ id: "1", slug: "tern", state: "hibernating" }],
    }).streams;
    expect(s).toMatchObject({ slug: "tern", state: "hibernating", title: "", state_label: "", active: true });
  });

  it("reads a disabled Мультика as enabled=false", () => {
    expect(MultikaSchema.parse({}).enabled).toBe(false);
    expect(MultikaSchema.parse({ enabled: true, chat_session_id: "c", focus_slug: "tern" })).toMatchObject({
      enabled: true,
      chat_session_id: "c",
      focus_slug: "tern",
    });
  });

  it("reads voice as off unless the server says so", () => {
    expect(MultikaSchema.parse({ enabled: true }).voice).toBe(false);
    expect(MultikaSchema.parse({ enabled: true, voice: true }).voice).toBe(true);
  });

  it("reads a transcription without text as empty", () => {
    expect(TranscriptionSchema.parse({}).text).toBe("");
    expect(TranscriptionSchema.parse({ text: "привет", extra: 1 }).text).toBe("привет");
  });

  it("reads screens as off unless the server says so", () => {
    expect(MultikaSchema.parse({ enabled: true }).screens).toBe(false);
    expect(MultikaSchema.parse({ enabled: true, screens: true }).screens).toBe(true);
  });

  it("keeps the list when one screen's doc is malformed", () => {
    const { blocks } = ListScreenBlocksResponseSchema.parse({
      blocks: [
        { id: "a", task_id: "t", a2ui: { root: "root", components: [{ id: "root", component: { Text: { text: "x" } } }] } },
        { id: "b", a2ui: { components: "nope" } },
      ],
    });
    expect(blocks.map((b) => b.id)).toEqual(["a", "b"]);
    expect(blocks[0]?.a2ui?.components).toHaveLength(1);
    expect(blocks[1]?.a2ui).toBeNull();
    expect(parseWithFallback(7, ListScreenBlocksResponseSchema, { blocks: [] }, { endpoint: "x" }).blocks).toEqual([]);
  });

  it("reads an unreadable press answer as failed, never done", () => {
    expect(parseWithFallback({}, ScreenPressResultSchema, FAILED_SCREEN_PRESS, { endpoint: "x" }).status).toBe("failed");
    expect(
      ScreenPressResultSchema.parse({ status: "confirm", confirm: { title: "Поднять бюджет?", text: "на $20" } }).confirm,
    ).toEqual({ window: "", title: "Поднять бюджет?", text: "на $20" });
  });
});
